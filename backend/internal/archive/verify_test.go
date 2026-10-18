package archive

import (
	"context"
	"errors"
	"testing"
)

func TestVerifierRejectsUnboundedOrUnknownRequests(t *testing.T) {
	for _, request := range []struct {
		category string
		limit    int
	}{{"unknown", 1}, {"job_counters", 0}, {"job_counters", 201}} {
		if _, err := (Verifier{}).Check(context.Background(), request.category, "", request.limit); !errors.Is(err, ErrInvalidVerificationRequest) {
			t.Fatalf("request %#v: %v", request, err)
		}
	}
}
