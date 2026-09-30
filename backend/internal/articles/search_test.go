package articles

import (
	"testing"
	"time"
)

func TestSearchRequestValidation(t *testing.T) {
	if err := (SearchRequest{Limit: 101}).validate(); err == nil {
		t.Fatal("accepted limit above 100")
	}
	start := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, -1)
	if err := (SearchRequest{Start: &start, End: &end}).validate(); err == nil {
		t.Fatal("accepted reversed date range")
	}
}

func TestSearchCursorRoundTrip(t *testing.T) {
	cursor := encodeCursor(12345)
	id, err := decodeCursor(cursor)
	if err != nil || id != 12345 {
		t.Fatalf("cursor = %q, id = %d, err = %v", cursor, id, err)
	}
	if _, err := decodeCursor("bad cursor"); err == nil {
		t.Fatal("accepted malformed cursor")
	}
}
