package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

const sample = `package demo

import "fmt"

// Hello greets.
func Hello() {
	url := "http://example.com//path"
	raw := ` + "`// not a comment`" + `
	fmt.Println(url, raw) // trailing
	/* block
	spanning
	lines */
	x := 1 /* inline */ + 2
	_ = x
}

//go:generate echo hi
//nolint:gosec
// go:generate spaced
`

func scanAll(t *testing.T, added []lineRange) []int {
	t.Helper()
	var lines []int
	for _, v := range scanFile("demo.go", []byte(sample), added) {
		lines = append(lines, v.line)
	}
	return lines
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScanFileFlagsOnlyAddedCommentLines(t *testing.T) {
	cases := []struct {
		name  string
		added []lineRange
		want  []int
	}{
		{"line comment", []lineRange{{5, 5}}, []int{5}},
		{"string and raw string with slashes", []lineRange{{7, 8}}, nil},
		{"trailing comment", []lineRange{{9, 9}}, []int{9}},
		{"block comment middle line", []lineRange{{11, 11}}, []int{11}},
		{"whole block comment", []lineRange{{10, 12}}, []int{10, 11, 12}},
		{"inline block comment", []lineRange{{13, 13}}, []int{13}},
		{"go directive exempt", []lineRange{{17, 17}}, nil},
		{"nolint is flagged", []lineRange{{18, 18}}, []int{18}},
		{"spaced go directive is flagged", []lineRange{{19, 19}}, []int{19}},
		{"no added lines", nil, nil},
		{"code only", []lineRange{{6, 6}, {14, 15}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanAll(t, tc.added)
			if !equalInts(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScanFileReportsLineText(t *testing.T) {
	got := scanFile("demo.go", []byte(sample), []lineRange{{9, 9}})
	if len(got) != 1 || got[0].text != "fmt.Println(url, raw) // trailing" {
		t.Fatalf("unexpected violations: %+v", got)
	}
}

func TestParseRanges(t *testing.T) {
	ranges, order, err := parseRanges(strings.NewReader("a.go\t1\t2\n\nb.go\t3\t3\na.go\t5\t6\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "a.go" || order[1] != "b.go" {
		t.Fatalf("order = %v", order)
	}
	if len(ranges["a.go"]) != 2 || ranges["a.go"][1] != (lineRange{5, 6}) {
		t.Fatalf("ranges = %v", ranges)
	}
}

func TestParseRangesRejectsMalformed(t *testing.T) {
	for _, in := range []string{"a.go\t1\n", "a.go\tx\t2\n", "a.go\t3\t2\n", "a.go\t0\t1\n"} {
		if _, _, err := parseRanges(strings.NewReader(in)); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

func TestRun(t *testing.T) {
	fsys := fstest.MapFS{
		"pkg/a.go": {Data: []byte("package a\n\n// new\nvar s = \"//x\"\n")},
		"pkg/b.go": {Data: []byte("package b\n\n//go:build linux\n")},
	}
	var out bytes.Buffer
	code := run(strings.NewReader("pkg/a.go\t3\t4\npkg/b.go\t3\t3\n"), &out, fsys)
	if code != 1 {
		t.Fatalf("exit = %d, out = %s", code, out.String())
	}
	if out.String() != "pkg/a.go:3: // new\n" {
		t.Fatalf("out = %q", out.String())
	}

	out.Reset()
	if code := run(strings.NewReader("pkg/b.go\t1\t3\n"), &out, fsys); code != 0 || out.Len() != 0 {
		t.Fatalf("clean run: exit = %d, out = %q", code, out.String())
	}

	out.Reset()
	if code := run(strings.NewReader("missing.go\t1\t1\n"), &out, fsys); code != 2 {
		t.Fatalf("missing file exit = %d", code)
	}

	out.Reset()
	if code := run(strings.NewReader("bad\n"), &out, fsys); code != 2 {
		t.Fatalf("malformed exit = %d", code)
	}
}
