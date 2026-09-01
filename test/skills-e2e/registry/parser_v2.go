package registry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const LoomEvidenceMarkerV2 = "LOOM_EDGE_CASE_V2"

type evidenceMarker struct {
	ID   int    `json:"id"`
	Test string `json:"test"`
}

type goTestJSONEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// EvidenceMarker is logged beside an executable test. Canonical semantics and
// execution coordinates are deliberately absent from this transport marker.
func EvidenceMarker(id int, test string) string {
	body, _ := json.Marshal(evidenceMarker{ID: id, Test: test})
	return LoomEvidenceMarkerV2 + " " + string(body)
}

// MarkEvidence declares a canonical ID beside a test. go test -json supplies
// the package, and ParseGoTestJSON promotes it only if this exact test passes.
func MarkEvidence(t interface {
	Helper()
	Name() string
	Log(...any)
}, ids ...int) {
	t.Helper()
	for _, id := range ids {
		t.Log(EvidenceMarker(id, t.Name()))
	}
}

func ParseGoTestJSON(r io.Reader, repository Repository, revision string, backend Backend, provider Provider) (EvidenceReport, error) {
	report := EvidenceReport{SchemaVersion: EvidenceSchemaVersion, Repository: repository, Revision: revision}
	if err := validateCoordinate(EvidenceCoordinate{Repository: repository, Backend: backend, Provider: provider}); err != nil {
		return EvidenceReport{}, err
	}
	if strings.TrimSpace(revision) == "" {
		return EvidenceReport{}, fmt.Errorf("revision is required")
	}
	pending := make(map[string][]evidenceMarker)
	seenMarkers := make(map[string]struct{})
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event goTestJSONEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return EvidenceReport{}, fmt.Errorf("decode go test event: %w", err)
		}
		key := event.Package + "\x00" + event.Test
		if event.Action == "output" {
			position := strings.Index(event.Output, LoomEvidenceMarkerV2+" ")
			if position < 0 {
				continue
			}
			if strings.Contains(event.Test, "/") {
				return EvidenceReport{}, fmt.Errorf("evidence must be declared by a top-level test, got %q", event.Test)
			}
			encoded := strings.TrimSpace(event.Output[position+len(LoomEvidenceMarkerV2)+1:])
			var marker evidenceMarker
			if err := json.Unmarshal([]byte(encoded), &marker); err != nil {
				return EvidenceReport{}, fmt.Errorf("decode evidence marker: %w", err)
			}
			if marker.Test != event.Test {
				return EvidenceReport{}, fmt.Errorf("marker test %q does not match event test %q", marker.Test, event.Test)
			}
			if err := validateCaseID(marker.ID); err != nil {
				return EvidenceReport{}, err
			}
			markerKey := fmt.Sprintf("%s\x00%d", key, marker.ID)
			if _, duplicate := seenMarkers[markerKey]; duplicate {
				return EvidenceReport{}, fmt.Errorf("duplicate marker for case %d in %s %s", marker.ID, event.Package, event.Test)
			}
			seenMarkers[markerKey] = struct{}{}
			pending[key] = append(pending[key], marker)
			continue
		}
		if event.Action != "pass" || event.Test == "" {
			continue
		}
		for _, marker := range pending[key] {
			report.Evidence = append(report.Evidence, Evidence{ID: marker.ID, Package: event.Package, Test: event.Test, Backend: backend, Provider: provider})
		}
		delete(pending, key)
	}
	if err := scanner.Err(); err != nil {
		return EvidenceReport{}, err
	}
	sort.Slice(report.Evidence, func(i, j int) bool {
		if report.Evidence[i].ID != report.Evidence[j].ID {
			return report.Evidence[i].ID < report.Evidence[j].ID
		}
		return report.Evidence[i].Package+"\x00"+report.Evidence[i].Test < report.Evidence[j].Package+"\x00"+report.Evidence[j].Test
	})
	if err := ValidateEvidenceReport(report); err != nil {
		return EvidenceReport{}, err
	}
	return report, nil
}
