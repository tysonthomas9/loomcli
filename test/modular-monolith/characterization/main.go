package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	manifestSchemaVersion = 1
	manifestSuite         = "modular-monolith-phase1-characterization"
	defaultManifestPath   = "test/modular-monolith/characterization-matrix.yaml"
)

var requiredRowIDs = []string{
	"workflow-approval",
	"trigger-admission",
	"agent-provisioning",
	"execution-recovery",
	"supervisor-policy",
}

type manifest struct {
	SchemaVersion int    `yaml:"schema_version"`
	Suite         string `yaml:"suite"`
	Description   string `yaml:"description"`
	Rows          []row  `yaml:"rows"`
}

type row struct {
	ID               string   `yaml:"id"`
	Package          string   `yaml:"package"`
	TestRegex        string   `yaml:"test_regex"`
	ExpectedTests    []string `yaml:"expected_tests"`
	Timeout          string   `yaml:"timeout"`
	ExpectedBehavior []string `yaml:"expected_behavior"`
}

func main() {
	manifestFlag := flag.String("manifest", defaultManifestPath, "path to the characterization manifest, relative to the repository root")
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fatal(err)
	}
	manifestPath := *manifestFlag
	if !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(root, manifestPath)
	}

	m, err := loadManifest(manifestPath)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("characterization manifest valid: %d authoritative rows\n", len(m.Rows))

	for i, r := range m.Rows {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(m.Rows), r.ID)
		fmt.Printf("  package: %s\n", r.Package)
		fmt.Printf("  tests: %s\n", strings.Join(r.ExpectedTests, ", "))
		for _, behavior := range r.ExpectedBehavior {
			fmt.Printf("  expect: %s\n", behavior)
		}

		discovered, err := discoverTests(root, r)
		if err != nil {
			fatal(fmt.Errorf("row %q: %w", r.ID, err))
		}
		if err := compareDiscoveredTests(r.ExpectedTests, discovered); err != nil {
			fatal(fmt.Errorf("row %q: %w", r.ID, err))
		}
		if err := executeRow(root, r); err != nil {
			fatal(fmt.Errorf("row %q: %w", r.ID, err))
		}
		fmt.Printf("  result: PASS\n")
	}

	fmt.Printf("\nPhase 1 characterization gate PASS (%d/%d rows)\n", len(m.Rows), len(requiredRowIDs))
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "characterization gate FAIL: %v\n", err)
	os.Exit(1)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect go.mod in %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find repository root containing go.mod")
		}
		dir = parent
	}
}

func loadManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The operator selects a local YAML manifest; it is parsed as data, never executed.
	if err != nil {
		return manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	return decodeManifest(data)
}

func decodeManifest(data []byte) (manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var m manifest
	if err := decoder.Decode(&m); err != nil {
		return manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifest{}, errors.New("decode manifest: multiple YAML documents are not allowed")
		}
		return manifest{}, fmt.Errorf("decode manifest trailer: %w", err)
	}
	if err := validateManifest(m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

func validateManifest(m manifest) error {
	if m.SchemaVersion != manifestSchemaVersion {
		return fmt.Errorf("schema_version = %d, want %d", m.SchemaVersion, manifestSchemaVersion)
	}
	if m.Suite != manifestSuite {
		return fmt.Errorf("suite = %q, want %q", m.Suite, manifestSuite)
	}
	if strings.TrimSpace(m.Description) == "" {
		return errors.New("description must not be empty")
	}
	if len(m.Rows) != len(requiredRowIDs) {
		return fmt.Errorf("rows = %d, want exactly %d authoritative rows", len(m.Rows), len(requiredRowIDs))
	}

	for i, r := range m.Rows {
		if r.ID != requiredRowIDs[i] {
			return fmt.Errorf("rows[%d].id = %q, want %q", i, r.ID, requiredRowIDs[i])
		}
		if err := validateRow(r); err != nil {
			return fmt.Errorf("row %q: %w", r.ID, err)
		}
	}
	return nil
}

func validateRow(r row) error {
	if !strings.HasPrefix(r.Package, "./internal/") || strings.Contains(r.Package, "...") || strings.ContainsAny(r.Package, " \t\r\n") {
		return fmt.Errorf("package %q must name one exact ./internal package", r.Package)
	}
	if !strings.HasPrefix(r.TestRegex, "^") || !strings.HasSuffix(r.TestRegex, "$") {
		return fmt.Errorf("test_regex %q must be fully anchored", r.TestRegex)
	}
	re, err := regexp.Compile(r.TestRegex)
	if err != nil {
		return fmt.Errorf("compile test_regex: %w", err)
	}
	if len(r.ExpectedTests) == 0 {
		return errors.New("expected_tests must not be empty")
	}
	seenTests := make(map[string]struct{}, len(r.ExpectedTests))
	for _, name := range r.ExpectedTests {
		if !strings.HasPrefix(name, "Test") || strings.Contains(name, "/") || !re.MatchString(name) {
			return fmt.Errorf("expected test %q is not a top-level test selected by test_regex", name)
		}
		if _, exists := seenTests[name]; exists {
			return fmt.Errorf("expected test %q is duplicated", name)
		}
		seenTests[name] = struct{}{}
	}
	timeout, err := time.ParseDuration(r.Timeout)
	if err != nil {
		return fmt.Errorf("parse timeout %q: %w", r.Timeout, err)
	}
	if timeout <= 0 || timeout > 5*time.Minute {
		return fmt.Errorf("timeout %s must be greater than zero and no more than 5m", timeout)
	}
	if len(r.ExpectedBehavior) == 0 {
		return errors.New("expected_behavior must not be empty")
	}
	for i, behavior := range r.ExpectedBehavior {
		if strings.TrimSpace(behavior) == "" {
			return fmt.Errorf("expected_behavior[%d] must not be empty", i)
		}
	}
	return nil
}

func discoverTests(root string, r row) ([]string, error) {
	output, err := runCleanGoCombined(root, "test", r.Package, "-list", r.TestRegex)
	if err != nil {
		return nil, fmt.Errorf("list selected tests: %w\n%s", err, output)
	}
	re := regexp.MustCompile(r.TestRegex)
	var tests []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Test") && re.MatchString(line) {
			tests = append(tests, line)
		}
	}
	return tests, nil
}

func compareDiscoveredTests(expected, discovered []string) error {
	want := append([]string(nil), expected...)
	got := append([]string(nil), discovered...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return fmt.Errorf("selected test set drifted: got %v, want %v", got, want)
	}
	return nil
}

func executeRow(root string, r row) error {
	cleaner := filepath.Join(root, "scripts", "with-clean-loom-env.sh")
	args := []string{
		"go", "test", r.Package,
		"-run", r.TestRegex,
		"-count=1",
		"-shuffle=off",
		"-parallel=1",
		"-timeout", r.Timeout,
	}
	cmd := exec.Command(cleaner, args...) //nolint:gosec // Executable is the repo-owned cleaner; schema validation constrains package, regex, and timeout arguments.
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("execute %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func runCleanGoCombined(root string, args ...string) (string, error) {
	cleaner := filepath.Join(root, "scripts", "with-clean-loom-env.sh")
	cmdArgs := append([]string{"go"}, args...)
	cmd := exec.Command(cleaner, cmdArgs...) //nolint:gosec // Executable is the repo-owned cleaner; schema validation constrains package and regex arguments.
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	return string(output), err
}
