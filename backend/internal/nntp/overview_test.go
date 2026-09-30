package nntp

import "testing"

func TestParseOverviewAcceptsMalformedDateWithoutDroppingArticle(t *testing.T) {
	overview, err := parseOverview("5\tSubject\tAlice\tnot-a-date\t<id@example>\t\t42\t3")
	if err != nil {
		t.Fatalf("parseOverview() error = %v", err)
	}
	if overview.ArticleNumber != 5 || overview.MessageID != "<id@example>" || !overview.Date.IsZero() {
		t.Fatalf("overview = %#v", overview)
	}
}

func TestParseOverviewRejectsMalformedProtocolLine(t *testing.T) {
	if _, err := parseOverview("not-an-overview"); err == nil {
		t.Fatal("parseOverview() succeeded for malformed line")
	}
}

func TestParseOverviewAcceptsMailDateWithoutWeekday(t *testing.T) {
	overview, err := parseOverview("5\tSubject\tAlice\t11 Jul 2026 22:25:34 -0100\t<id@example>\t\t42\t3")
	if err != nil || overview.Date.IsZero() {
		t.Fatalf("parseOverview() = %#v, %v", overview, err)
	}
}
