package articles

import (
	"context"
	"testing"
)

func TestSaveBodyRejectsEmptyBodyBeforeDatabase(t *testing.T) {
	if err := (Store{}).SaveBody(context.Background(), 1, "endpoint", ""); err == nil {
		t.Fatal("SaveBody accepted empty body")
	}
}
