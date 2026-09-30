package accounts

import (
	"context"
	"testing"
)

func TestQuotaStoreRejectsNegativeTransferWithoutDatabase(t *testing.T) {
	store := QuotaStore{}
	if err := store.Consume(context.Background(), "account", -1); err == nil {
		t.Fatal("negative transfer was accepted")
	}
	if err := store.Consume(context.Background(), "account", 0); err != nil {
		t.Fatalf("zero transfer = %v", err)
	}
}
