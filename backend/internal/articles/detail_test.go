package articles

import (
	"context"
	"errors"
	"testing"
)

func TestGetDetailRejectsNonPositiveIDWithoutDatabase(t *testing.T) {
	_, err := (Store{}).GetDetail(context.Background(), 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}
