package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/indexing"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
	"github.com/jmkgreen/usenet-locator/backend/internal/providers"
	"github.com/jmkgreen/usenet-locator/backend/internal/qualification"
	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
	"github.com/jmkgreen/usenet-locator/backend/internal/watchlist"
)

type testJobs struct {
	created jobs.CreateRequest
	job     jobs.Job
	getErr  error
	target  jobs.State
	moveErr error
}

type testPreferences struct {
	ids      []int64
	unwanted bool
	err      error
}

func (s *testPreferences) SetUnwanted(_ context.Context, ids []int64, unwanted bool) error {
	s.ids, s.unwanted = ids, unwanted
	return s.err
}

type testSearch struct {
	request articles.SearchRequest
	page    articles.SearchPage
	err     error
}

type testBodyFetcher struct {
	body string
	err  error
}

type testQualificationHistory struct {
	items []qualification.History
	err   error
}

type apiPreflightClient struct{}

func (apiPreflightClient) Authenticate(context.Context, string, string) error { return nil }
func (apiPreflightClient) Capabilities(context.Context) ([]string, error) {
	return []string{"reader", "OVER", "provider secret diagnostic"}, nil
}
func (apiPreflightClient) ModeReader(context.Context) error { return nil }
func (apiPreflightClient) Group(context.Context, string) (nntp.Group, error) {
	return nntp.Group{Low: 1, High: 1}, nil
}
func (apiPreflightClient) Overview(_ context.Context, _ int64, _ int64, emit func(nntp.Overview) error) error {
	return emit(nntp.Overview{ArticleNumber: 1, MessageID: "<safe@test>", Date: time.Now()})
}
func (apiPreflightClient) Body(context.Context, int64, int64, io.Writer) error { return nil }
func (apiPreflightClient) Close() error                                        { return nil }
func (apiPreflightClient) OverviewFormat(context.Context) (nntp.OverviewFormat, error) {
	return nntp.OverviewFormat{Code: 215, Fields: []string{"subject"}}, nil
}
func (apiPreflightClient) Stat(context.Context, string) (int, error) { return 223, nil }
func (apiPreflightClient) TransferBytes() int64                      { return 1 }

type quotaFailure struct{ err error }

func (q quotaFailure) Consume(context.Context, string, int64) error { return q.err }

type testRetention struct {
	items          []retention.Observation
	err            error
	probed         string
	retrievedGroup string
	retrievedLimit int
}

type testWatchlist struct {
	item    watchlist.Item
	group   string
	removed string
	err     error
}

func (s *testWatchlist) List(context.Context) ([]watchlist.Item, error) {
	return []watchlist.Item{s.item}, s.err
}
func (s *testWatchlist) Add(_ context.Context, group string, interval int) (watchlist.Item, error) {
	s.group = group
	if s.err != nil {
		return watchlist.Item{}, s.err
	}
	return watchlist.Item{Newsgroup: strings.ToLower(group), IntervalHours: interval}, nil
}
func (s *testWatchlist) Remove(_ context.Context, group string) error {
	s.removed = group
	return s.err
}

type testChronological struct {
	group, cursor string
	limit         int
	err           error
}

func (s *testChronological) Chronological(_ context.Context, group, cursor string, limit int) (articles.SearchPage, error) {
	s.group, s.cursor, s.limit = group, cursor, limit
	return articles.SearchPage{Articles: []articles.SearchResult{{ID: 1, MessageID: "<safe@test>"}}}, s.err
}

func (s *testRetention) Probe(_ context.Context, group string) ([]retention.Observation, error) {
	s.probed = group
	return s.items, s.err
}
func (s *testRetention) List(context.Context, string) ([]retention.Observation, error) {
	return s.items, s.err
}
func (s *testRetention) RetrieveNext(_ context.Context, group string, limit int) error {
	s.retrievedGroup, s.retrievedLimit = group, limit
	return s.err
}

func (s testQualificationHistory) Record(context.Context, string, qualification.Result) error {
	return nil
}
func (s testQualificationHistory) List(context.Context, string) ([]qualification.History, error) {
	return s.items, s.err
}

func (f testBodyFetcher) GetOrFetch(context.Context, int64) (string, error) { return f.body, f.err }
func (f testBodyFetcher) GetCached(context.Context, int64) (string, error)  { return f.body, f.err }

type testStorage struct{}

