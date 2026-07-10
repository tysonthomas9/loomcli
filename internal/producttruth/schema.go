package producttruth

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var requiredDimensions = []string{"contract", "persistence", "ui", "retry"}

type Registry struct {
	Version    int         `yaml:"version"`
	Invariants []Invariant `yaml:"invariants"`
}

type Invariant struct {
	ID             string         `yaml:"id"`
	Title          string         `yaml:"title"`
	Stories        []string       `yaml:"stories"`
	SourceOfTruth  []TruthSource  `yaml:"source_of_truth"`
	Transition     Transition     `yaml:"transition"`
	UI             UIContract     `yaml:"ui"`
	Persistence    Persistence    `yaml:"persistence"`
	FailureRetry   FailureRetry   `yaml:"failure_retry"`
	Implementation Implementation `yaml:"implementation"`
	Proofs         []Proof        `yaml:"proofs"`
}

type Implementation struct {
	State string `yaml:"state"`
	Gap   string `yaml:"gap,omitempty"`
}

type TruthSource struct {
	System string `yaml:"system"`
	Record string `yaml:"record"`
}

type Transition struct {
	From   string `yaml:"from"`
	Action string `yaml:"action"`
	To     string `yaml:"to"`
}

type UIContract struct {
	Surfaces []string `yaml:"surfaces"`
	Expected string   `yaml:"expected"`
}

type Persistence struct {
	Query      string `yaml:"query"`
	Assertions string `yaml:"assertions"`
}

type FailureRetry struct {
	Failure     string `yaml:"failure"`
	Retry       string `yaml:"retry"`
	Idempotency string `yaml:"idempotency"`
}

type Proof struct {
	Dimension string `yaml:"dimension"`
	Kind      string `yaml:"kind"`
	Path      string `yaml:"path"`
	Selector  string `yaml:"selector"`
}

type Result struct {
	Registry Registry
	Errors   []string
}

func Validate(root, registryPath, catalogPath string) Result {
	var result Result
	// #nosec G304 -- registryPath is a repository-relative CLI input and proofs are separately constrained below.
	data, err := os.ReadFile(filepath.Join(root, registryPath))
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("registry: %v", err))
		return result
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&result.Registry); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("registry: %v", err))
		return result
	}
	stories, err := loadStories(filepath.Join(root, catalogPath))
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("catalog: %v", err))
		return result
	}
	if result.Registry.Version != 1 {
		result.Errors = append(result.Errors, fmt.Sprintf("registry: version is %d, want 1", result.Registry.Version))
	}
	validateInvariants(root, stories, &result)
	sort.Strings(result.Errors)
	return result
}

func validateInvariants(root string, stories map[string]bool, result *Result) {
	seen := map[string]bool{}
	for i := range result.Registry.Invariants {
		inv := &result.Registry.Invariants[i]
		prefix := inv.ID
		if prefix == "" {
			prefix = fmt.Sprintf("invariants[%d]", i)
		}
		if strings.TrimSpace(inv.ID) == "" || strings.TrimSpace(inv.Title) == "" {
			result.Errors = append(result.Errors, prefix+": id and title are required")
		}
		if seen[inv.ID] {
			result.Errors = append(result.Errors, prefix+": duplicate invariant id")
		}
		seen[inv.ID] = true
		if len(inv.Stories) == 0 {
			result.Errors = append(result.Errors, prefix+": at least one story is required")
		}
		for _, story := range inv.Stories {
			if !stories[story] {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: unknown story %s", prefix, story))
			}
		}
		validateContract(prefix, inv, &result.Errors)
		dimensions := map[string]bool{}
		for j, proof := range inv.Proofs {
			dimensions[proof.Dimension] = true
			validateProof(root, prefix, j, proof, &result.Errors)
		}
		for _, dimension := range requiredDimensions {
			if !dimensions[dimension] {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: missing %s proof", prefix, dimension))
			}
		}
	}
}

