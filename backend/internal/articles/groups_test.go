package articles

import "testing"

func TestGroupCountJSONShape(t *testing.T) {
	if (GroupCount{Name: "example", Articles: 2}).Articles != 2 {
		t.Fatal("group count lost article total")
	}
}
