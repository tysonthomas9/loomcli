package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/archtest"
)

const testSourceHead = "0123456789abcdef0123456789abcdef01234567"

func TestRunChecksRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var output bytes.Buffer
	if err := run([]string{"check"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Architecture guardrails passed") {
		t.Fatalf("unexpected output: %s", output.String())
	}
	if !strings.Contains(output.String(), "in-scope non-test goroutine launch definitions: 107") {
		t.Fatalf("runtime launch count missing from output: %s", output.String())
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
