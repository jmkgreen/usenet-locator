// Package httpapi exposes the application HTTP boundary. Business services are
// introduced behind this package rather than being embedded in HTTP handlers.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/indexing"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/providers"
	"github.com/jmkgreen/usenet-locator/backend/internal/qualification"
	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
	"github.com/jmkgreen/usenet-locator/backend/internal/timeline"
	"github.com/jmkgreen/usenet-locator/backend/internal/watchlist"
)

type Readiness func(context.Context) error
type BodyFetcher interface {
	GetOrFetch(context.Context, int64) (string, error)
	GetCached(context.Context, int64) (string, error)
}
type MetricSnapshot struct {
	DBAcquiredConns int32
	DBIdleConns     int32
	DBAcquireCount  int64
}
type MetricSource func() MetricSnapshot
type RetentionService interface {
	Probe(context.Context, string) ([]retention.Observation, error)
	List(context.Context, string) ([]retention.Observation, error)
	RetrieveNext(context.Context, string, int) error
}
type WatchlistService interface {
	List(context.Context) ([]watchlist.Item, error)
	Add(context.Context, string, int) (watchlist.Item, error)
	Remove(context.Context, string) error
}
type TimelineService interface {
	List(context.Context, string, string, *time.Time) ([]timeline.Period, error)
	Complete(context.Context, string, time.Time, time.Time) (string, error)
}

// NewHandler returns the base Stage 1 HTTP router without optional services.
func NewHandler(version string) http.Handler {
	return NewHandlerWithReadiness(version, func(context.Context) error { return nil })
}

// WithStaticFiles serves a compiled single-page application without allowing
// it to shadow API or probe routes. Unknown client-side routes receive the SPA
// entry point; requested assets continue to receive normal file-server status.
func WithStaticFiles(api http.Handler, assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			api.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" || !strings.ContainsRune(strings.TrimPrefix(r.URL.Path, "/"), '.') {
			index, err := fs.ReadFile(assets, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(index)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// WithBodyRetrieval adds the explicit text-body action without making cached
// body fetching part of ordinary header/detail reads.
func WithBodyRetrieval(next http.Handler, fetcher BodyFetcher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const articlePrefix = "/api/v1/articles/"
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, articlePrefix) && strings.HasSuffix(r.URL.Path, "/body/download") {
			idText := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, articlePrefix), "/body/download")
			id, err := strconv.ParseInt(idText, 10, 64)
			if err != nil || id < 1 {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
				return
			}
			body, err := fetcher.GetCached(r.Context(), id)
			if err != nil {
				switch {
				case errors.Is(err, articles.ErrNotFound):
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
				case errors.Is(err, articles.ErrBodyNotCached):
					writeJSON(w, http.StatusConflict, map[string]string{"error": "article text has not been retrieved"})
				default:
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load cached article text"})
				}
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=article-%d.txt", id))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = io.WriteString(w, body)
			return
		}
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, articlePrefix) || !strings.HasSuffix(r.URL.Path, "/body") {
			next.ServeHTTP(w, r)
			return
		}
		idText := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, articlePrefix), "/body")
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id < 1 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			return
		}
		body, err := fetcher.GetOrFetch(r.Context(), id)
		if err != nil {
			switch {
			case errors.Is(err, articles.ErrNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			case errors.Is(err, articles.ErrUnwanted):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "article is marked unwanted; clear the mark before retrieval"})
			case errors.Is(err, accounts.ErrQuotaExceeded):
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "configured provider transfer quota has been reached"})
			default:
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not retrieve article body"})
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"text": body})
	})
}

