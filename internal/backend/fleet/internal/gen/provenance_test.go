package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const eventFixture = `package models
const Create = "issue.create"
const Issue = "issue"
var actions = [...]string{Create}
var actionEntityMap = map[string]string{Create: Issue}
func ValidActions() []string { result := make([]string, len(actions)); copy(result, actions[:]); return result }
`

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := append([]string{"-C", dir, "-c", "user.name=Contract Test", "-c", "user.email=contract@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	output, err := exec.Command("git", command...).CombinedOutput() //nolint:norawexec // Isolated test repository only.
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeTestFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProducerPinAndSemanticDrift(t *testing.T) {
	repo := t.TempDir()
	testGit(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, fleetEventPath), eventFixture)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-qm", "canonical action")
	first := testGit(t, repo, "rev-parse", "HEAD")
	output := filepath.Join(t.TempDir(), generatedFilename)
	options := generationOptions{fleetDB: repo, output: output, revision: first, update: true}
	if err := generateContract(options); err != nil {
		t.Fatal(err)
	}
	options.update, options.revision, options.check = false, "", true
	if err := generateContract(options); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "commit", "--allow-empty", "-qm", "unrelated revision")
	options.check, options.upstream = false, true
	if err := generateContract(options); err != nil {
		t.Fatalf("revision-only drift must pass: %v", err)
	}
	changed := strings.ReplaceAll(eventFixture, "{Create}", "{Create, Update}")
	changed = strings.ReplaceAll(changed, "{Create: Issue}", "{Create: Issue, Update: Issue}") + "\nconst Update = \"issue.update\"\n"
	writeTestFile(t, filepath.Join(repo, fleetEventPath), changed)
	if err := generateContract(options); err != nil {
		t.Fatalf("generation certifies committed objects, not dirty sources: %v", err)
	}
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-qm", "new canonical action")
	if err := generateContract(options); err == nil || !strings.Contains(err.Error(), "contract drift") {
		t.Fatalf("new action must fail upstream check: %v", err)
	}
	options.check, options.upstream = true, false
	if err := generateContract(options); err != nil {
		t.Fatalf("pinned producer must remain reproducible after HEAD changes: %v", err)
	}
	writeTestFile(t, output, "stale consumer")
	if err := generateContract(options); err == nil {
		t.Fatal("stale consumer unexpectedly passed pinned check")
	}
}

func TestProducerManifestRejectsInvalidPins(t *testing.T) {
	base := producerManifest{Version: 1, Repository: "BrowserOperator/fleet-db", Source: fleetEventPath,
		Revision: strings.Repeat("a", 40), ContractSHA256: strings.Repeat("b", 64)}
	for _, revision := range []string{"main", "abc123", strings.Repeat("A", 40), strings.Repeat("a", 39), "--help"} {
		t.Run(revision, func(t *testing.T) {
			manifest := base
			manifest.Revision = revision
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), producerFilename)
			writeTestFile(t, path, string(encoded))
			if _, err := readProducer(path); err == nil {
				t.Fatal("invalid revision accepted")
			}
		})
	}
}

func TestProducerRejectsNonCommitAndHashMismatch(t *testing.T) {
	repo := t.TempDir()
	testGit(t, repo, "init", "-q")
	writeTestFile(t, filepath.Join(repo, fleetEventPath), eventFixture)
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-qm", "canonical action")
	tree := testGit(t, repo, "rev-parse", "HEAD^{tree}")
	if _, err := contractAtRevision(repo, tree); err == nil {
		t.Fatal("tree accepted as pinned producer commit")
	}
	options := generationOptions{fleetDB: repo, output: filepath.Join(t.TempDir(), generatedFilename),
		update: true, revision: testGit(t, repo, "rev-parse", "HEAD")}
	if err := generateContract(options); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(filepath.Dir(options.output), producerFilename)
	manifest, err := readProducer(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ContractSHA256 = strings.Repeat("0", 64)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manifestPath, string(encoded))
	options.update, options.revision, options.check = false, "", true
	if err := generateContract(options); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("wrong manifest hash accepted: %v", err)
	}
}
