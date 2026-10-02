package doctor

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDoctorInventoryEntry(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	result := checkLoomGitInventory(context.Background(), false)
	if result.Status != StatusPass || !strings.Contains(result.Summary, "0 objects") {
		t.Fatalf("doctor entry failed with no journal: %+v", result)
	}
}

func TestDoctorReportsMissingProviderRef(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	remote := filepath.Join(root, "remote.git")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "--bare", "-q", remote) //nolint:norawexec // Real local provider acceptance test.
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := `CREATE TABLE workspace_repos(workspace TEXT,repo TEXT,trunk TEXT,workspace_branch TEXT,base_sha TEXT);
	CREATE TABLE working_areas(workspace TEXT,lead TEXT,repo TEXT,path TEXT,branch TEXT,base_sha TEXT,mode TEXT);
	CREATE TABLE change_revisions(workspace TEXT,change_id TEXT,request_id TEXT,number INTEGER,kind TEXT,operation TEXT,outcome TEXT,base_sha TEXT,head_sha TEXT,tree_hash TEXT,source_head_sha TEXT,derived_from_change TEXT,derived_from_number INTEGER,ready INTEGER,incomplete INTEGER);
	CREATE TABLE change_publications(workspace TEXT,change_id TEXT,repo TEXT,branch TEXT,trunk TEXT,slug TEXT,head_sha TEXT,phase TEXT,pr_number INTEGER,pr_url TEXT);
	CREATE TABLE mirror_refs(repo TEXT,ref TEXT,remote TEXT,sha TEXT,state TEXT,reason TEXT);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if _, err := db.Exec(`INSERT INTO mirror_refs VALUES (?,?,?,?,'mirrored','')`, repo, "refs/loom/ws/W1/change/C1/1/head", remote, sha); err != nil {
		t.Fatal(err)
	}
	result := checkLoomGitInventory(context.Background(), false)
	if result.Status != StatusWarn || !strings.Contains(result.Detail, "integrity_missing") || !strings.Contains(result.Detail, sha) {
		t.Fatalf("doctor omitted provider finding: %+v", result)
	}
}

func TestDoctorWarnsWhenDependencyStatusNotSynced(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := `CREATE TABLE workspace_repos(workspace TEXT,repo TEXT,trunk TEXT,workspace_branch TEXT,base_sha TEXT);
	CREATE TABLE working_areas(workspace TEXT,lead TEXT,repo TEXT,path TEXT,branch TEXT,base_sha TEXT,mode TEXT);
	CREATE TABLE change_revisions(workspace TEXT,change_id TEXT,request_id TEXT,number INTEGER,kind TEXT,operation TEXT,outcome TEXT,base_sha TEXT,head_sha TEXT,tree_hash TEXT,source_head_sha TEXT,derived_from_change TEXT,derived_from_number INTEGER,ready INTEGER,incomplete INTEGER);
	CREATE TABLE change_publications(workspace TEXT,change_id TEXT,repo TEXT,branch TEXT,trunk TEXT,slug TEXT,head_sha TEXT,phase TEXT,pr_number INTEGER,pr_url TEXT);
	CREATE TABLE dependency_checks(workspace TEXT,change_id TEXT,repo TEXT,state TEXT,reason TEXT,synced INTEGER);
	CREATE TABLE dependency_enforcement(repo TEXT,branch TEXT,state TEXT,reason TEXT);
	INSERT INTO dependency_checks VALUES ('W','C2','owner/repo2','pending','Waiting for owner/repo1#1 to land',0);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	result := checkLoomGitInventory(context.Background(), false)
	if result.Status != StatusWarn || !strings.Contains(result.Detail, "dependency status not synced") {
		t.Fatalf("doctor hid an unsynced dependency status: %+v", result)
	}
}

func TestDoctorWarnsOnLandingAttention(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	path := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := `CREATE TABLE workspace_repos(workspace TEXT,repo TEXT,trunk TEXT,workspace_branch TEXT,base_sha TEXT);
	CREATE TABLE working_areas(workspace TEXT,lead TEXT,repo TEXT,path TEXT,branch TEXT,base_sha TEXT,mode TEXT);
	CREATE TABLE change_revisions(workspace TEXT,change_id TEXT,request_id TEXT,number INTEGER,kind TEXT,operation TEXT,outcome TEXT,base_sha TEXT,head_sha TEXT,tree_hash TEXT,source_head_sha TEXT,derived_from_change TEXT,derived_from_number INTEGER,ready INTEGER,incomplete INTEGER);
	CREATE TABLE change_publications(workspace TEXT,change_id TEXT,repo TEXT,branch TEXT,trunk TEXT,slug TEXT,head_sha TEXT,phase TEXT,pr_number INTEGER,pr_url TEXT);
	CREATE TABLE landing_attention(workspace TEXT,change_id TEXT,reason TEXT);
	INSERT INTO change_publications VALUES ('W1','A','repo','loom/ws/W1/change/A','main','owner/repo','` + strings.Repeat("a", 40) + `','done',4,'');
	INSERT INTO landing_attention VALUES ('W1','A','publication mismatch: owned PR 4 does not match published change A');`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	result := checkLoomGitInventory(context.Background(), false)
	if result.Status != StatusWarn || !strings.Contains(result.Detail, "W1/repo A: attention_required") ||
		!strings.Contains(result.Detail, "owned PR 4 does not match published change A") {
		t.Fatalf("doctor hid a landing attention: %+v", result)
	}
}
