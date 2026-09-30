package articles

import (
	"context"
	"errors"
	"testing"
)

func TestSetUnwantedRejectsEmptyAndOversizedBatchBeforeDatabase(t *testing.T) {
	store := Store{}
	if err := store.SetUnwanted(context.Background(), nil, true); err == nil {
		t.Fatal("accepted empty IDs")
	}
	if err := store.SetUnwanted(context.Background(), make([]int64, 1001), true); err == nil {
		t.Fatal("accepted oversized IDs")
	}
}

func TestSetUnwantedRejectsDuplicateOrNonPositiveID(t *testing.T) {
	store := Store{}
	if err := store.SetUnwanted(context.Background(), []int64{2, 2}, true); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := store.SetUnwanted(context.Background(), []int64{0}, true); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("zero ID error = %v", err)
	}
}
