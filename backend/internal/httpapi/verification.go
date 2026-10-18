package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jmkgreen/usenet-locator/backend/internal/archive"
)

type ArchiveVerifier interface {
	Check(context.Context, string, string, int) (archive.Report, error)
}

type ArchiveReprocessor interface {
	Reprocess(context.Context, int64) error
}

type QuarantinedReader interface {
	Get(context.Context, int64) (archive.QuarantinedOverview, error)
}

func WithQuarantinedOverviews(next http.Handler, reader QuarantinedReader) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/archive/quarantined/"
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, prefix), 10, 64)
		if err != nil || id < 1 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "overview not found"})
			return
		}
		item, err := reader.Get(r.Context(), id)
		if errors.Is(err, archive.ErrArticleNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "overview not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load overview"})
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
}

func WithArchiveReprocessing(next http.Handler, reprocessor ArchiveReprocessor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/articles/"
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/reprocess") {
			next.ServeHTTP(w, r)
			return
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/reprocess"), 10, 64)
		if err != nil || id < 1 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			return
		}
		err = reprocessor.Reprocess(r.Context(), id)
		if errors.Is(err, archive.ErrArticleNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "article not found"})
			return
		}
		if errors.Is(err, archive.ErrSourceUnavailable) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "original overview was not recorded for this article"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not reprocess article"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func WithArchiveVerification(next http.Handler, verifier ArchiveVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/archive/verification" || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		limit := 50
		if text := r.URL.Query().Get("limit"); text != "" {
			value, err := strconv.Atoi(text)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
				return
			}
			limit = value
		}
		report, err := verifier.Check(r.Context(), r.URL.Query().Get("category"), r.URL.Query().Get("after"), limit)
		if errors.Is(err, archive.ErrInvalidVerificationRequest) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid verification request"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification could not be completed"})
			return
		}
		writeJSON(w, http.StatusOK, report)
	})
}
