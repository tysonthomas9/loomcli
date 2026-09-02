package producttruth

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const scenarioMapKind = "aft-scenario-map"

var scenarioStatuses = map[string]bool{
	"covered":            true,
	"partial":            true,
	"missing":            true,
	"implementation-gap": true,
	"lower-level":        true,
	"opt-in-live":        true,
}

// ScenarioMap is the reviewed mapping between Loom product surfaces and AFT
// browser evidence. Reachability census data is deliberately kept separate
// from semantic coverage and gaps.
type ScenarioMap struct {
	Kind                string                 `yaml:"kind"`
	Version             int                    `yaml:"version"`
	AsOf                string                 `yaml:"asOf"`
	Scope               ScenarioMapScope       `yaml:"scope"`
	ExecutionInvariants []string               `yaml:"executionInvariants"`
	Census              ScenarioMapCensus      `yaml:"census"`
	StoryAudit          ScenarioMapStoryAudit  `yaml:"storyAudit"`
	Families            []ScenarioFamily       `yaml:"families"`
	ImplementationGaps  []ScenarioGap          `yaml:"implementationGaps"`
	LowerLevelOnly      []string               `yaml:"lowerLevelOnly"`
	KnownFailures       []ScenarioKnownFailure `yaml:"knownFailures"`
}

type ScenarioMapScope struct {
	Gate                      string                      `yaml:"gate"`
	Executions                int                         `yaml:"executions"`
	ProductExecutions         int                         `yaml:"productExecutions"`
	SurfaceContractExecutions int                         `yaml:"surfaceContractExecutions"`
	ObservedBaseline          ScenarioMapObservedBaseline `yaml:"observedBaseline"`
	ExcludedTiers             []string                    `yaml:"excludedTiers"`
}

type ScenarioMapObservedBaseline struct {
	Passed int    `yaml:"passed"`
	Failed int    `yaml:"failed"`
	RunID  string `yaml:"runId"`
}

type ScenarioMapCensus struct {
	Note       string                 `yaml:"note"`
	Routes     ScenarioMapCensusCount `yaml:"routes"`
	Components ScenarioMapCensusCount `yaml:"components"`
	Endpoints  ScenarioMapCensusCount `yaml:"endpoints"`
	TestIDs    ScenarioMapCensusCount `yaml:"testids"`
}

type ScenarioMapCensusCount struct {
	Reached    int    `yaml:"reached"`
	Total      int    `yaml:"total"`
	Limitation string `yaml:"limitation"`
}

type ScenarioMapStoryAudit struct {
	Registry    string                     `yaml:"registry"`
	Note        string                     `yaml:"note"`
	Unannotated []ScenarioStoryDisposition `yaml:"unannotated"`
}

type ScenarioStoryDisposition struct {
	Story       string   `yaml:"story"`
	Disposition string   `yaml:"disposition"`
	Gap         string   `yaml:"gap,omitempty"`
	Gaps        []string `yaml:"gaps,omitempty"`
}

type ScenarioFamily struct {
	ID        string              `yaml:"id"`
	Status    string              `yaml:"status"`
	Stories   []string            `yaml:"stories"`
	Owns      map[string][]string `yaml:"owns"`
	Scenarios []string            `yaml:"scenarios"`
	Proves    []string            `yaml:"proves"`
	Missing   []ScenarioGap       `yaml:"missing"`
}

type ScenarioGap struct {
	ID       string   `yaml:"id"`
	Status   string   `yaml:"status"`
	Priority string   `yaml:"priority"`
	Stories  []string `yaml:"stories"`
	Code     any      `yaml:"code"`
	Scenario string   `yaml:"scenario"`
	Finding  string   `yaml:"finding"`
}

type ScenarioKnownFailure struct {
	Source         string `yaml:"source"`
	SelectorSource string `yaml:"selectorSource,omitempty"`
	Scenario       string `yaml:"scenario"`
}

type ScenarioMapResult struct {
	Map    ScenarioMap
	Errors []string
}

// ValidateScenarioMap validates the machine-checkable structure of the AFT
// scenario map. Whether a scenario semantically proves a claimed behavior
// remains a human review decision.
func ValidateScenarioMap(root, mapPath string) ScenarioMapResult {
	var result ScenarioMapResult
	fullPath, err := scenarioMapRepoPath(root, mapPath)
	if err != nil {
		result.Errors = append(result.Errors, "scenario map: "+err.Error())
		return result
	}
	// #nosec G304 -- scenarioMapRepoPath rejects absolute and parent-traversal paths.
	data, err := os.ReadFile(fullPath)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("scenario map: %v", err))
		return result
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&result.Map); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("scenario map: %v", err))
		return result
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			result.Errors = append(result.Errors, "scenario map: multiple YAML documents are not allowed")
		} else {
			result.Errors = append(result.Errors, fmt.Sprintf("scenario map: trailing YAML: %v", err))
		}
		return result
	}

	validateScenarioMap(root, &result)
	sort.Strings(result.Errors)
	return result
}