func WithProviderStatus(next http.Handler, lister providers.Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/providers" {
			items, err := lister.List(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load provider status"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"endpoints": items})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WithProviderPreflight exposes a small, explicitly invoked compatibility
// check. It never returns credentials, Message-IDs, or provider response text.
func WithProviderPreflight(next http.Handler, service qualification.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/providers/") && strings.HasSuffix(r.URL.Path, "/qualifications") {
			endpoint := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/providers/"), "/qualifications")
			if service.History == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "qualification history unavailable"})
				return
			}
			items, err := service.History.List(r.Context(), endpoint)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load qualification history"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"qualifications": items})
			return
		}
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/v1/providers/") || !strings.HasSuffix(r.URL.Path, "/preflight") {
			next.ServeHTTP(w, r)
			return
		}
		endpoint := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/providers/"), "/preflight")
		var body struct {
			MessageID     string `json:"message_id"`
			Newsgroup     string `json:"newsgroup"`
			ArticleNumber int64  `json:"article_number"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
			return
		}
		result, err := service.Run(r.Context(), endpoint, body.MessageID, body.Newsgroup, body.ArticleNumber)
		if err != nil {
			if errors.Is(err, accounts.ErrQuotaExceeded) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "configured provider transfer quota has been reached"})
			} else {
				// qualification.Service emits only fixed stage labels; exposing that
				// label makes a compatibility failure actionable without returning
				// credentials, Message-IDs, or provider-supplied response text.
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}

func WithStorageBrowser(next http.Handler, groups articles.GroupLister, coverage indexing.CoverageLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/newsgroups":
			items, err := groups.ListGroups(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list newsgroups"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"newsgroups": items})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/coverage":
			items, err := coverage.ListCoverage(r.Context(), r.URL.Query().Get("newsgroup"))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list coverage"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"coverage": items})
			return
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// WithTimeline exposes the aggregate calendar while retaining the underlying
// endpoint-specific evidence in every response.
func WithTimeline(next http.Handler, service TimelineService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/newsgroups/"
		if !strings.HasPrefix(r.URL.Path, prefix) || !strings.Contains(r.URL.Path, "/timeline") {
			next.ServeHTTP(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, prefix)
		if r.Method == http.MethodGet && strings.HasSuffix(path, "/timeline") {
			group := strings.TrimSuffix(path, "/timeline")
			level := r.URL.Query().Get("level")
			if level == "" {
				level = "year"
			}
			var start *time.Time
			if raw := r.URL.Query().Get("start_date"); raw != "" {
				value, err := time.Parse("2006-01-02", raw)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start_date must be YYYY-MM-DD"})
					return
				}
				start = &value
			}
			periods, err := service.List(r.Context(), group, level, start)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid timeline request"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"periods": periods})
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/timeline/complete") {
			group := strings.TrimSuffix(path, "/timeline/complete")
			var body struct {
				StartDate string `json:"start_date"`
				EndDate   string `json:"end_date"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
				return
			}
			start, err := time.Parse("2006-01-02", body.StartDate)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start_date must be YYYY-MM-DD"})
				return
			}
			end, err := time.Parse("2006-01-02", body.EndDate)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "end_date must be YYYY-MM-DD"})
				return
			}
			id, err := service.Complete(r.Context(), group, start, end)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not queue coverage completion"})
				return
			}
			if id == "" {
				writeJSON(w, http.StatusOK, map[string]any{"state": "complete", "job_id": nil})
			} else {
				writeJSON(w, http.StatusAccepted, map[string]any{"state": "queued", "job_id": id})
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WithWatchlist exposes a small persisted list of groups for recurring,
// bounded retention probes. Creating or editing a list entry never contacts NNTP.
func WithWatchlist(next http.Handler, service WatchlistService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/watchlist":
			items, err := service.List(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load watchlist"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"watchlist": items})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/watchlist":
			var body struct {
				Newsgroup     string `json:"newsgroup"`
				IntervalHours int    `json:"interval_hours"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
				return
			}
			item, err := service.Add(r.Context(), body.Newsgroup, body.IntervalHours)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, item)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/watchlist/"):
			group := strings.TrimPrefix(r.URL.Path, "/api/v1/watchlist/")
			if group == "" || strings.Contains(group, "/") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newsgroup is required"})
				return
			}
			if err := service.Remove(r.Context(), strings.ToLower(group)); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "watchlist entry not found"})
				} else {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not remove watchlist entry"})
				}
				return
			}
			writeJSON(w, http.StatusNoContent, nil)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// WithRetention adds explicit, bounded oldest-retained probes. Reading stored
// observations never opens an NNTP connection.
func WithRetention(next http.Handler, service RetentionService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/newsgroups/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		var group string
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/oldest-headers"):
			group = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/oldest-headers")
			limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
			if group == "" || strings.Contains(group, "/") || err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newsgroup and integer limit are required"})
				return
			}
			if err := service.RetrieveNext(r.Context(), strings.ToLower(group), limit); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not retrieve oldest headers"})
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"newsgroup": strings.ToLower(group), "limit": limit})
			return
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/retention-probe"):
			group = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/retention-probe")
			if group == "" || strings.Contains(group, "/") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newsgroup is required"})
				return
			}
			items, err := service.Probe(r.Context(), strings.ToLower(group))
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not probe retained history"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"observations": items})
			return
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/retention"):
			group = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/retention")
			if group == "" || strings.Contains(group, "/") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newsgroup is required"})
				return
			}
			items, err := service.List(r.Context(), strings.ToLower(group))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load retained history"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"observations": items})
			return
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func WithChronologicalBrowser(next http.Handler, lister articles.ChronologicalLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/newsgroups/"
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/headers") {
			next.ServeHTTP(w, r)
			return
		}
		group := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/headers")
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
				return
			}
			limit = value
		}
		var start, end *time.Time
		if raw := r.URL.Query().Get("start_date"); raw != "" {
			value, parseErr := time.Parse("2006-01-02", raw)
			if parseErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid chronological request"})
				return
			}
			start = &value
		}
		if raw := r.URL.Query().Get("end_date"); raw != "" {
			value, parseErr := time.Parse("2006-01-02", raw)
			if parseErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid chronological request"})
				return
			}
			value = value.AddDate(0, 0, 1)
			end = &value
		}
		var page articles.SearchPage
		var err error
		if periodLister, ok := lister.(articles.PeriodChronologicalLister); ok {
			page, err = periodLister.ChronologicalPeriod(r.Context(), group, start, end, r.URL.Query().Get("cursor"), limit)
		} else {
			if start != nil || end != nil {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "period browsing unavailable"})
				return
			}
			page, err = lister.Chronological(r.Context(), group, r.URL.Query().Get("cursor"), limit)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid chronological request"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"articles": page.Articles, "next_cursor": page.NextCursor, "total_records": page.TotalRecords, "page_size": limit})
	})
}

// WithMetrics emits low-cardinality operational metrics only. It intentionally
// has no labels derived from articles, user actions, or credentials.
func WithMetrics(next http.Handler, source MetricSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		snapshot := source()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintf(w, "# TYPE usenet_locator_go_goroutines gauge\nusenet_locator_go_goroutines %d\n# TYPE usenet_locator_heap_bytes gauge\nusenet_locator_heap_bytes %d\n# TYPE usenet_locator_db_acquired_connections gauge\nusenet_locator_db_acquired_connections %d\n# TYPE usenet_locator_db_idle_connections gauge\nusenet_locator_db_idle_connections %d\n# TYPE usenet_locator_db_acquires_total counter\nusenet_locator_db_acquires_total %d\n", runtime.NumGoroutine(), memory.HeapAlloc, snapshot.DBAcquiredConns, snapshot.DBIdleConns, snapshot.DBAcquireCount)
	})
}

func NewHandlerWithReadiness(version string, readiness Readiness) http.Handler {
	return NewHandlerWithDependencies(version, readiness, nil, nil)
}

// NewHandlerWithDependencies adds optional job APIs to the base Stage 1
// router. Nil job dependencies leave those routes unavailable rather than
// pretending a request has been persisted.
func NewHandlerWithDependencies(version string, readiness Readiness, creator jobs.Creator, finder jobs.Finder) http.Handler {
	return NewHandlerWithServices(version, readiness, creator, finder, nil)
}

func NewHandlerWithServices(version string, readiness Readiness, creator jobs.Creator, finder jobs.Finder, preferences articles.PreferenceWriter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(readiness))
	mux.HandleFunc("GET /api/v1/system/status", status(version))
	if creator != nil {
		mux.HandleFunc("POST /api/v1/jobs", createJob(creator))
	}
	if finder != nil {
		mux.HandleFunc("GET /api/v1/jobs/{id}", getJob(finder))
	}
	if transitioner, ok := finder.(jobs.Transitioner); ok {
		mux.HandleFunc("POST /api/v1/jobs/{id}/pause", transitionJob(transitioner, jobs.Paused))
		mux.HandleFunc("POST /api/v1/jobs/{id}/resume", transitionJob(transitioner, jobs.Queued))
		mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", transitionJob(transitioner, jobs.Cancelled))
	}
	if preferences != nil {
		mux.HandleFunc("POST /api/v1/articles/unwanted", setUnwanted(preferences))
	}
	if searcher, ok := preferences.(articles.Searcher); ok {
		mux.HandleFunc("GET /api/v1/search", searchArticles(searcher))
	}
	if detailer, ok := preferences.(articles.Detailer); ok {
		mux.HandleFunc("GET /api/v1/articles/{id}", getArticle(detailer))
	}
	return mux
}

func transitionJob(transitioner jobs.Transitioner, target jobs.State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if err := transitioner.Transition(r.Context(), id, target); err != nil {
			switch {
			case errors.Is(err, jobs.ErrNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
			case errors.Is(err, jobs.ErrInvalidTransition):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "job cannot perform that command"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update job"})
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "state": string(target)})
	}
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func readyz(readiness Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := readiness(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func status(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version": version,
			"status":  "starting",
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type createJobBody struct {
	Newsgroup          string `json:"newsgroup"`
	Endpoint           string `json:"endpoint"`
	StartDate          string `json:"start_date"`
	EndDate            string `json:"end_date"`
	MarginDays         *int   `json:"margin_days"`
	ScanReason         string `json:"scan_reason"`
	SourceJobID        string `json:"source_job_id"`
	TransferLimitBytes *int64 `json:"transfer_limit_bytes"`
}

type unwantedBody struct {
	ArticleIDs []int64 `json:"article_ids"`
	Unwanted   *bool   `json:"unwanted"`
}

func setUnwanted(preferences articles.PreferenceWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body unwantedBody
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF || body.Unwanted == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "article_ids and unwanted are required"})
			return
		}
		if err := preferences.SetUnwanted(r.Context(), body.ArticleIDs, *body.Unwanted); err != nil {
			switch {
			case errors.Is(err, articles.ErrNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			case errors.Is(err, articles.ErrInvalidSelection):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid article selection"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not update article preference"})
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"article_ids": body.ArticleIDs, "unwanted": *body.Unwanted})
	}
}

func searchArticles(searcher articles.Searcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		request := articles.SearchRequest{Subject: query.Get("subject"), Author: query.Get("author"), MessageID: query.Get("message_id"), Newsgroup: query.Get("newsgroup"), Cursor: query.Get("cursor")}
		if raw := query.Get("limit"); raw != "" {
			limit, err := strconv.Atoi(raw)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer"})
				return
			}
			request.Limit = limit
		}
		if raw := query.Get("include_unwanted"); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "include_unwanted must be true or false"})
				return
			}
			request.IncludeUnwanted = value
		}
		var err error
		if raw := query.Get("start_date"); raw != "" {
			value, parseErr := time.Parse("2006-01-02", raw)
			if parseErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start_date must be YYYY-MM-DD"})
				return
			}
			request.Start = &value
		}
		if raw := query.Get("end_date"); raw != "" {
			value, parseErr := time.Parse("2006-01-02", raw)
			if parseErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "end_date must be YYYY-MM-DD"})
				return
			}
			value = value.AddDate(0, 0, 1)
			request.End = &value
		}
		page, err := searcher.Search(r.Context(), request)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid search request"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"articles": page.Articles, "next_cursor": page.NextCursor, "total_records": page.TotalRecords, "page_size": request.Limit})
	}
}

func getArticle(detailer articles.Detailer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			return
		}
		detail, err := detailer.GetDetail(r.Context(), id)
		if errors.Is(err, articles.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not retrieve article"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": detail.ID, "message_id": detail.MessageID, "subject": detail.Subject, "author": detail.Author, "date": detail.Date, "references": detail.References, "bytes": detail.Bytes, "lines": detail.Lines, "newsgroups": detail.Newsgroups, "unwanted": detail.Unwanted, "cached_body": detail.CachedBody})
	}
}

func createJob(creator jobs.Creator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createJobBody
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON object"})
			return
		}
		if body.MarginDays == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "margin_days is required"})
			return
		}
		start, err := time.Parse("2006-01-02", body.StartDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start_date must be YYYY-MM-DD"})
			return
		}
		end, err := time.Parse("2006-01-02", body.EndDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "end_date must be YYYY-MM-DD"})
			return
		}
		request := jobs.CreateRequest{NewsgroupID: strings.ToLower(body.Newsgroup), EndpointID: body.Endpoint, StartDate: start, EndDate: end, MarginDays: *body.MarginDays, ScanReason: body.ScanReason, SourceJobID: body.SourceJobID, TransferLimitBytes: body.TransferLimitBytes}
		if err := request.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		id, err := creator.Create(r.Context(), request)
		if err != nil {
			if errors.Is(err, jobs.ErrUnknownEndpoint) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configured endpoint does not exist"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create job"})
			return
		}
		w.Header().Set("Location", "/api/v1/jobs/"+id)
		writeJSON(w, http.StatusCreated, map[string]string{"id": id, "state": string(jobs.Queued)})
	}
}

func getJob(finder jobs.Finder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
			return
		}
		job, err := finder.Get(r.Context(), id)
		if errors.Is(err, jobs.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not retrieve job"})
			return
		}
		writeJSON(w, http.StatusOK, jobResponse(job))
	}
}

func jobResponse(job jobs.Job) map[string]any {
	return map[string]any{
		"id": job.ID, "newsgroup": job.Newsgroup, "endpoint": job.Endpoint,
		"start_date": job.StartDate.Format("2006-01-02"), "end_date": job.EndDate.Format("2006-01-02"),
		"margin_days": job.MarginDays, "state": job.State,
		"headers_retrieved": job.HeadersRetrieved, "articles_stored": job.ArticlesStored,
		"scan_reason": job.ScanReason, "source_job_id": job.SourceJobID,
		"transfer_limit_bytes": job.TransferLimitBytes, "transfer_used_bytes": job.TransferUsedBytes,
		"provider_jobs": job.ProviderJobs,
		"last_error":    job.LastError, "created_at": job.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": job.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
