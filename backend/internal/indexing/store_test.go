package indexing

import (
	"testing"

	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

func TestBatchValidation(t *testing.T) {
	valid := Batch{JobID: "job", RangeStart: 10, RangeEnd: 12, NextArticle: 13, HeadersRetrieved: 1, Overviews: []nntp.Overview{{ArticleNumber: 11}}}
	if err := valid.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	invalid := valid
	invalid.NextArticle = 12
	if err := invalid.validate(); err == nil {
		t.Fatal("validate() accepted a non-advancing checkpoint")
	}
	invalid = valid
	invalid.Overviews = []nntp.Overview{{ArticleNumber: 13}}
	if err := invalid.validate(); err == nil {
		t.Fatal("validate() accepted overview beyond batch")
	}
}

func TestValidMessageID(t *testing.T) {
	if !validMessageID("<one@example.test>") {
		t.Fatal("validMessageID rejected normal ID")
	}
	if validMessageID("not-an-id") || validMessageID("<two example@test>") {
		t.Fatal("validMessageID accepted malformed ID")
	}
}
