package indexing

import "testing"

func TestCoverageRetainsEndpointLocalBounds(t *testing.T) {
	item := Coverage{Endpoint: "primary", Newsgroup: "example", ArticleNumberStart: 1, ArticleNumberEnd: 2}
	if item.Endpoint != "primary" || item.ArticleNumberEnd != 2 {
		t.Fatal("coverage fields not retained")
	}
}
