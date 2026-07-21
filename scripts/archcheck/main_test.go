package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/archtest"
)

const testSourceHead = "0123456789abcdef0123456789abcdef01234567"

func TestRunRepositoryCheckFormatsSummary(t *testing.T) {
	wantRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wantRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	wantManifestDir := filepath.Join(wantRoot, "internal", "archtest", "testdata")
	var output bytes.Buffer
	check := func(root, manifestDir string) (archtest.Report, error) {
		if root != wantRoot || manifestDir != wantManifestDir {
			t.Fatalf("repository paths = (%q, %q), want (%q, %q)", root, manifestDir, wantRoot, wantManifestDir)
		}
		return archtest.Report{
			CompositeStoreFiles:          make([]string, 2),
			CompositeStoreOutside:        make([]string, 1),
			CompositeStoreMaximum:        3,
			CompositeStoreOutsideMaximum: 2,
			LegacyHandlerImports:         make([]archtest.LegacyImportUse, 4),
			LegacyHandlerImportMaximum:   5,
			DirectPersistenceWrites:      6,
			ModuleRoots:                  []string{"artifacts", "execution"},
			MutationCommands:             7,
			RuntimeComponents:            8,
			RuntimeGoroutineLaunches:     9,
			PerformanceMetrics:           10,
			PerformanceMetricsMeasured:   9,
			PerformanceMetricsDeferred:   1,
			PendingDecisions:             []string{"decision"},
			AnalysisProfilesEnforced:     11,
			AnalysisProfileTotal:         12,
		}, nil
	}
	if err := runWithRepositoryCheck([]string{"check"}, &output, check); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Architecture guardrails passed",
		"composite Store files: 2/3",
		"outside composition: 1/2",
		"legacy handler imports: 4/5",
		"direct persistence-write rows: 6",
		"capability module roots: 2",
		"reviewed mutation commands: 7",
		"named runtime components: 8",
		"in-scope non-test goroutine launch definitions: 9",
		"performance records: 10 (9 measured, 1 explicitly deferred)",
		"pending architecture decisions: 1",
		"build profiles enforced: 11/12",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
}

func TestRunRepositoryCheckPropagatesCheckerError(t *testing.T) {
	want := errors.New("repository check failed")
	check := func(_, _ string) (archtest.Report, error) {
		return archtest.Report{}, want
	}
	if err := runWithRepositoryCheck([]string{"check"}, &bytes.Buffer{}, check); !errors.Is(err, want) {
		t.Fatalf("runWithRepositoryCheck error = %v, want %v", err, want)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run([]string{"unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected usage error")
	}
}

func TestParseSnapshotProvenance(t *testing.T) {
	for _, test := range []struct {
		name      string
		head      string
		status    string
		wantDirty bool
		wantErr   bool
	}{
		{name: "clean", head: testSourceHead + "\n"},
		{name: "dirty", head: testSourceHead + "\n", status: " M internal/archtest/direct_writes.go\n", wantDirty: true},
		{name: "reject abbreviated", head: "01234567\n", wantErr: true},
		{name: "reject uppercase", head: "0123456789ABCDEF0123456789ABCDEF01234567\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseSnapshotProvenance([]byte(test.head), []byte(test.status))
			if test.wantErr {
				if err == nil {
					t.Fatalf("provenance = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.SourceHead != testSourceHead || got.SourceDirty != test.wantDirty {
				t.Fatalf("provenance = %+v, want HEAD %s and dirty %t", got, testSourceHead, test.wantDirty)
			}
		})
	}
}

func TestEncodeDirectWriteSnapshotIncludesProvenance(t *testing.T) {
	want := directWriteSnapshot{
		SourceHead:       testSourceHead,
		SourceDirty:      true,
		AnalysisProfiles: []string{"linux-amd64", "race"},
		Writes: []archtest.DirectWriteUse{{
			File: "internal/cli/write.go", Receiver: "example/internal/store.WorkspaceStore",
			Method: "Create", Count: 1, AggregateOwner: "workspace", ExpiresAfterPhase: 7,
		}},
	}
	var output bytes.Buffer
	if err := encodeDirectWriteSnapshot(&output, want); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "source_head: "+testSourceHead+"\nsource_dirty: true\nanalysis_profiles:\n") {
		t.Fatalf("snapshot does not lead with reproducibility provenance:\n%s", output.String())
	}
	var got directWriteSnapshot
	if err := yaml.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded snapshot = %+v, want %+v", got, want)
	}
}
