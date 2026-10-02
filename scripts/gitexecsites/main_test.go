package main

import (
	"reflect"
	"testing"
)

// Each escape form the boundary review found must count: plain, ctx,
// nested context expression, split lines, renamed import, named constant and
// raw string. Calls whose program is not git must not.
func TestScanCountsEveryGitProgramForm(t *testing.T) {
	got, err := scan("testdata/escapes")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"escapes.go:7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}