func validateContract(prefix string, inv *Invariant, errs *[]string) {
	required := map[string]string{
		"source_of_truth":           firstSource(inv.SourceOfTruth),
		"transition.from":           inv.Transition.From,
		"transition.action":         inv.Transition.Action,
		"transition.to":             inv.Transition.To,
		"ui.surfaces":               strings.Join(inv.UI.Surfaces, ","),
		"ui.expected":               inv.UI.Expected,
		"persistence.query":         inv.Persistence.Query,
		"persistence.assertions":    inv.Persistence.Assertions,
		"failure_retry.failure":     inv.FailureRetry.Failure,
		"failure_retry.retry":       inv.FailureRetry.Retry,
		"failure_retry.idempotency": inv.FailureRetry.Idempotency,
	}
	switch inv.Implementation.State {
	case "enforced":
		if strings.TrimSpace(inv.Implementation.Gap) != "" {
			*errs = append(*errs, fmt.Sprintf("%s: enforced invariant must not declare an implementation gap", prefix))
		}
	case "fail_closed":
		if strings.TrimSpace(inv.Implementation.Gap) == "" {
			*errs = append(*errs, fmt.Sprintf("%s: fail_closed invariant must describe the missing capability", prefix))
		}
	default:
		*errs = append(*errs, fmt.Sprintf("%s: implementation.state must be enforced or fail_closed", prefix))
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			*errs = append(*errs, fmt.Sprintf("%s: missing %s", prefix, field))
		}
	}
}

func firstSource(sources []TruthSource) string {
	if len(sources) == 0 {
		return ""
	}
	return sources[0].System + sources[0].Record
}

func validateProof(root, prefix string, index int, proof Proof, errs *[]string) {
	label := fmt.Sprintf("%s: proofs[%d]", prefix, index)
	if proof.Dimension == "" || proof.Kind == "" || proof.Path == "" || proof.Selector == "" {
		*errs = append(*errs, label+": dimension, kind, path, and selector are required")
		return
	}
	if filepath.IsAbs(proof.Path) || strings.HasPrefix(filepath.Clean(proof.Path), "..") {
		*errs = append(*errs, label+": path must stay within the repository")
		return
	}
	// #nosec G304 -- absolute and parent-traversal paths are rejected immediately above.
	data, err := os.ReadFile(filepath.Join(root, proof.Path))
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: proof file: %v", label, err))
		return
	}
	if !bytes.Contains(data, []byte(proof.Selector)) {
		*errs = append(*errs, fmt.Sprintf("%s: selector %q not found in %s", label, proof.Selector, proof.Path))
	}
}

func loadStories(path string) (map[string]bool, error) {
	// #nosec G304 -- the caller supplies the repository-owned catalog path.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] != "Story ID" {
		return nil, fmt.Errorf("first column must be Story ID")
	}
	out := map[string]bool{}
	for _, row := range rows[1:] {
		if len(row) > 0 && strings.TrimSpace(row[0]) != "" {
			out[row[0]] = true
		}
	}
	return out, nil
}

func Format(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "product truths: %d invariants\n", len(result.Registry.Invariants))
	for _, inv := range result.Registry.Invariants {
		dims := map[string]bool{}
		for _, proof := range inv.Proofs {
			dims[proof.Dimension] = true
		}
		fmt.Fprintf(&b, "- %s state=%s", inv.ID, inv.Implementation.State)
		for _, dim := range requiredDimensions {
			mark := "missing"
			if dims[dim] {
				mark = "mapped"
			}
			fmt.Fprintf(&b, " %s=%s", dim, mark)
		}
		b.WriteByte('\n')
		if inv.Implementation.Gap != "" {
			fmt.Fprintf(&b, "  GAP: %s\n", inv.Implementation.Gap)
		}
	}
	if len(result.Errors) == 0 {
		b.WriteString("product truths: PASS\n")
	} else {
		fmt.Fprintf(&b, "product truths: FAIL (%d problems)\n", len(result.Errors))
		for _, err := range result.Errors {
			fmt.Fprintf(&b, "  - %s\n", err)
		}
	}
	return b.String()
}