func (testStorage) ListGroups(context.Context) ([]articles.GroupCount, error) {
	return []articles.GroupCount{{Name: "comp.lang.go", Articles: 2}}, nil
}
func (testStorage) ListCoverage(context.Context, string) ([]indexing.Coverage, error) {
	return []indexing.Coverage{{Endpoint: "primary", Newsgroup: "comp.lang.go", State: "complete", ArticleNumberStart: 1, ArticleNumberEnd: 2}}, nil
}

type failingStorage struct{}

func (failingStorage) ListGroups(context.Context) ([]articles.GroupCount, error) {
	return nil, errors.New("storage unavailable")
}
func (failingStorage) ListCoverage(context.Context, string) ([]indexing.Coverage, error) {
	return nil, errors.New("storage unavailable")
}

func (s *testSearch) SetUnwanted(context.Context, []int64, bool) error { return nil }
func (s *testSearch) Search(_ context.Context, request articles.SearchRequest) (articles.SearchPage, error) {
	s.request = request
	return s.page, s.err
}
func (s *testSearch) GetDetail(_ context.Context, id int64) (articles.Detail, error) {
	if id == 4 {
		return articles.Detail{ID: 4, MessageID: "<a@test>", Newsgroups: []string{"comp.lang.go"}}, nil
	}
	return articles.Detail{}, articles.ErrNotFound
}

func (s *testJobs) Create(_ context.Context, request jobs.CreateRequest) (string, error) {
	s.created = request
	return "9a84900b-5d31-4acb-b917-b73b5a7c9e32", nil
}

func (s *testJobs) Get(_ context.Context, _ string) (jobs.Job, error) { return s.job, s.getErr }
func (s *testJobs) Transition(_ context.Context, _ string, target jobs.State) error {
	s.target = target
	return s.moveErr
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()
	NewHandler("test").ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if body := res.Body.String(); !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	res := httptest.NewRecorder()
	NewHandler("test-version").ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if body := res.Body.String(); !strings.Contains(body, `"version":"test-version"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestReadyz(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	res := httptest.NewRecorder()
	NewHandler("test").ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if body := res.Body.String(); !strings.Contains(body, `"status":"ready"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestReadyzReportsDatabaseFailure(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	res := httptest.NewRecorder()
	NewHandlerWithReadiness("test", func(context.Context) error { return errors.New("database unavailable") }).ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusServiceUnavailable)
	}
}

func TestUnknownRouteIsNotFound(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	res := httptest.NewRecorder()
	NewHandler("test").ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNotFound)
	}
}

func TestStaticFilesServeSPAWithoutShadowingAPI(t *testing.T) {
	t.Parallel()
	handler := WithStaticFiles(NewHandler("test"), fstest.MapFS{"index.html": {Data: []byte("<main>app</main>")}, "assets/app.js": {Data: []byte("asset")}})
	for _, path := range []string{"/", "/jobs/new"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "app") {
			t.Fatalf("%s = %d %s", path, res.Code, res.Body.String())
		}
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("API status = %d", res.Code)
	}
}