func validateScenarioMap(root string, result *ScenarioMapResult) {
	m := &result.Map
	if m.Kind != scenarioMapKind {
		result.Errors = append(result.Errors, fmt.Sprintf("scenario map: kind is %q, want %q", m.Kind, scenarioMapKind))
	}
	if m.Version != 1 {
		result.Errors = append(result.Errors, fmt.Sprintf("scenario map: version is %d, want 1", m.Version))
	}
	if m.Scope.Executions != m.Scope.ProductExecutions+m.Scope.SurfaceContractExecutions {
		result.Errors = append(result.Errors, "scenario map: productExecutions + surfaceContractExecutions must equal executions")
	}
	if m.Scope.Executions != m.Scope.ObservedBaseline.Passed+m.Scope.ObservedBaseline.Failed {
		result.Errors = append(result.Errors, "scenario map: observed passed + failed must equal executions")
	}

	stories := loadScenarioMapStories(root, m.StoryAudit.Registry, &result.Errors)
	knownScenarios := make(map[string]bool)
	gapIDs := make(map[string]bool)
	familyIDs := make(map[string]bool)

	for i := range m.Families {
		family := &m.Families[i]
		prefix := family.ID
		if prefix == "" {
			prefix = fmt.Sprintf("families[%d]", i)
			result.Errors = append(result.Errors, prefix+": id is required")
		}
		if familyIDs[family.ID] {
			result.Errors = append(result.Errors, prefix+": duplicate family id")
		}
		familyIDs[family.ID] = true
		validateScenarioStatus(prefix, family.Status, &result.Errors)
		validateScenarioStories(prefix, family.Stories, stories, &result.Errors)
		if family.Status == "covered" && hasActiveScenarioGap(family.Missing) {
			result.Errors = append(result.Errors, prefix+": covered family must not declare a missing or implementation gap")
		}
		for j, source := range family.Scenarios {
			label := fmt.Sprintf("%s: scenarios[%d]", prefix, j)
			if validateScenarioFile(root, label, source, &result.Errors) {
				knownScenarios[filepath.Clean(source)] = true
			}
		}
		for j := range family.Missing {
			validateScenarioGap(root, fmt.Sprintf("%s: missing[%d]", prefix, j), &family.Missing[j], stories, gapIDs, &result.Errors)
		}
	}

	for i := range m.ImplementationGaps {
		validateScenarioGap(root, fmt.Sprintf("implementationGaps[%d]", i), &m.ImplementationGaps[i], stories, gapIDs, &result.Errors)
	}
	validateScenarioStoryAudit(&m.StoryAudit, stories, gapIDs, &result.Errors)

	for i := range m.KnownFailures {
		validateScenarioKnownFailure(root, i, &m.KnownFailures[i], knownScenarios, &result.Errors)
	}
}

func loadScenarioMapStories(root, registry string, errs *[]string) map[string]bool {
	if strings.TrimSpace(registry) == "" {
		*errs = append(*errs, "storyAudit: registry is required")
		return nil
	}
	fullPath, err := scenarioMapRepoPath(root, registry)
	if err != nil {
		*errs = append(*errs, "storyAudit: registry: "+err.Error())
		return nil
	}
	stories, err := loadStories(fullPath)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("storyAudit: registry: %v", err))
		return nil
	}
	return stories
}

func validateScenarioStoryAudit(audit *ScenarioMapStoryAudit, stories, gapIDs map[string]bool, errs *[]string) {
	seen := make(map[string]bool)
	for i, item := range audit.Unannotated {
		prefix := fmt.Sprintf("storyAudit: unannotated[%d]", i)
		if item.Story == "" {
			*errs = append(*errs, prefix+": story is required")
		} else if seen[item.Story] {
			*errs = append(*errs, prefix+": duplicate story "+item.Story)
		}
		seen[item.Story] = true
		validateScenarioStories(prefix, []string{item.Story}, stories, errs)
		validateScenarioStatus(prefix, item.Disposition, errs)
		refs := append([]string{}, item.Gaps...)
		if item.Gap != "" {
			refs = append(refs, item.Gap)
		}
		for _, gapID := range refs {
			if !gapIDs[gapID] {
				*errs = append(*errs, fmt.Sprintf("%s: unknown gap %s", prefix, gapID))
			}
		}
	}
}

func validateScenarioGap(root, prefix string, gap *ScenarioGap, stories map[string]bool, gapIDs map[string]bool, errs *[]string) {
	if gap.ID == "" {
		*errs = append(*errs, prefix+": id is required")
	} else if gapIDs[gap.ID] {
		*errs = append(*errs, prefix+": duplicate gap id "+gap.ID)
	}
	gapIDs[gap.ID] = true
	validateScenarioStatus(prefix, gap.Status, errs)
	if gap.Priority != "P0" && gap.Priority != "P1" && gap.Priority != "P2" && gap.Priority != "P3" {
		*errs = append(*errs, prefix+": priority must be P0, P1, P2, or P3")
	}
	if strings.TrimSpace(gap.Scenario) == "" && strings.TrimSpace(gap.Finding) == "" {
		*errs = append(*errs, prefix+": scenario or finding is required")
	}
	validateScenarioStories(prefix, gap.Stories, stories, errs)
	for label, path := range scenarioCodePaths(gap.Code) {
		validateScenarioPath(root, prefix+": code."+label, path, false, errs)
	}
}

