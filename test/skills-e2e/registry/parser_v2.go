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

type goTestEvidenceParser struct {
	report      EvidenceReport
	backend     Backend
	provider    Provider
	pending     map[string][]evidenceMarker
	seenMarkers map[string]struct{}
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
	if err := validateCoordinate(EvidenceCoordinate{Repository: repository, Backend: backend, Provider: provider}); err != nil {
		return EvidenceReport{}, err
	}
	if strings.TrimSpace(revision) == "" {
		return EvidenceReport{}, fmt.Errorf("revision is required")
	}
	parser := goTestEvidenceParser{
		report:      EvidenceReport{SchemaVersion: EvidenceSchemaVersion, Repository: repository, Revision: revision},
		backend:     backend,
		provider:    provider,
		pending:     make(map[string][]evidenceMarker),
		seenMarkers: make(map[string]struct{}),
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event goTestJSONEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return EvidenceReport{}, fmt.Errorf("decode go test event: %w", err)
		}
		if err := parser.consume(event); err != nil {
			return EvidenceReport{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return EvidenceReport{}, err
	}
	return parser.finish()
}

func (p *goTestEvidenceParser) consume(event goTestJSONEvent) error {
	if event.Action == "output" {
		return p.consumeOutput(event)
	}
	if event.Action == "pass" && event.Test != "" {
		p.consumePass(event)
	}
	return nil
}

func (p *goTestEvidenceParser) consumeOutput(event goTestJSONEvent) error {
	position := strings.Index(event.Output, LoomEvidenceMarkerV2+" ")
	if position < 0 {
		return nil
	}
	if strings.Contains(event.Test, "/") {
		return fmt.Errorf("evidence must be declared by a top-level test, got %q", event.Test)
	}
	encoded := strings.TrimSpace(event.Output[position+len(LoomEvidenceMarkerV2)+1:])
	var marker evidenceMarker
	if err := json.Unmarshal([]byte(encoded), &marker); err != nil {
		return fmt.Errorf("decode evidence marker: %w", err)
	}
	if marker.Test != event.Test {
		return fmt.Errorf("marker test %q does not match event test %q", marker.Test, event.Test)
	}
	if err := validateCaseID(marker.ID); err != nil {
		return err
	}
	key := event.Package + "\x00" + event.Test
	markerKey := fmt.Sprintf("%s\x00%d", key, marker.ID)
	if _, duplicate := p.seenMarkers[markerKey]; duplicate {
		return fmt.Errorf("duplicate marker for case %d in %s %s", marker.ID, event.Package, event.Test)
	}
	p.seenMarkers[markerKey] = struct{}{}
	p.pending[key] = append(p.pending[key], marker)
	return nil
}

func (p *goTestEvidenceParser) consumePass(event goTestJSONEvent) {
	key := event.Package + "\x00" + event.Test
	for _, marker := range p.pending[key] {
		p.report.Evidence = append(p.report.Evidence, Evidence{
			ID: marker.ID, Package: event.Package, Test: event.Test, Backend: p.backend, Provider: p.provider,
		})
	}
	delete(p.pending, key)
}

func (p *goTestEvidenceParser) finish() (EvidenceReport, error) {
	sort.Slice(p.report.Evidence, func(i, j int) bool {
		if p.report.Evidence[i].ID != p.report.Evidence[j].ID {
			return p.report.Evidence[i].ID < p.report.Evidence[j].ID
		}
		return p.report.Evidence[i].Package+"\x00"+p.report.Evidence[i].Test < p.report.Evidence[j].Package+"\x00"+p.report.Evidence[j].Test
	})
	if err := ValidateEvidenceReport(p.report); err != nil {
		return EvidenceReport{}, err
	}
	return p.report, nil
}