func TestCreateJob(t *testing.T) {
	t.Parallel()
	store := &testJobs{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"newsgroup":"Comp.Lang.Go","endpoint":"primary","start_date":"2020-01-01","end_date":"2020-01-02","margin_days":2}`))
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Location"); got != "/api/v1/jobs/9a84900b-5d31-4acb-b917-b73b5a7c9e32" {
		t.Fatalf("Location = %q", got)
	}
	if store.created.NewsgroupID != "comp.lang.go" || store.created.StartDate.Location() != time.UTC {
		t.Fatalf("created request = %#v", store.created)
	}
}

func TestCreateJobRejectsUnknownField(t *testing.T) {
	t.Parallel()
	store := &testJobs{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"newsgroup":"comp.lang.go","endpoint":"primary","start_date":"2020-01-01","end_date":"2020-01-02","unexpected":true}`))
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestCreateJobRequiresMarginDays(t *testing.T) {
	t.Parallel()
	store := &testJobs{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"newsgroup":"comp.lang.go","endpoint":"primary","start_date":"2020-01-01","end_date":"2020-01-02"}`))
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestGetJob(t *testing.T) {
	t.Parallel()
	store := &testJobs{job: jobs.Job{
		ID: "9a84900b-5d31-4acb-b917-b73b5a7c9e32", Newsgroup: "comp.lang.go", Endpoint: "primary",
		StartDate: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
		MarginDays: 2, State: jobs.Running, HeadersRetrieved: 4, ArticlesStored: 3,
		ProviderJobs: []jobs.ProviderJob{{Endpoint: "primary", State: jobs.Running, HeadersRetrieved: 4, ArticlesStored: 3}},
		CreatedAt:    time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC), UpdatedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/9a84900b-5d31-4acb-b917-b73b5a7c9e32", nil)
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)

	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"state":"running"`) || !strings.Contains(res.Body.String(), `"start_date":"2020-01-01"`) || !strings.Contains(res.Body.String(), `"provider_jobs":[{"endpoint":"primary"`) {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestGetJobNotFound(t *testing.T) {
	t.Parallel()
	store := &testJobs{getErr: jobs.ErrNotFound}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/absent", nil)
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestPauseJob(t *testing.T) {
	t.Parallel()
	store := &testJobs{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-1/pause", nil)
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)
	if res.Code != http.StatusOK || store.target != jobs.Paused || !strings.Contains(res.Body.String(), `"state":"paused"`) {
		t.Fatalf("status = %d, target = %s, body = %s", res.Code, store.target, res.Body.String())
	}
}

func TestResumeJobRejectsInvalidState(t *testing.T) {
	t.Parallel()
	store := &testJobs{moveErr: jobs.ErrInvalidTransition}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-1/resume", nil)
	res := httptest.NewRecorder()
	NewHandlerWithDependencies("test", func(context.Context) error { return nil }, store, store).ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestBatchMarkUnwanted(t *testing.T) {
	t.Parallel()
	prefs := &testPreferences{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/articles/unwanted", strings.NewReader(`{"article_ids":[11,12],"unwanted":true}`))
	res := httptest.NewRecorder()
	NewHandlerWithServices("test", func(context.Context) error { return nil }, nil, nil, prefs).ServeHTTP(res, req)
	if res.Code != http.StatusOK || len(prefs.ids) != 2 || !prefs.unwanted {
		t.Fatalf("status = %d, prefs = %#v", res.Code, prefs)
	}
}

func TestBatchMarkUnwantedRejectsUnknownArticle(t *testing.T) {
	t.Parallel()
	prefs := &testPreferences{err: articles.ErrNotFound}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/articles/unwanted", strings.NewReader(`{"article_ids":[11],"unwanted":true}`))
	res := httptest.NewRecorder()
	NewHandlerWithServices("test", func(context.Context) error { return nil }, nil, nil, prefs).ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestSearchParsesInclusiveDates(t *testing.T) {
	t.Parallel()
	search := &testSearch{page: articles.SearchPage{Articles: []articles.SearchResult{{ID: 4, MessageID: "<a@test>"}}}}
	handler := NewHandlerWithServices("test", func(context.Context) error { return nil }, nil, nil, search)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/search?newsgroup=comp.lang.go&start_date=2020-01-01&end_date=2020-01-02&include_unwanted=true", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"message_id":`) || search.request.Start == nil || search.request.End == nil || search.request.End.Format("2006-01-02") != "2020-01-03" || !search.request.IncludeUnwanted {
		t.Fatalf("status = %d, request = %#v", res.Code, search.request)
	}
}

func TestGetArticleDetail(t *testing.T) {
	t.Parallel()
	handler := NewHandlerWithServices("test", func(context.Context) error { return nil }, nil, nil, &testSearch{})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/articles/4", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"id":4`) {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestBodyRetrievalRejectsUnwanted(t *testing.T) {
	t.Parallel()
	handler := WithBodyRetrieval(NewHandler("test"), testBodyFetcher{err: articles.ErrUnwanted})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/articles/4/body", nil))
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestBodyRetrievalReportsQuotaExhaustion(t *testing.T) {
	t.Parallel()
	handler := WithBodyRetrieval(NewHandler("test"), testBodyFetcher{err: accounts.ErrQuotaExceeded})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/articles/4/body", nil))
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestCachedBodyDownloadIsAnAttachmentWithoutNNTPFetch(t *testing.T) {
	t.Parallel()
	handler := WithBodyRetrieval(NewHandler("test"), testBodyFetcher{body: "cached text"})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/articles/4/body/download", nil))
	if res.Code != http.StatusOK || res.Body.String() != "cached text" || res.Header().Get("Content-Disposition") != "attachment; filename=article-4.txt" || res.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status = %d, headers = %#v, body = %q", res.Code, res.Header(), res.Body.String())
	}
}

func TestCachedBodyDownloadDoesNotFetchMissingText(t *testing.T) {
	t.Parallel()
	handler := WithBodyRetrieval(NewHandler("test"), testBodyFetcher{err: articles.ErrBodyNotCached})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/articles/4/body/download", nil))
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "has not been retrieved") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestProviderStatusDoesNotNeedSecrets(t *testing.T) {
	t.Parallel()
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example", Port: 563, TLS: true, Primary: true}}}
	handler := WithProviderStatus(NewHandler("test"), providers.New(cfg, nil, accounts.NewGuard(cfg)))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"host":"news.example"`) || strings.Contains(res.Body.String(), "password") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestProviderQualificationHistoryReturnsSafeRecords(t *testing.T) {
	t.Parallel()
	service := qualification.Service{History: testQualificationHistory{items: []qualification.History{{Endpoint: "primary", Result: qualification.Result{OverviewCode: 224, OverviewDates: 1}}}}}
	handler := WithProviderPreflight(NewHandler("test"), service)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/providers/primary/qualifications", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"overview_code":224`) || strings.Contains(res.Body.String(), "password") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestProviderPreflightRouteRunsBoundedCheckAndFailsClosed(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(username, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("not-for-response\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example", Port: 563, TLS: true}}}
	service := qualification.New(cfg, accounts.NewGuard(cfg), nil)
	service.History = testQualificationHistory{}
	service.Dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return apiPreflightClient{}, nil }
	handler := WithProviderPreflight(NewHandler("test"), service)

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/v1/providers/primary/preflight", strings.NewReader(`{"unexpected":true}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid request = %d %s", invalid.Code, invalid.Body.String())
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/providers/primary/preflight", strings.NewReader(`{"message_id":"<probe@test>","newsgroup":"alt.test","article_number":1}`)))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"overview_code":224`) || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "not-for-response") {
		t.Fatalf("preflight = %d %s", response.Code, response.Body.String())
	}

	quotaService := service
	quotaService.Quota = quotaFailure{err: accounts.ErrQuotaExceeded}
	quotaService.History = nil
	quota := httptest.NewRecorder()
	WithProviderPreflight(NewHandler("test"), quotaService).ServeHTTP(quota, httptest.NewRequest(http.MethodPost, "/api/v1/providers/primary/preflight", strings.NewReader(`{}`)))
	if quota.Code != http.StatusTooManyRequests || strings.Contains(quota.Body.String(), "operator") {
		t.Fatalf("quota response = %d %s", quota.Code, quota.Body.String())
	}

	missingHistory := httptest.NewRecorder()
	WithProviderPreflight(NewHandler("test"), qualification.Service{}).ServeHTTP(missingHistory, httptest.NewRequest(http.MethodGet, "/api/v1/providers/primary/qualifications", nil))
	if missingHistory.Code != http.StatusNotFound {
		t.Fatalf("missing history = %d %s", missingHistory.Code, missingHistory.Body.String())
	}
}

func TestStorageBrowserKeepsCoverageEndpointLocal(t *testing.T) {
	t.Parallel()
	handler := WithStorageBrowser(NewHandler("test"), testStorage{}, testStorage{})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/coverage?newsgroup=comp.lang.go", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"endpoint":"primary"`) {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestStorageBrowserFailsClosedOnStorageError(t *testing.T) {
	handler := WithStorageBrowser(NewHandler("test"), failingStorage{}, failingStorage{})
	for _, path := range []string{"/api/v1/newsgroups", "/api/v1/coverage?newsgroup=alt.test"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "storage unavailable") {
			t.Fatalf("%s = %d %s", path, res.Code, res.Body.String())
		}
	}
}