func validateScenarioStories(prefix string, refs []string, stories map[string]bool, errs *[]string) {
	for _, story := range refs {
		if story == "" || !stories[story] {
			*errs = append(*errs, fmt.Sprintf("%s: unknown story %s", prefix, story))
		}
	}
}

func validateScenarioStatus(prefix, status string, errs *[]string) {
	if !scenarioStatuses[status] {
		*errs = append(*errs, fmt.Sprintf("%s: unknown status %q", prefix, status))
	}
}

func hasActiveScenarioGap(gaps []ScenarioGap) bool {
	for _, gap := range gaps {
		if gap.Status == "missing" || gap.Status == "implementation-gap" {
			return true
		}
	}
	return false
}

func validateScenarioKnownFailure(root string, index int, failure *ScenarioKnownFailure, knownScenarios map[string]bool, errs *[]string) {
	prefix := fmt.Sprintf("knownFailures[%d]", index)
	if !validateScenarioFile(root, prefix+": source", failure.Source, errs) {
		return
	}
	if !knownScenarios[filepath.Clean(failure.Source)] {
		*errs = append(*errs, prefix+": source is not listed by a scenario family")
	}
	if strings.TrimSpace(failure.Scenario) == "" {
		*errs = append(*errs, prefix+": scenario is required")
		return
	}

	sources, err := scenarioGraphSources(root, failure.Source)
	if err != nil {
		*errs = append(*errs, prefix+": "+err.Error())
		return
	}
	selectorSource := filepath.Clean(failure.SelectorSource)
	if failure.SelectorSource != "" {
		if _, ok := sources[selectorSource]; !ok {
			*errs = append(*errs, prefix+": selectorSource is not the source or one of its graph imports")
			return
		}
		sources = map[string][]byte{selectorSource: sources[selectorSource]}
	}
	for _, data := range sources {
		if bytes.Contains(data, []byte(failure.Scenario)) {
			return
		}
	}
	*errs = append(*errs, fmt.Sprintf("%s: scenario %q not found in source or graph imports", prefix, failure.Scenario))
}

func scenarioGraphSources(root, source string) (map[string][]byte, error) {
	found := make(map[string][]byte)
	var visit func(string) error
	visit = func(path string) error {
		clean := filepath.Clean(path)
		if _, ok := found[clean]; ok {
			return nil
		}
		fullPath, err := scenarioMapRepoPath(root, clean)
		if err != nil {
			return err
		}
		// #nosec G304 -- scenarioMapRepoPath rejects absolute and parent-traversal paths.
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("read graph source %s: %w", clean, err)
		}
		found[clean] = data
		var imports struct {
			Imports []string `yaml:"imports"`
		}
		if err := yaml.Unmarshal(data, &imports); err != nil {
			return fmt.Errorf("parse graph source %s: %w", clean, err)
		}
		for _, imported := range imports.Imports {
			if err := visit(filepath.Join(filepath.Dir(clean), imported)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(source); err != nil {
		return nil, err
	}
	return found, nil
}

func validateScenarioFile(root, prefix, path string, errs *[]string) bool {
	return validateScenarioPath(root, prefix, path, true, errs)
}

func validateScenarioPath(root, prefix, path string, requireFile bool, errs *[]string) bool {
	fullPath, err := scenarioMapRepoPath(root, path)
	if err != nil {
		*errs = append(*errs, prefix+": "+err.Error())
		return false
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: %v", prefix, err))
		return false
	}
	if requireFile && !info.Mode().IsRegular() {
		*errs = append(*errs, prefix+": path must name a file")
		return false
	}
	return true
}

func scenarioMapRepoPath(root, path string) (string, error) {
	clean := filepath.Clean(path)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay within the repository: %s", path)
	}
	return filepath.Join(root, clean), nil
}

func scenarioCodePaths(code any) map[string]string {
	paths := make(map[string]string)
	var walk func(string, any)
	walk = func(label string, value any) {
		switch typed := value.(type) {
		case string:
			paths[label] = typed
		case map[string]any:
			for key, child := range typed {
				childLabel := key
				if label != "" {
					childLabel = label + "." + key
				}
				walk(childLabel, child)
			}
		case []any:
			for index, child := range typed {
				walk(fmt.Sprintf("%s[%d]", label, index), child)
			}
		}
	}
	walk("path", code)
	return paths
}

func FormatScenarioMap(result ScenarioMapResult) string {
	var b strings.Builder
	gapCount := len(result.Map.ImplementationGaps)
	for _, family := range result.Map.Families {
		gapCount += len(family.Missing)
	}
	fmt.Fprintf(&b, "AFT scenario map: %d families, %d gaps\n", len(result.Map.Families), gapCount)
	if len(result.Errors) == 0 {
		b.WriteString("AFT scenario map: PASS\n")
		return b.String()
	}
	for _, err := range result.Errors {
		fmt.Fprintf(&b, "ERROR: %s\n", err)
	}
	b.WriteString("AFT scenario map: FAIL\n")
	return b.String()
}
