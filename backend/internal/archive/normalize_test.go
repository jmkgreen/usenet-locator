package archive

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

func TestNormalizeOverviewPreservesSourceAndReportsMalformedData(t *testing.T) {
	input := nntp.Overview{Subject: "=?ISO-8859-1?Q?Re=3A_caf=E9?=", Author: "invalid\xff\x00", RawDate: "bad date", MessageID: "<a@example>", References: "bad reference", Bytes: -1}
	first := NormalizeOverview(input)
	if !reflect.DeepEqual(first, NormalizeOverview(input)) {
		t.Fatal("normalization is not deterministic")
	}
	if first.Overview.Subject != "Re: café" || first.SubjectKey != "café" || first.Overview.Bytes != 0 {
		t.Fatalf("normalized = %#v", first)
	}
	for _, flag := range []string{"invalid_date", "malformed_header", "threading_uncertain"} {
		if !strings.Contains(strings.Join(first.Flags, ","), flag) {
			t.Fatalf("missing %s: %v", flag, first.Flags)
		}
	}
	var source map[string][]byte
	if err := json.Unmarshal(first.Source, &source); err != nil || string(source["author"]) != input.Author {
		t.Fatalf("source = %#v, error = %v", source, err)
	}
}

func TestUnknownHeaderEncodingIsRetained(t *testing.T) {
	item := NormalizeOverview(nntp.Overview{Subject: "=?made-up?Q?hello?="})
	if item.Overview.Subject != "=?made-up?Q?hello?=" || !reflect.DeepEqual(item.Flags, []string{"unknown_encoding"}) {
		t.Fatalf("result = %#v", item)
	}
}