func TestRetentionProbeAndStoredHistoryRoutes(t *testing.T) {
	t.Parallel()
	articleID := int64(4)
	service := &testRetention{items: []retention.Observation{{Endpoint: "primary", Newsgroup: "alt.test", ArticleID: &articleID, Outcome: "found"}}}
	handler := WithRetention(NewHandler("test"), service)
	probe := httptest.NewRecorder()
	handler.ServeHTTP(probe, httptest.NewRequest(http.MethodPost, "/api/v1/newsgroups/alt.test/retention-probe", nil))
	if probe.Code != http.StatusOK || service.probed != "alt.test" || !strings.Contains(probe.Body.String(), `"article_id":4`) {
		t.Fatalf("probe = %d %s", probe.Code, probe.Body.String())
	}
	stored := httptest.NewRecorder()
	handler.ServeHTTP(stored, httptest.NewRequest(http.MethodGet, "/api/v1/newsgroups/alt.test/retention", nil))
	if stored.Code != http.StatusOK || !strings.Contains(stored.Body.String(), `"outcome":"found"`) {
		t.Fatalf("stored = %d %s", stored.Code, stored.Body.String())
	}
}

func TestRetentionHeaderRetrievalIsBoundedAndFailsClosed(t *testing.T) {
	service := &testRetention{}
	handler := WithRetention(NewHandler("test"), service)
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/v1/newsgroups/alt.test/oldest-headers?limit=bad", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d", bad.Code)
	}
	ok := httptest.NewRecorder()
	handler.ServeHTTP(ok, httptest.NewRequest(http.MethodPost, "/api/v1/newsgroups/Alt.Test/oldest-headers?limit=10", nil))
	if ok.Code != http.StatusAccepted || service.retrievedGroup != "alt.test" || service.retrievedLimit != 10 {
		t.Fatalf("response=%d service=%#v", ok.Code, service)
	}
	service.err = errors.New("provider unavailable")
	failure := httptest.NewRecorder()
	handler.ServeHTTP(failure, httptest.NewRequest(http.MethodPost, "/api/v1/newsgroups/alt.test/oldest-headers?limit=10", nil))
	if failure.Code != http.StatusBadGateway || strings.Contains(failure.Body.String(), "provider") {
		t.Fatalf("failure=%d %s", failure.Code, failure.Body.String())
	}
}

