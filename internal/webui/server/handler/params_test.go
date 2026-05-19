package handler

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseStringBoolIntAndArrayParams(t *testing.T) {
	q := url.Values{
		"name":   {"loom"},
		"active": {"true"},
		"limit":  {"42"},
		"labels": {" api, ui ,, test "},
	}

	if got := ParseStringParam(q, "name"); got != "loom" {
		t.Fatalf("ParseStringParam = %q, want loom", got)
	}

	active, err := ParseBoolParam(q, "active")
	if err != nil || !active {
		t.Fatalf("ParseBoolParam = %v, %v; want true, nil", active, err)
	}
	missingBool, err := ParseBoolParam(q, "missing")
	if err != nil || missingBool {
		t.Fatalf("missing bool = %v, %v; want false, nil", missingBool, err)
	}
	if _, err := ParseBoolParam(url.Values{"active": {"maybe"}}, "active"); err == nil || !strings.Contains(err.Error(), "invalid active value") {
		t.Fatalf("invalid bool err = %v", err)
	}

	limit, err := ParseIntParam(q, "limit")
	if err != nil || limit == nil || *limit != 42 {
		t.Fatalf("ParseIntParam = %v, %v; want 42, nil", limit, err)
	}
	missingInt, err := ParseIntParam(q, "missing")
	if err != nil || missingInt != nil {
		t.Fatalf("missing int = %v, %v; want nil, nil", missingInt, err)
	}
	if _, err := ParseIntParam(url.Values{"limit": {"abc"}}, "limit"); err == nil || !strings.Contains(err.Error(), "invalid limit value") {
		t.Fatalf("invalid int err = %v", err)
	}

	gotLabels := ParseArrayParam(q, "labels")
	wantLabels := []string{"api", "ui", "test"}
	if len(gotLabels) != len(wantLabels) {
		t.Fatalf("ParseArrayParam len = %d, want %d: %#v", len(gotLabels), len(wantLabels), gotLabels)
	}
	for i := range wantLabels {
		if gotLabels[i] != wantLabels[i] {
			t.Fatalf("ParseArrayParam[%d] = %q, want %q", i, gotLabels[i], wantLabels[i])
		}
	}
	if got := ParseArrayParam(q, "missing"); got != nil {
		t.Fatalf("missing array = %#v, want nil", got)
	}
}

func TestParseDateParams(t *testing.T) {
	q := url.Values{
		"from": {"2026-05-19"},
		"to":   {"2026-05-19T12:34:56Z"},
	}
	got, err := ParseDateParams(q, []string{"from", "to", "missing"})
	if err != nil {
		t.Fatalf("ParseDateParams: %v", err)
	}
	if got["from"] != "2026-05-19" || got["to"] != "2026-05-19T12:34:56Z" {
		t.Fatalf("dates = %#v", got)
	}
	if _, ok := got["missing"]; ok {
		t.Fatalf("missing date was included: %#v", got)
	}

	_, err = ParseDateParams(url.Values{"from": {"19 May 2026"}}, []string{"from"})
	if err == nil || !strings.Contains(err.Error(), "invalid from") {
		t.Fatalf("invalid date err = %v", err)
	}
}

func TestSplitAndTrim(t *testing.T) {
	if got := SplitAndTrim(""); got != nil {
		t.Fatalf("empty split = %#v, want nil", got)
	}
	got := SplitAndTrim(" one, two ,, three ")
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("SplitAndTrim len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SplitAndTrim[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
