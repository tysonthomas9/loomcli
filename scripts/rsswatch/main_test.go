package main

import (
	"testing"
	"time"
)

func TestParseWatchOptions(t *testing.T) {
	options, err := parseWatchOptions([]string{"2048", "900", "go", "test", "./internal/archtest"})
	if err != nil {
		t.Fatal(err)
	}
	if options.limitMiB != 2048 || options.timeout != 15*time.Minute || options.command != "go" {
		t.Fatalf("options = %+v", options)
	}
	if len(options.commandArgs) != 2 || options.commandArgs[0] != "test" || options.commandArgs[1] != "./internal/archtest" {
		t.Fatalf("command args = %v", options.commandArgs)
	}
	for _, args := range [][]string{
		{},
		{"0", "900", "go"},
		{"2048", "0", "go"},
	} {
		if _, err := parseWatchOptions(args); err == nil {
			t.Fatalf("parseWatchOptions(%v) succeeded, want error", args)
		}
	}
}

func TestTreeRSSIncludesOnlyDescendants(t *testing.T) {
	processes, err := parseProcessTable([]byte("100 1 100\n101 100 50\n102 101 25\n200 1 999\nmalformed\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := treeRSS(100, processes), int64(175); got != want {
		t.Fatalf("tree RSS = %d KiB, want %d", got, want)
	}
	if got := treeRSS(300, processes); got != 0 {
		t.Fatalf("missing-root tree RSS = %d KiB, want 0", got)
	}
}