func TestWatchlistRoutesRejectMalformedInputAndNormaliseGroup(t *testing.T) {
	t.Parallel()
	service := &testWatchlist{item: watchlist.Item{Newsgroup: "alt.test", IntervalHours: 12}}
	handler := WithWatchlist(NewHandler("test"), service)

	malformed := httptest.NewRecorder()
	handler.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/api/v1/watchlist", strings.NewReader(`{"newsgroup":"alt.test","interval_hours":1,"extra":true}`)))
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d", malformed.Code)
	}

	created := httptest.NewRecorder()
	handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/v1/watchlist", strings.NewReader(`{"newsgroup":"Alt.Test","interval_hours":12}`)))
	if created.Code != http.StatusCreated || service.group != "Alt.Test" || !strings.Contains(created.Body.String(), `"newsgroup":"alt.test"`) {
		t.Fatalf("created = %d %s service=%#v", created.Code, created.Body.String(), service)
	}

	invalidDelete := httptest.NewRecorder()
	handler.ServeHTTP(invalidDelete, httptest.NewRequest(http.MethodDelete, "/api/v1/watchlist/alt/test", nil))
	if invalidDelete.Code != http.StatusBadRequest {
		t.Fatalf("invalid delete = %d", invalidDelete.Code)
	}

	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, httptest.NewRequest(http.MethodDelete, "/api/v1/watchlist/ALT.TEST", nil))
	if removed.Code != http.StatusNoContent || service.removed != "alt.test" {
		t.Fatalf("removed = %d service=%#v", removed.Code, service)
	}
}

func TestWatchlistRoutesFailSafely(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		err                error
		want               int
	}{
		{"list", http.MethodGet, "/api/v1/watchlist", errors.New("database failed"), http.StatusInternalServerError},
		{"missing delete", http.MethodDelete, "/api/v1/watchlist/alt.test", pgx.ErrNoRows, http.StatusNotFound},
		{"failed delete", http.MethodDelete, "/api/v1/watchlist/alt.test", errors.New("database failed"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			WithWatchlist(NewHandler("test"), &testWatchlist{err: tc.err}).ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
			if res.Code != tc.want || strings.Contains(res.Body.String(), "database failed") {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

func TestChronologicalRouteRejectsInvalidLimitAndNeverTouchesNNTP(t *testing.T) {
	t.Parallel()
	lister := &testChronological{}
	handler := WithChronologicalBrowser(NewHandler("test"), lister)
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/v1/newsgroups/alt.test/headers?limit=not-a-number", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad request status = %d", bad.Code)
	}
	good := httptest.NewRecorder()
	handler.ServeHTTP(good, httptest.NewRequest(http.MethodGet, "/api/v1/newsgroups/alt.test/headers?limit=2&cursor=opaque", nil))
	if good.Code != http.StatusOK || lister.group != "alt.test" || lister.cursor != "opaque" || lister.limit != 2 || !strings.Contains(good.Body.String(), `"message_id"`) {
		t.Fatalf("response = %d %s lister=%#v", good.Code, good.Body.String(), lister)
	}
}

func TestMetricsContainOnlyFixedOperationalNames(t *testing.T) {
	t.Parallel()
	handler := WithMetrics(NewHandler("test"), func() MetricSnapshot { return MetricSnapshot{DBAcquiredConns: 2, DBIdleConns: 1, DBAcquireCount: 3} })
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "usenet_locator_db_acquired_connections 2") || strings.Contains(res.Body.String(), "comp.lang") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}
