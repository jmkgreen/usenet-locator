package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jmkgreen/usenet-locator/backend/internal/archive"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
)

type verifierFake struct {
	err             error
	category, after string
	limit           int
}

func (v *verifierFake) Check(_ context.Context, category, after string, limit int) (archive.Report, error) {
	v.category, v.after, v.limit = category, after, limit
	return archive.Report{Category: category, Findings: []archive.Finding{{Key: "1", Description: "review"}}}, v.err
}

type reprocessorFake struct {
	err error
	id  int64
}

type quarantineFake struct {
	err error
	id  int64
}

func (q *quarantineFake) Get(_ context.Context, id int64) (archive.QuarantinedOverview, error) {
	q.id = id
	return archive.QuarantinedOverview{ID: id, Endpoint: "provider"}, q.err
}

func TestQuarantinedOverviewHTTP(t *testing.T) {
	q := &quarantineFake{}
	h := WithQuarantinedOverviews(http.NotFoundHandler(), q)
	for _, tc := range []struct {
		path   string
		err    error
		status int
	}{
		{"/api/v1/archive/quarantined/3", nil, 200}, {"/api/v1/archive/quarantined/bad", nil, 404},
		{"/api/v1/archive/quarantined/3", archive.ErrArticleNotFound, 404}, {"/api/v1/archive/quarantined/3", errors.New("private"), 500}, {"/other", nil, 404},
	} {
		q.err = tc.err
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest("GET", tc.path, nil))
		if res.Code != tc.status || strings.Contains(res.Body.String(), "private") {
			t.Fatalf("%s: status %d body %s", tc.path, res.Code, res.Body.String())
		}
		if tc.status == 200 && q.id != 3 {
			t.Fatalf("id=%d", q.id)
		}
	}
}

func (r *reprocessorFake) Reprocess(_ context.Context, id int64) error { r.id = id; return r.err }

func TestArchiveVerificationHTTP(t *testing.T) {
	v := &verifierFake{}
	h := WithArchiveVerification(http.NotFoundHandler(), v)
	for _, tc := range []struct {
		url    string
		err    error
		status int
	}{
		{"/api/v1/archive/verification?category=job_counters&after=abc&limit=12", nil, 200},
		{"/api/v1/archive/verification?limit=bad", nil, 400},
		{"/api/v1/archive/verification?category=bad", archive.ErrInvalidVerificationRequest, 400},
		{"/api/v1/archive/verification?category=job_counters", errors.New("database secret"), 500},
		{"/other", nil, 404},
	} {
		v.err = tc.err
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest("GET", tc.url, nil))
		if res.Code != tc.status || strings.Contains(res.Body.String(), "database secret") {
			t.Fatalf("%s: status %d body %q", tc.url, res.Code, res.Body.String())
		}
		if tc.status == 200 && (v.category != "job_counters" || v.after != "abc" || v.limit != 12) {
			t.Fatalf("request not forwarded: %#v", v)
		}
	}
}

func TestArchiveReprocessHTTP(t *testing.T) {
	r := &reprocessorFake{}
	h := WithArchiveReprocessing(http.NotFoundHandler(), r)
	for _, tc := range []struct {
		url    string
		err    error
		status int
	}{
		{"/api/v1/articles/42/reprocess", nil, 204}, {"/api/v1/articles/not-an-id/reprocess", nil, 404},
		{"/api/v1/articles/42/reprocess", archive.ErrArticleNotFound, 404}, {"/api/v1/articles/42/reprocess", archive.ErrSourceUnavailable, 409},
		{"/api/v1/articles/42/reprocess", errors.New("secret"), 500}, {"/other", nil, 404},
	} {
		r.err = tc.err
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest("POST", tc.url, nil))
		if res.Code != tc.status || strings.Contains(res.Body.String(), "secret") {
			t.Fatalf("%s status=%d body=%q", tc.url, res.Code, res.Body.String())
		}
		if tc.status == 204 && r.id != 42 {
			t.Fatalf("id=%d", r.id)
		}
	}
}

type cachedReaderFake struct {
	testBodyFetcher
	reader    archive.Reader
	errReader error
}

func (f cachedReaderFake) GetCachedReader(context.Context, int64) (archive.Reader, error) {
	return f.reader, f.errReader
}

func TestCachedReaderHTTPDoesNotFetchAndSanitizesDownloads(t *testing.T) {
	fetcher := cachedReaderFake{reader: archive.Reader{Text: "hello", HTML: `<p onclick="evil()">safe<script>evil()</script></p>`}}
	h := WithBodyRetrieval(http.NotFoundHandler(), fetcher)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", "/api/v1/articles/42/reader/html/download", nil))
	if res.Code != 200 || res.Body.String() != "<p>safe</p>" || !strings.Contains(res.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("download %d %q %#v", res.Code, res.Body.String(), res.Header())
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", "/api/v1/articles/42/reader", nil))
	if res.Code != 200 || !strings.Contains(res.Body.String(), "hello") || strings.Contains(res.Body.String(), "script") || strings.Contains(res.Body.String(), "onclick") {
		t.Fatalf("reader %d %q", res.Code, res.Body.String())
	}
	fetcher.errReader = articles.ErrBodyNotCached
	h = WithBodyRetrieval(http.NotFoundHandler(), fetcher)
	for _, path := range []string{"/api/v1/articles/42/reader", "/api/v1/articles/42/reader/html/download"} {
		res = httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest("GET", path, nil))
		if res.Code != 409 {
			t.Fatalf("%s status=%d", path, res.Code)
		}
	}
}
