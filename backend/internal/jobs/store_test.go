package jobs

import (
	"regexp"
	"testing"
)

func TestNewIDProducesUUIDv4(t *testing.T) {
	id, err := newID()
	if err != nil {
		t.Fatalf("newID() error = %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("newID() = %q", id)
	}
}
