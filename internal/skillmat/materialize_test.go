package skillmat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/test/skills-e2e/registry"
)

type skillFixtureFile struct {
	Path       string
	Content    string
	Bytes      []byte
	MediaType  string
	Executable bool
}

// skillFixture preserves the terse shape of the pre-file-tree tests while
// making every successful fixture travel through the public immutable tree
// publication seam. Document is used by import tests that must preserve an
// already-complete SKILL.md byte-for-byte; otherwise Content is the body used
// to build Loom's canonical document.
type skillFixture struct {
	Name        string
	Scope       domain.SkillScope
	RoleName    string
	Description string
	Content     string
	Document    []byte
	Files       []skillFixtureFile
}

type staticSkillStore struct {
	store.SkillStore
	mu     sync.Mutex
	files  store.WorkspaceFileStore
	skills []*skillFixture
	err    error
}

func (s *staticSkillStore) workspaceFiles() store.WorkspaceFileStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = memstore.New().WorkspaceFiles()
	}
	return s.files
}

func (s *staticSkillStore) List(ctx context.Context, workspace string, _ store.SkillFilter) ([]*domain.Skill, error) {
	if s.err != nil {
		return nil, s.err
	}
	files := s.workspaceFiles()
	out := make([]*domain.Skill, 0, len(s.skills))
	for _, fixture := range s.skills {
		skill, err := publishSkillFixture(ctx, files, workspace, fixture)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, nil
}

func publishSkillFixture(ctx context.Context, files store.WorkspaceFileStore, workspace string, fixture *skillFixture) (*domain.Skill, error) {
	bundled := make([]domain.SkillFileTreeFile, 0, len(fixture.Files))
	for _, file := range fixture.Files {
		body := append([]byte(nil), file.Bytes...)
		if body == nil {
			body = []byte(file.Content)
		}
		bundled = append(bundled, domain.SkillFileTreeFile{
			Path: file.Path, Bytes: body, MediaType: file.MediaType, Executable: file.Executable,
		})
	}
	var (
		snapshot *domain.SkillFileTreeSnapshot
		err      error
	)
	if fixture.Document != nil {
		snapshot, err = domain.ValidateSkillFileTree(append(bundled, domain.SkillFileTreeFile{
			Path: domain.SkillFileNameSKILLMD, Bytes: append([]byte(nil), fixture.Document...), MediaType: "text/markdown",
		}))
	} else {
		snapshot, err = domain.BuildSkillFileTree(fixture.Name, fixture.Description, []byte(fixture.Content), bundled)
	}
	if err != nil {
		return nil, err
	}
	inputs := make([]domain.WorkspaceFileInput, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		inputs = append(inputs, domain.WorkspaceFileInput(file))
	}
	published, err := files.Publish(ctx, workspace, inputs)
	if err != nil {
		return nil, err
	}
	return &domain.Skill{
		WorkspaceKey: workspace, Name: fixture.Name, Scope: fixture.Scope, RoleName: fixture.RoleName,
		Description: fixture.Description, FileTreeRevision: published.Tree.Revision,
	}, nil
}

type materializeStore struct {
	store.Store
	skills *staticSkillStore
	files  store.WorkspaceFileStore
}

func (s materializeStore) Skills() store.SkillStore { return s.skills }
func (s materializeStore) WorkspaceFiles() store.WorkspaceFileStore {
	if s.files != nil {
		return s.files
	}
	if s.skills == nil {
		return nil
	}
	return s.skills.workspaceFiles()
}

type failingWorkspaceFileStore struct {
	store.WorkspaceFileStore
	getTreeErr  error
	downloadErr error
}

func (s failingWorkspaceFileStore) GetTree(ctx context.Context, workspace, revision string) (*domain.WorkspaceFileTree, error) {
	if s.getTreeErr != nil {
		return nil, s.getTreeErr
	}
	return s.WorkspaceFileStore.GetTree(ctx, workspace, revision)
}

func (s failingWorkspaceFileStore) Download(ctx context.Context, workspace, revision, filePath string) ([]byte, error) {
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	return s.WorkspaceFileStore.Download(ctx, workspace, revision, filePath)
}

type markerRecordingRoot struct {
	secureRoot
	created []string
	renamed [][2]string
}

func (r *markerRecordingRoot) CreateFile(name string, _ []byte, _ os.FileMode) error {
	r.created = append(r.created, name)
	return nil
}

func (r *markerRecordingRoot) CopyFile(sourceName, destinationName string, perm os.FileMode, maxBytes int64) (int64, error) {
	return r.secureRoot.CopyFile(sourceName, destinationName, perm, maxBytes)
}

func (r *markerRecordingRoot) Rename(oldName, newName string) error {
	r.renamed = append(r.renamed, [2]string{oldName, newName})
	return nil
}

func (r *markerRecordingRoot) Swap(firstName, secondName string) error {
	r.renamed = append(r.renamed, [2]string{firstName, secondName})
	return nil
}

func (r *markerRecordingRoot) Lock(ctx context.Context, name string) (io.Closer, error) {
	return r.secureRoot.Lock(ctx, name)
}

func (r *markerRecordingRoot) Remove(string) error { return nil }

type failNthMutationRoot struct {
	secureRoot
	failAt             int
	persistent         bool
	mutations          int
	faultTriggered     bool
	exchangeCommitted  bool
	exchangeMutation   int
	cleanupAfterCommit bool
}

func (r *failNthMutationRoot) fail() error {
	r.mutations++
	if r.failAt > 0 && r.mutations >= r.failAt && (r.persistent || !r.faultTriggered) {
		r.faultTriggered = true
		return fmt.Errorf("injected filesystem failure at operation %d", r.mutations)
	}
	return nil
}

func (r *failNthMutationRoot) MkdirAll(name string, perm os.FileMode) error {
	if err := r.fail(); err != nil {
		return err
	}
	return r.secureRoot.MkdirAll(name, perm)
}

func (r *failNthMutationRoot) CreateFile(name string, content []byte, perm os.FileMode) error {
	if err := r.fail(); err != nil {
		return err
	}
	return r.secureRoot.CreateFile(name, content, perm)
}

func (r *failNthMutationRoot) CopyFile(sourceName, destinationName string, perm os.FileMode, maxBytes int64) (int64, error) {
	if err := r.fail(); err != nil {
		if r.exchangeCommitted {
			r.cleanupAfterCommit = true
		}
		return 0, err
	}
	return r.secureRoot.CopyFile(sourceName, destinationName, perm, maxBytes)
}

func (r *failNthMutationRoot) Symlink(target, name string) error {
	if err := r.fail(); err != nil {
		return err
	}
	return r.secureRoot.Symlink(target, name)
}

func (r *failNthMutationRoot) Rename(oldName, newName string) error {
	if err := r.fail(); err != nil {
		return err
	}
	if err := r.secureRoot.Rename(oldName, newName); err != nil {
		return err
	}
	if newName == projectionCurrentPath {
		r.exchangeCommitted = true
		r.exchangeMutation = r.mutations
	}
	return nil
}

func (r *failNthMutationRoot) Swap(firstName, secondName string) error {
	if err := r.fail(); err != nil {
		return err
	}
	if err := r.secureRoot.Swap(firstName, secondName); err != nil {
		return err
	}
	r.exchangeCommitted = true
	r.exchangeMutation = r.mutations
	return nil
}

func (r *failNthMutationRoot) Remove(name string) error {
	if err := r.fail(); err != nil {
		if r.exchangeCommitted {
			r.cleanupAfterCommit = true
		}
		return err
	}
	return r.secureRoot.Remove(name)
}

func (r *failNthMutationRoot) RemoveDir(name string) error {
	if err := r.fail(); err != nil {
		if r.exchangeCommitted {
			r.cleanupAfterCommit = true
		}
		return err
	}
	return r.secureRoot.RemoveDir(name)
}

func (r *failNthMutationRoot) Lock(ctx context.Context, name string) (io.Closer, error) {
	return r.secureRoot.Lock(ctx, name)
}

func TestMaterializeOneShotFailuresExposeOnlyPreviousOrCommittedGeneration(t *testing.T) {
	countTarget := t.TempDir()
	countSkill, countStore := atomicityFixture()
	mustMaterialize(t, countStore, countTarget, "operation-count initial Materialize")
	updateAtomicityFixture(countSkill)
	var counter *failNthMutationRoot
	err := materializeWithRootOpener(t.Context(), countStore, "WS", "lead", countTarget, func(rootPath string) (secureRoot, error) {
		root, openErr := openSecureRoot(rootPath)
		if openErr != nil {
			return nil, openErr
		}
		counter = &failNthMutationRoot{secureRoot: root}
		return counter, nil
	})
	if err != nil {
		t.Fatalf("count generation update operations: %v", err)
	}
	if counter == nil || counter.mutations == 0 {
		t.Fatal("generation update performed no injectable filesystem mutations")
	}
	wantCommitted := snapshotMaterializedProjection(t, countTarget)

	postCommitCleanupFaults := 0
	for failAt := 1; failAt <= counter.mutations; failAt++ {
		t.Run(fmt.Sprintf("operation-%02d", failAt), func(t *testing.T) {
			target := t.TempDir()
			skill, st := atomicityFixture()
			mustMaterialize(t, st, target, "initial Materialize")
			want := snapshotMaterializedProjection(t, target)
			updateAtomicityFixture(skill)

			var injected *failNthMutationRoot
			err := materializeWithRootOpener(t.Context(), st, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
				root, openErr := openSecureRoot(rootPath)
				if openErr != nil {
					return nil, openErr
				}
				injected = &failNthMutationRoot{secureRoot: root, failAt: failAt}
				return injected, nil
			})
			if err == nil || !strings.Contains(err.Error(), "injected filesystem failure") {
				t.Fatalf("Materialize error = %v, want injected filesystem failure", err)
			}
			if injected == nil || !injected.faultTriggered {
				t.Fatalf("fault handshake = %#v, want operation %d to fail", injected, failAt)
			}
			wantAfterFailure := want
			if injected.exchangeCommitted {
				postCommitCleanupFaults++
				wantAfterFailure = wantCommitted
			}
			if got := snapshotMaterializedProjection(t, target); !reflect.DeepEqual(got, wantAfterFailure) {
				t.Fatalf("failure exposed a partial generation:\n got: %#v\nwant: %#v", got, wantAfterFailure)
			}
		})
	}
	if postCommitCleanupFaults == 0 {
		t.Fatal("fault matrix never exercised cleanup after a committed exchange")
	}
}

func TestMaterializePersistentFailureLeavesOldGenerationForRecovery(t *testing.T) {
	target := t.TempDir()
	skill, st := atomicityFixture()
	mustMaterialize(t, st, target, "initial Materialize")
	markerFile := filepath.Join(target, filepath.FromSlash(markerPath))
	oldMarker, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("read old marker: %v", err)
	}
	updateAtomicityFixture(skill)

	err = materializeWithRootOpener(t.Context(), st, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
		root, openErr := openSecureRoot(rootPath)
		if openErr != nil {
			return nil, openErr
		}
		return &failNthMutationRoot{secureRoot: root, failAt: 7, persistent: true}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "operation 7") {
		t.Fatalf("Materialize error = %v, want injected staging failure", err)
	}
	afterFailureMarker, readErr := os.ReadFile(markerFile)
	if readErr != nil || !bytes.Equal(afterFailureMarker, oldMarker) {
		t.Fatalf("marker advanced after failed staging: content=%q err=%v", afterFailureMarker, readErr)
	}

	mustMaterialize(t, st, target, "recovery Materialize")
	newMarker, readErr := os.ReadFile(markerFile)
	if readErr != nil || bytes.Equal(newMarker, oldMarker) {
		t.Fatalf("recovery marker = %q, err=%v, want new projection marker", newMarker, readErr)
	}
	for relative, want := range map[string]string{
		"SKILL.md":           "---\nname: alpha\ndescription: alpha\n---\nnew body\n",
		"references/add.md":  "new add\n",
		"references/keep.md": "new keep\n",
	} {
		got, readErr := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", filepath.FromSlash(relative)))
		if readErr != nil || string(got) != want {
			t.Fatalf("recovered %s = %q, err=%v, want %q", relative, got, readErr, want)
		}
	}
	removed := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "references", "remove.md")
	if _, statErr := os.Stat(removed); !os.IsNotExist(statErr) {
		t.Fatalf("removed old path survived recovery: %v", statErr)
	}
}

type blockingGenerationRoot struct {
	secureRoot
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *blockingGenerationRoot) MkdirAll(name string, perm os.FileMode) error {
	if strings.HasPrefix(name, projectionGenerationsDir+"/") {
		r.once.Do(func() {
			close(r.entered)
			<-r.release
		})
	}
	return r.secureRoot.MkdirAll(name, perm)
}

type lockAttemptRoot struct {
	secureRoot
	attempted chan struct{}
	once      sync.Once
}

func (r *lockAttemptRoot) Lock(ctx context.Context, name string) (io.Closer, error) {
	r.once.Do(func() { close(r.attempted) })
	return r.secureRoot.Lock(ctx, name)
}

func TestMaterializeSerializesOverlappingWritersForOneTarget(t *testing.T) {
	target := t.TempDir()
	initialSkill, initialStore := atomicityFixture()
	mustMaterialize(t, initialStore, target, "initial Materialize")

	firstSkill := *initialSkill
	updateAtomicityFixture(&firstSkill)
	firstSkill.Content = "writer one\n"
	firstStore := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{&firstSkill}}}
	secondSkill := firstSkill
	secondSkill.Content = "writer two\n"
	secondSkill.Files = []skillFixtureFile{{Path: "references/keep.md", Content: "writer two keep\n"}}
	secondStore := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{&secondSkill}}}

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- materializeWithRootOpener(t.Context(), firstStore, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
			root, err := openSecureRoot(rootPath)
			if err != nil {
				return nil, err
			}
			return &blockingGenerationRoot{secureRoot: root, entered: entered, release: release}, nil
		})
	}()
	<-entered

	attempted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- materializeWithRootOpener(t.Context(), secondStore, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
			root, err := openSecureRoot(rootPath)
			if err != nil {
				return nil, err
			}
			return &lockAttemptRoot{secureRoot: root, attempted: attempted}, nil
		})
	}()
	<-attempted
	select {
	case err := <-secondDone:
		t.Fatalf("second writer passed target lock while first was staging: %v", err)
	default:
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first writer: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second writer: %v", err)
	}

	want := "---\nname: alpha\ndescription: alpha\n---\nwriter two\n"
	got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"))
	if err != nil || string(got) != want {
		t.Fatalf("final serialized projection = %q, err=%v, want second writer", got, err)
	}
	assertNoRetainedGenerations(t, target)
}

func TestMaterializeFailuresNeverReplaceCurrentProjection(t *testing.T) {
	registry.MarkEvidence(t, 70)

	t.Run("integrity failure", func(t *testing.T) {
		target := t.TempDir()
		skill, st := atomicityFixture()
		mustMaterialize(t, st, target, "initial Materialize")
		want := snapshotMaterializedProjection(t, target)
		updateAtomicityFixture(skill)
		st.files = failingWorkspaceFileStore{WorkspaceFileStore: st.skills.workspaceFiles(), downloadErr: domain.ErrIntegrity}
		if err := materialize(t.Context(), st, "WS", "lead", target); !errors.Is(err, domain.ErrIntegrity) {
			t.Fatalf("Materialize error = %v, want integrity failure", err)
		}
		if got := snapshotMaterializedProjection(t, target); !reflect.DeepEqual(got, want) {
			t.Fatalf("integrity failure changed the current projection:\n got: %#v\nwant: %#v", got, want)
		}
	})

	t.Run("path failure", func(t *testing.T) {
		target := t.TempDir()
		skill, st := atomicityFixture()
		mustMaterialize(t, st, target, "initial Materialize")
		want := snapshotMaterializedProjection(t, target)
		skill.Files = append(skill.Files, skillFixtureFile{Path: "../escape", Content: "nope"})
		if err := materialize(t.Context(), st, "WS", "lead", target); err == nil {
			t.Fatal("Materialize error = nil, want path failure")
		}
		if got := snapshotMaterializedProjection(t, target); !reflect.DeepEqual(got, want) {
			t.Fatalf("path failure changed the current projection:\n got: %#v\nwant: %#v", got, want)
		}
	})

	t.Run("collision failure", func(t *testing.T) {
		target := t.TempDir()
		skill, st := atomicityFixture()
		mustMaterialize(t, st, target, "initial Materialize")
		unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "user.md")
		if err := os.WriteFile(unrecorded, []byte("user owned\n"), 0o644); err != nil {
			t.Fatalf("write unrecorded collision: %v", err)
		}
		want := snapshotMaterializedProjection(t, target)
		skill.Files = append(skill.Files, skillFixtureFile{Path: "user.md", Content: "managed\n"})
		if err := materialize(t.Context(), st, "WS", "lead", target); err == nil || !strings.Contains(err.Error(), "unrecorded") {
			t.Fatalf("Materialize error = %v, want unrecorded collision", err)
		}
		if got := snapshotMaterializedProjection(t, target); !reflect.DeepEqual(got, want) {
			t.Fatalf("collision failure changed the current projection:\n got: %#v\nwant: %#v", got, want)
		}
	})

	countTarget := t.TempDir()
	countSkill, countStore := atomicityFixture()
	mustMaterialize(t, countStore, countTarget, "operation-count initial Materialize")
	updateAtomicityFixture(countSkill)
	var counter *failNthMutationRoot
	if err := materializeWithRootOpener(t.Context(), countStore, "WS", "lead", countTarget, func(rootPath string) (secureRoot, error) {
		root, openErr := openSecureRoot(rootPath)
		if openErr != nil {
			return nil, openErr
		}
		counter = &failNthMutationRoot{secureRoot: root}
		return counter, nil
	}); err != nil {
		t.Fatalf("count generation update operations: %v", err)
	}
	if counter == nil || counter.mutations == 0 {
		t.Fatal("generation update performed no injectable filesystem mutations")
	}
	wantCommitted := snapshotMaterializedProjection(t, countTarget)

	for failAt := 1; failAt <= counter.mutations; failAt++ {
		t.Run(fmt.Sprintf("persistent-update-operation-%02d", failAt), func(t *testing.T) {
			target := t.TempDir()
			skill, st := atomicityFixture()
			mustMaterialize(t, st, target, "initial Materialize")
			want := snapshotMaterializedProjection(t, target)
			updateAtomicityFixture(skill)

			var injected *failNthMutationRoot
			err := materializeWithRootOpener(t.Context(), st, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
				root, openErr := openSecureRoot(rootPath)
				if openErr != nil {
					return nil, openErr
				}
				injected = &failNthMutationRoot{secureRoot: root, failAt: failAt, persistent: true}
				return injected, nil
			})
			if err == nil || !strings.Contains(err.Error(), "injected filesystem failure") {
				t.Fatalf("Materialize error = %v, want injected filesystem failure", err)
			}
			if injected == nil || !injected.faultTriggered {
				t.Fatalf("fault handshake = %#v, want operation %d to fail", injected, failAt)
			}
			wantAfterFailure := want
			if injected.exchangeCommitted {
				wantAfterFailure = wantCommitted
			}
			if got := snapshotMaterializedProjection(t, target); !reflect.DeepEqual(got, wantAfterFailure) {
				t.Fatalf("persistent failure exposed a partial generation:\n got: %#v\nwant: %#v", got, wantAfterFailure)
			}
		})
	}

	initialCountTarget := t.TempDir()
	_, initialCountStore := atomicityFixture()
	var initialCounter *failNthMutationRoot
	if err := materializeWithRootOpener(t.Context(), initialCountStore, "WS", "lead", initialCountTarget, func(rootPath string) (secureRoot, error) {
		root, openErr := openSecureRoot(rootPath)
		if openErr != nil {
			return nil, openErr
		}
		initialCounter = &failNthMutationRoot{secureRoot: root}
		return initialCounter, nil
	}); err != nil {
		t.Fatalf("count initial projection operations: %v", err)
	}
	for failAt := 1; failAt <= initialCounter.mutations; failAt++ {
		t.Run(fmt.Sprintf("persistent-initial-operation-%02d", failAt), func(t *testing.T) {
			target := t.TempDir()
			_, st := atomicityFixture()
			var injected *failNthMutationRoot
			err := materializeWithRootOpener(t.Context(), st, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
				root, openErr := openSecureRoot(rootPath)
				if openErr != nil {
					return nil, openErr
				}
				injected = &failNthMutationRoot{secureRoot: root, failAt: failAt, persistent: true}
				return injected, nil
			})
			if err == nil || injected == nil || !injected.faultTriggered {
				t.Fatalf("Materialize error = %v, fault=%#v, want activated persistent failure", err, injected)
			}
			for _, publicPath := range []string{
				filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"),
				filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha", "SKILL.md"),
			} {
				if _, statErr := os.Stat(publicPath); !os.IsNotExist(statErr) {
					t.Fatalf("failed initial projection exposed %q: %v", publicPath, statErr)
				}
			}
		})
	}
}

func TestMaterializeBoundsAndReclaimsRecoveryGenerations(t *testing.T) {
	countTarget := t.TempDir()
	countSkill, countStore := atomicityFixture()
	mustMaterialize(t, countStore, countTarget, "count initial Materialize")
	updateAtomicityFixture(countSkill)
	var counter *failNthMutationRoot
	if err := materializeWithRootOpener(t.Context(), countStore, "WS", "lead", countTarget, func(rootPath string) (secureRoot, error) {
		root, err := openSecureRoot(rootPath)
		if err != nil {
			return nil, err
		}
		counter = &failNthMutationRoot{secureRoot: root}
		return counter, nil
	}); err != nil {
		t.Fatalf("count update operations: %v", err)
	}
	if counter.exchangeMutation == 0 || counter.exchangeMutation >= counter.mutations {
		t.Fatalf("exchange mutation = %d of %d, want post-commit cleanup boundary", counter.exchangeMutation, counter.mutations)
	}
	assertNoRetainedGenerations(t, countTarget)

	target := t.TempDir()
	skill, st := atomicityFixture()
	mustMaterialize(t, st, target, "initial Materialize")
	updateAtomicityFixture(skill)
	var injected *failNthMutationRoot
	err := materializeWithRootOpener(t.Context(), st, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
		root, openErr := openSecureRoot(rootPath)
		if openErr != nil {
			return nil, openErr
		}
		injected = &failNthMutationRoot{secureRoot: root, failAt: counter.exchangeMutation + 1, persistent: true}
		return injected, nil
	})
	if err == nil || !strings.Contains(err.Error(), "committed but remove displaced generation") || !injected.exchangeCommitted {
		t.Fatalf("post-commit cleanup error = %v, injected=%#v", err, injected)
	}
	if got := retainedGenerationCount(t, target); got != 1 {
		t.Fatalf("retained generations after cleanup failure = %d, want 1", got)
	}
	mustMaterialize(t, st, target, "cleanup recovery Materialize")
	assertNoRetainedGenerations(t, target)

	// A persistent failure while recovering the one abandoned generation must
	// stop before allocating another, so repeated retries remain bounded.
	for attempt := 1; attempt <= 3; attempt++ {
		abandoned := filepath.Join(target, filepath.FromSlash(projectionGenerationsDir), fmt.Sprintf("%024x", attempt))
		if err := os.MkdirAll(abandoned, 0o700); err != nil {
			t.Fatalf("plant abandoned generation %d: %v", attempt, err)
		}
		if err := os.WriteFile(filepath.Join(abandoned, "leftover"), []byte("x"), 0o600); err != nil {
			t.Fatalf("plant abandoned file %d: %v", attempt, err)
		}
		var retry *failNthMutationRoot
		err := materializeWithRootOpener(t.Context(), st, "WS", "lead", target, func(rootPath string) (secureRoot, error) {
			root, openErr := openSecureRoot(rootPath)
			if openErr != nil {
				return nil, openErr
			}
			retry = &failNthMutationRoot{secureRoot: root, failAt: 2, persistent: true}
			return retry, nil
		})
		if err == nil {
			t.Fatalf("persistent recovery attempt %d unexpectedly succeeded", attempt)
		}
		if got := retainedGenerationCount(t, target); got > 1 {
			t.Fatalf("persistent recovery attempt %d retained %d generations, want at most 1", attempt, got)
		}
		mustMaterialize(t, st, target, fmt.Sprintf("recover abandoned generation %d", attempt))
		assertNoRetainedGenerations(t, target)
	}
}

func TestMaterializeRecoveryRemovesAtMostOneGenerationPerInvocation(t *testing.T) {
	target := t.TempDir()
	skill, st := atomicityFixture()
	mustMaterialize(t, st, target, "initial Materialize")
	want := snapshotMaterializedProjection(t, target)
	for i := 1; i <= 2; i++ {
		abandoned := filepath.Join(target, filepath.FromSlash(projectionGenerationsDir), fmt.Sprintf("%024x", i))
		if err := os.MkdirAll(abandoned, 0o700); err != nil {
			t.Fatalf("plant abandoned generation %d: %v", i, err)
		}
		if err := os.WriteFile(filepath.Join(abandoned, "leftover"), []byte("x"), 0o600); err != nil {
			t.Fatalf("plant abandoned file %d: %v", i, err)
		}
	}
	updateAtomicityFixture(skill)

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "additional abandoned") {
		t.Fatalf("Materialize error = %v, want bounded recovery refusal", err)
	}
	if got := retainedGenerationCount(t, target); got != 1 {
		t.Fatalf("retained generations after one recovery pass = %d, want 1", got)
	}
	if got := snapshotMaterializedProjection(t, target); !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded recovery changed the current projection:\n got: %#v\nwant: %#v", got, want)
	}

	mustMaterialize(t, st, target, "second bounded recovery Materialize")
	assertNoRetainedGenerations(t, target)
	got, readErr := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"))
	if readErr != nil || !strings.Contains(string(got), "new body") {
		t.Fatalf("post-recovery projection = %q, err=%v, want updated generation", got, readErr)
	}
}

func retainedGenerationCount(t *testing.T, target string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(target, filepath.FromSlash(projectionGenerationsDir)))
	if err != nil {
		t.Fatalf("read retained generations: %v", err)
	}
	return len(entries)
}

func assertNoRetainedGenerations(t *testing.T, target string) {
	t.Helper()
	if got := retainedGenerationCount(t, target); got != 0 {
		t.Fatalf("retained generations = %d, want 0", got)
	}
}

func TestMaterializeRejectsOversizedSparseUnrecordedFileBeforeExchange(t *testing.T) {
	target := t.TempDir()
	skill, st := atomicityFixture()
	mustMaterialize(t, st, target, "initial Materialize")
	markerFile := filepath.Join(target, filepath.FromSlash(markerPath))
	oldMarker, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("read old marker: %v", err)
	}
	sparse := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "large.user")
	file, err := os.OpenFile(sparse, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create sparse unrecorded file: %v", err)
	}
	if err := file.Truncate(maxPreservedProjectionFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("size sparse unrecorded file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close sparse unrecorded file: %v", err)
	}
	updateAtomicityFixture(skill)

	err = materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "exceeds clone limit") {
		t.Fatalf("Materialize error = %v, want bounded clone refusal", err)
	}
	afterMarker, readErr := os.ReadFile(markerFile)
	if readErr != nil || !bytes.Equal(afterMarker, oldMarker) {
		t.Fatalf("marker changed after bounded clone refusal: content=%q err=%v", afterMarker, readErr)
	}
	info, statErr := os.Stat(sparse)
	if statErr != nil || info.Size() != maxPreservedProjectionFileBytes+1 {
		t.Fatalf("sparse unrecorded file changed: info=%#v err=%v", info, statErr)
	}
	assertNoRetainedGenerations(t, target)
}

type cloneLimitRoot struct {
	secureRoot
	files  map[string]int64
	limits []int64
}

func (r *cloneLimitRoot) ReadDir(name string) ([]string, error) {
	if name == path.Join("current", AgentsSkillsDir) {
		names := make([]string, 0, len(r.files))
		for file := range r.files {
			names = append(names, path.Base(file))
		}
		return names, nil
	}
	if name == path.Join("current", ClaudeSkillsDir) {
		return nil, nil
	}
	return nil, fs.ErrNotExist
}

func (r *cloneLimitRoot) Lstat(name string) (securePathInfo, error) {
	size, ok := r.files[name]
	if !ok {
		return securePathInfo{}, fs.ErrNotExist
	}
	return securePathInfo{Mode: 0o644, Size: size}, nil
}

func (r *cloneLimitRoot) MkdirAll(string, os.FileMode) error { return nil }

func (r *cloneLimitRoot) CopyFile(sourceName, _ string, _ os.FileMode, maxBytes int64) (int64, error) {
	r.limits = append(r.limits, maxBytes)
	size := r.files[sourceName]
	if size > maxBytes {
		return 0, fmt.Errorf("%s exceeds clone limit of %d bytes", sourceName, maxBytes)
	}
	return size, nil
}

func TestCloneGenerationEnforcesAggregateStreamingLimit(t *testing.T) {
	root := &cloneLimitRoot{files: make(map[string]int64)}
	fileSize := int64(maxPreservedProjectionFileBytes / 2)
	for i := 0; i < 9; i++ {
		name := path.Join("current", AgentsSkillsDir, fmt.Sprintf("unrecorded-%d", i))
		root.files[name] = fileSize
	}
	err := cloneGeneration(root, "current", "staged")
	if err == nil || !strings.Contains(err.Error(), "exceeds clone limit") {
		t.Fatalf("cloneGeneration error = %v, want aggregate bound", err)
	}
	for _, limit := range root.limits {
		if limit > maxPreservedProjectionFileBytes {
			t.Fatalf("per-file clone limit = %d, want at most %d", limit, maxPreservedProjectionFileBytes)
		}
	}
}

func atomicityFixture() (*skillFixture, materializeStore) {
	skill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "old body\n",
		Files: []skillFixtureFile{{Path: "references/keep.md", Content: "old keep\n"}, {Path: "references/remove.md", Content: "old remove\n"}},
	}
	return skill, materializeStore{skills: &staticSkillStore{skills: []*skillFixture{skill}}}
}

func updateAtomicityFixture(skill *skillFixture) {
	skill.Content = "new body\n"
	skill.Files = []skillFixtureFile{{Path: "references/keep.md", Content: "new keep\n"}, {Path: "references/add.md", Content: "new add\n"}}
}

func TestMaterializeResolvesRoleSkillAndWritesAgentLayout(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{
		{
			Name:        "alpha",
			Scope:       domain.SkillScopeWorkspace,
			Description: "workspace skill",
			Content:     "Workspace body\n",
		},
		{
			Name:        "alpha",
			Scope:       domain.SkillScopeRole,
			RoleName:    "lead",
			Description: "role skill",
			Content:     "Role body\n",
			Files: []skillFixtureFile{{
				Path:       "scripts/run.sh",
				Content:    "#!/bin/sh\necho ok\n",
				Executable: true,
			}},
		},
	}}}

	mustMaterialize(t, st, target, "Materialize")

	skillMDPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	got, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	want := "---\nname: alpha\ndescription: role skill\n---\nRole body\n"
	if string(got) != want {
		t.Fatalf("SKILL.md = %q, want %q", got, want)
	}

	scriptPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "scripts", "run.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("script mode = %v, want executable", info.Mode().Perm())
	}

	linkPath := filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha")
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("read Claude skill link: %v", err)
	}
	if linkTarget != "../../.agents/skills/alpha" {
		t.Fatalf("Claude skill link = %q, want relative canonical target", linkTarget)
	}

	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		".agents/skills/INDEX.md",
		".agents/skills/alpha/SKILL.md",
		".agents/skills/alpha/scripts/run.sh",
		".agents/skills/loom-skill-catalog/SKILL.md",
		".claude/skills/alpha",
		".claude/skills/loom-skill-catalog",
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
	if gotMarker.Hash == "" {
		t.Fatal("marker hash is empty")
	}
}

func TestMaterializePublishesBothSkillViewsThroughOneCurrentGeneration(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "initial Materialize")

	for _, alias := range []struct{ name, target string }{
		{AgentsSkillsDir, agentsSkillsAliasTarget},
		{ClaudeSkillsDir, claudeSkillsAliasTarget},
	} {
		got, err := os.Readlink(filepath.Join(target, filepath.FromSlash(alias.name)))
		if err != nil || got != alias.target {
			t.Fatalf("projection alias %s = %q, err=%v, want %q", alias.name, got, err, alias.target)
		}
	}
	current, err := os.Stat(filepath.Join(target, filepath.FromSlash(projectionCurrentPath)))
	if err != nil || !current.IsDir() {
		t.Fatalf("current projection = %#v, err=%v, want directory", current, err)
	}
	compatibility, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha"))
	if err != nil || compatibility != "../../.agents/skills/alpha" {
		t.Fatalf("Claude compatibility link = %q, err=%v", compatibility, err)
	}
}

func TestMaterializePreservesImportedSkillDocumentAndRawExecutableBytes(t *testing.T) {
	target := t.TempDir()
	document := []byte("---\ndescription: imported exact\nname: imported\nx-vendor: keep\n---\r\nImported body\r\n")
	archive := []byte{'P', 'K', 0x03, 0x04, 0x00, 0xff, 0x80, '\n'}
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "imported", Scope: domain.SkillScopeWorkspace, Description: "imported exact", Document: document,
		Files: []skillFixtureFile{{Path: "Archive.zip", Bytes: archive, MediaType: "application/zip", Executable: true}},
	}}}}

	mustMaterialize(t, st, target, "Materialize imported tree")
	skillDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "imported")
	gotDocument, err := os.ReadFile(filepath.Join(skillDir, domain.SkillFileNameSKILLMD))
	if err != nil || !bytes.Equal(gotDocument, document) {
		t.Fatalf("imported SKILL.md = %q, err=%v, want exact bytes %q", gotDocument, err, document)
	}
	archivePath := filepath.Join(skillDir, "Archive.zip")
	gotArchive, err := os.ReadFile(archivePath)
	if err != nil || !bytes.Equal(gotArchive, archive) {
		t.Fatalf("Archive.zip = %v, err=%v, want raw bytes %v", gotArchive, err, archive)
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("stat Archive.zip: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("Archive.zip mode = %o, want 755", info.Mode().Perm())
	}
}

//nolint:funlen // The test verifies the complete synthetic catalog projection in one fixture.
func TestMaterializeWritesLiveSkillCatalog(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{
		{Name: "zulu", Scope: domain.SkillScopeWorkspace, Description: "Zulu skill", Content: "zulu\n"},
		{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "Alpha skill", Content: "alpha\n"},
	}}}

	mustMaterialize(t, st, target, "Materialize")

	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	wantIndex := "# Loom skills — live catalog\n" +
		"\n" +
		"Current as of the last turn boundary. Loom rewrites this file when skills\n" +
		"change; it supersedes any skill list captured at session start.\n" +
		"\n" +
		"- **alpha** — Alpha skill → read `.agents/skills/alpha/SKILL.md`\n" +
		"- **loom-skill-catalog** — Read this before listing or choosing skills. Loom adds/removes skills between turns; a session-start skill list may be stale. The live catalog is .agents/skills/INDEX.md. → read `.agents/skills/loom-skill-catalog/SKILL.md`\n" +
		"- **zulu** — Zulu skill → read `.agents/skills/zulu/SKILL.md`\n"
	if string(index) != wantIndex {
		t.Fatalf("INDEX.md = %q, want %q", index, wantIndex)
	}

	catalogPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), catalogSkillName, domain.SkillFileNameSKILLMD)
	catalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog SKILL.md: %v", err)
	}
	wantCatalog := "---\n" +
		"name: loom-skill-catalog\n" +
		"description: Read this before listing or choosing skills. Loom adds/removes skills between turns; a session-start skill list may be stale. The live catalog is .agents/skills/INDEX.md.\n" +
		"---\n" +
		"Loom manages the skills in this directory centrally. The set can change\n" +
		"between your turns: skills are added, updated, and removed while your\n" +
		"session is running.\n" +
		"\n" +
		"- The authoritative, always-current catalog: `.agents/skills/INDEX.md`.\n" +
		"- Any skill list captured at session start may be stale; prefer INDEX.md.\n" +
		"- To use a skill, read `.agents/skills/<name>/SKILL.md` and follow it.\n"
	if string(catalog) != wantCatalog {
		t.Fatalf("catalog SKILL.md = %q, want %q", catalog, wantCatalog)
	}
	info, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatalf("stat catalog SKILL.md: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("catalog SKILL.md mode = %o, want 644", info.Mode().Perm())
	}
	linkTarget, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), catalogSkillName))
	if err != nil {
		t.Fatalf("read catalog compatibility link: %v", err)
	}
	if linkTarget != "../../.agents/skills/loom-skill-catalog" {
		t.Fatalf("catalog compatibility link = %q", linkTarget)
	}
}

func TestMaterializePreservesAngleBracketsInDescription(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	description := "Use React's <ViewTransition> component"
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "view-transitions", Scope: domain.SkillScopeWorkspace,
		Description: description, Content: "body\n",
	}}}}

	mustMaterialize(t, st, target, "Materialize")

	skillDocument, err := os.ReadFile(filepath.Join(
		target, filepath.FromSlash(AgentsSkillsDir), "view-transitions", domain.SkillFileNameSKILLMD,
	))
	if err != nil {
		t.Fatalf("read materialized SKILL.md: %v", err)
	}
	if !strings.Contains(string(skillDocument), "description: "+description+"\n") {
		t.Fatalf("materialized SKILL.md did not preserve description: %q", skillDocument)
	}

	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	if !strings.Contains(string(index), "**view-transitions** — "+description+" → read") {
		t.Fatalf("INDEX.md did not preserve description: %q", index)
	}
}

func TestMaterializeCatalogAnnotatesShadowedSkill(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{
		{Name: "review", Scope: domain.SkillScopeWorkspace, Description: "workspace review", Content: "workspace\n"},
		{Name: "review", Scope: domain.SkillScopeRole, RoleName: "lead", Description: "lead review", Content: "lead\n"},
	}}}

	mustMaterialize(t, st, target, "Materialize")
	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	wantLine := "- **review** — lead review (overrides the workspace skill of the same name) → read `.agents/skills/review/SKILL.md`\n"
	if !strings.Contains(string(index), wantLine) {
		t.Fatalf("INDEX.md = %q, want shadow annotation %q", index, wantLine)
	}
	if strings.Contains(string(index), "workspace review") {
		t.Fatalf("INDEX.md includes shadowed workspace description: %q", index)
	}
}

func TestMaterializeRewritesCatalogAfterSkillRemoval(t *testing.T) {
	target := t.TempDir()
	alpha := &skillFixture{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "Alpha skill", Content: "alpha\n"}
	beta := &skillFixture{Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "Beta skill", Content: "beta\n"}
	skills := &staticSkillStore{skills: []*skillFixture{alpha, beta}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	skills.skills = []*skillFixture{alpha}
	mustMaterialize(t, st, target, "Materialize after removal")
	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read rewritten INDEX.md: %v", err)
	}
	if strings.Contains(string(index), "**beta**") || !strings.Contains(string(index), "**alpha**") {
		t.Fatalf("rewritten INDEX.md = %q, want alpha without beta", index)
	}
	for _, removed := range []string{
		filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "beta"),
		filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "beta"),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("removed skill path %q still exists or failed unexpectedly: %v", removed, err)
		}
	}
}

func TestMaterializeZeroSkillsWritesCatalogOnly(t *testing.T) {
	target := t.TempDir()
	if err := materialize(t.Context(), materializeStore{skills: &staticSkillStore{}}, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(indexPath)))
	if err != nil {
		t.Fatalf("read INDEX.md: %v", err)
	}
	wantIndex := skillIndexPreamble +
		"- **loom-skill-catalog** — " + catalogSkillDescription + " → read `.agents/skills/loom-skill-catalog/SKILL.md`\n"
	if string(index) != wantIndex {
		t.Fatalf("INDEX.md = %q, want catalog-only index %q", index, wantIndex)
	}
	if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), catalogSkillName, domain.SkillFileNameSKILLMD)); err != nil {
		t.Fatalf("stat catalog SKILL.md: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), catalogSkillName)); err != nil {
		t.Fatalf("read catalog compatibility link: %v", err)
	}

	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		indexPath,
		path.Join(AgentsSkillsDir, catalogSkillName, domain.SkillFileNameSKILLMD),
		path.Join(ClaudeSkillsDir, catalogSkillName),
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
	for _, managedPath := range wantPaths {
		if !validManagedPath(managedPath) {
			t.Fatalf("synthetic marker path %q is not valid", managedPath)
		}
	}
}

func TestMaterializeRejectsUnmanagedSkillIndex(t *testing.T) {
	target := t.TempDir()
	indexPath := filepath.Join(target, filepath.FromSlash(indexPath))
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("create skill directory: %v", err)
	}
	if err := os.WriteFile(indexPath, []byte("user-managed index\n"), 0o644); err != nil {
		t.Fatalf("write unmanaged INDEX.md: %v", err)
	}

	err := materialize(t.Context(), materializeStore{skills: &staticSkillStore{}}, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), AgentsSkillsDir) || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("Materialize error = %v, want unmanaged projection-root collision", err)
	}
	got, readErr := os.ReadFile(indexPath)
	if readErr != nil || string(got) != "user-managed index\n" {
		t.Fatalf("unmanaged INDEX.md changed: content=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(target, filepath.FromSlash(markerPath))); !os.IsNotExist(statErr) {
		t.Fatalf("marker exists after collision: %v", statErr)
	}
}

func TestMaterializeMatchingHashIsNoOp(t *testing.T) {
	target := t.TempDir()
	skills := &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "database body\n",
	}}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "first Materialize")
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("agent-authored working copy\n"), 0o644); err != nil {
		t.Fatalf("edit materialized copy: %v", err)
	}

	mustMaterialize(t, st, target, "second Materialize")
	got, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read working copy: %v", err)
	}
	if string(got) != "agent-authored working copy\n" {
		t.Fatalf("matching hash rewrote SKILL.md: %q", got)
	}
}

// Invalid trees cannot enter the immutable workspace-file store. If a complete
// prefetch encounters one, materialization fails before touching the prior
// projection instead of attempting to salvage a partial catalog.
func TestMaterializeRejectsUnpublishableSkillTreesBeforeProjection(t *testing.T) {
	tests := []struct {
		name string
		bad  *skillFixture
	}{
		{
			name: "name reserved for the catalog pointer",
			bad: &skillFixture{
				Name: catalogSkillName, Scope: domain.SkillScopeWorkspace,
				Description: "smuggled past write-time validation", Content: "body\n",
			},
		},
		{
			name: "bundled path escapes the skill directory",
			bad: &skillFixture{
				Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "beta", Content: "body\n",
				Files: []skillFixtureFile{{Path: "../escape.md", Content: "nope\n"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			skills := &staticSkillStore{skills: []*skillFixture{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "prior\n",
			}}}
			st := materializeStore{skills: skills}
			mustMaterialize(t, st, target, "initial Materialize")
			skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
			before, err := os.ReadFile(skillMD)
			if err != nil {
				t.Fatalf("read prior projection: %v", err)
			}

			skills.skills = []*skillFixture{tt.bad, &skillFixture{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "replacement\n",
			}}
			err = materialize(t.Context(), st, "WS", "lead", target)
			if !errors.Is(err, domain.ErrInvalid) || IsStoreUnavailable(err) {
				t.Fatalf("Materialize error = %v, want fatal invalid tree", err)
			}
			after, readErr := os.ReadFile(skillMD)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatalf("prior projection changed after failed prefetch: before=%q after=%q err=%v", before, after, readErr)
			}
			if _, err := os.Stat(filepath.Join(target, "escape.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("bundled path escaped the target: stat err = %v", err)
			}
		})
	}
}

func TestMaterializeMatchingHashReconcilesProjectionDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, target string)
	}{
		{
			name: "missing managed file",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Remove(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")); err != nil {
					t.Fatalf("remove managed file: %v", err)
				}
			},
		},
		{
			name: "executable mode drift",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Chmod(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "run.sh"), 0o644); err != nil {
					t.Fatalf("remove executable mode: %v", err)
				}
			},
		},
		{
			name: "managed file replaced by symlink",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				managed := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
				if err := os.Remove(managed); err != nil {
					t.Fatalf("remove managed file: %v", err)
				}
				outside := filepath.Join(target, "outside")
				if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
					t.Fatalf("write outside file: %v", err)
				}
				if err := os.Symlink(outside, managed); err != nil {
					t.Fatalf("replace managed file with symlink: %v", err)
				}
			},
		},
		{
			name: "managed compatibility link retargeted",
			mutate: func(t *testing.T, target string) {
				t.Helper()
				link := filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha")
				if err := os.Remove(link); err != nil {
					t.Fatalf("remove managed link: %v", err)
				}
				if err := os.Symlink("../../wrong", link); err != nil {
					t.Fatalf("retarget managed link: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
				Files: []skillFixtureFile{{Path: "run.sh", Content: "#!/bin/sh\n", Executable: true}},
			}}}}
			mustMaterialize(t, st, target, "first Materialize")
			tt.mutate(t, target)

			mustMaterialize(t, st, target, "second Materialize")
			skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
			info, err := os.Lstat(skillMD)
			if err != nil {
				t.Fatalf("lstat reconciled SKILL.md: %v", err)
			}
			if !info.Mode().IsRegular() {
				t.Fatalf("SKILL.md mode = %v, want regular file", info.Mode())
			}
			scriptInfo, err := os.Stat(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "run.sh"))
			if err != nil {
				t.Fatalf("stat reconciled executable: %v", err)
			}
			if scriptInfo.Mode().Perm() != 0o755 {
				t.Fatalf("run.sh mode = %o, want 755", scriptInfo.Mode().Perm())
			}
			linkTarget, err := os.Readlink(filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha"))
			if err != nil {
				t.Fatalf("read reconciled compatibility link: %v", err)
			}
			if linkTarget != "../../.agents/skills/alpha" {
				t.Fatalf("compatibility link target = %q", linkTarget)
			}
		})
	}
}

func TestMaterializeRestoresManagedFileReplacedByEmptyDirectory(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "initial Materialize")
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	if err := os.Remove(skillMD); err != nil {
		t.Fatalf("remove managed SKILL.md: %v", err)
	}
	if err := os.Mkdir(skillMD, 0o755); err != nil {
		t.Fatalf("replace managed SKILL.md with directory: %v", err)
	}

	mustMaterialize(t, st, target, "reconcile Materialize")
	body, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read restored SKILL.md: %v", err)
	}
	if got, want := string(body), "---\nname: alpha\ndescription: alpha\n---\nbody\n"; got != want {
		t.Fatalf("restored SKILL.md = %q, want %q", got, want)
	}
}

func TestMaterializeKeepsPersistingSkillsReadableWhileUpdatingAndDeleting(t *testing.T) {
	target := t.TempDir()
	version := func(body, fileBody string) *skillFixture {
		files := make([]skillFixtureFile, 8)
		for i := range files {
			files[i] = skillFixtureFile{
				Path:    fmt.Sprintf("references/file-%02d.md", i),
				Content: strings.Repeat(fileBody, 128),
			}
		}
		return &skillFixture{
			Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: body, Files: files,
		}
	}
	alphaA := version("version A\n", "A")
	alphaB := version("version B\n", "B")
	beta := &skillFixture{
		Name: "beta", Scope: domain.SkillScopeWorkspace, Description: "beta", Content: "persistent beta\n",
		Files: []skillFixtureFile{{Path: "references/one.md", Content: "one\n"}, {Path: "references/two.md", Content: "two\n"}},
	}
	deleted := &skillFixture{Name: "deleted", Scope: domain.SkillScopeWorkspace, Description: "deleted", Content: "remove me\n"}
	skills := &staticSkillStore{skills: []*skillFixture{alphaA, beta, deleted}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	alphaPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	betaPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "beta", "SKILL.md")
	wantAlpha := map[string]bool{
		"---\nname: alpha\ndescription: alpha\n---\nversion A\n": true,
		"---\nname: alpha\ndescription: alpha\n---\nversion B\n": true,
	}
	wantBeta := "---\nname: beta\ndescription: beta\n---\npersistent beta\n"

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	readerErr := make(chan error, 1)
	var reads atomic.Int64
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, check := range []struct {
				name    string
				path    string
				content func(string) bool
			}{
				{name: "alpha", path: alphaPath, content: func(got string) bool { return wantAlpha[got] }},
				{name: "beta", path: betaPath, content: func(got string) bool { return got == wantBeta }},
			} {
				body, err := os.ReadFile(check.path)
				if err != nil {
					readerErr <- fmt.Errorf("read persistent %s SKILL.md: %w", check.name, err)
					return
				}
				if !check.content(string(body)) {
					readerErr <- fmt.Errorf("persistent %s SKILL.md contained partial or unknown content %q", check.name, body)
					return
				}
				info, err := os.Stat(check.path)
				if err != nil {
					readerErr <- fmt.Errorf("stat persistent %s SKILL.md: %w", check.name, err)
					return
				}
				if !info.Mode().IsRegular() {
					readerErr <- fmt.Errorf("persistent %s SKILL.md mode = %v, want regular file", check.name, info.Mode())
					return
				}
				reads.Add(1)
			}
		}
	}()

	for i := 0; i < 64; i++ {
		alpha := alphaA
		if i%2 == 0 {
			alpha = alphaB
		}
		skills.skills = []*skillFixture{alpha, beta}
		if err := materialize(t.Context(), st, "WS", "lead", target); err != nil {
			close(stop)
			<-readerDone
			t.Fatalf("Materialize pass %d: %v", i+1, err)
		}
		select {
		case err := <-readerErr:
			close(stop)
			<-readerDone
			t.Fatal(err)
		default:
		}
	}
	close(stop)
	<-readerDone
	select {
	case err := <-readerErr:
		t.Fatal(err)
	default:
	}
	if got := reads.Load(); got < 100 {
		t.Fatalf("live reader completed %d reads, want at least 100", got)
	}
	deletedDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "deleted")
	if _, err := os.Stat(deletedDir); !os.IsNotExist(err) {
		t.Fatalf("deleted skill directory still exists or failed unexpectedly: %v", err)
	}
}

func TestMaterializeAtomicallyUpdatesContentAndExecutableMode(t *testing.T) {
	target := t.TempDir()
	skill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []skillFixtureFile{{Path: "scripts/run.sh", Content: "old\n"}},
	}
	skills := &staticSkillStore{skills: []*skillFixture{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	skill.Files = []skillFixtureFile{{Path: "scripts/run.sh", Content: "#!/bin/sh\necho new\n", Executable: true}}
	mustMaterialize(t, st, target, "updated Materialize")

	script := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "scripts", "run.sh")
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read updated script: %v", err)
	}
	if got, want := string(body), "#!/bin/sh\necho new\n"; got != want {
		t.Fatalf("updated script = %q, want %q", got, want)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat updated script: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o755); got != want {
		t.Fatalf("updated script mode = %o, want %o", got, want)
	}
}

func TestMaterializeTransitionsCaseFoldCollidingManagedPath(t *testing.T) {
	target := t.TempDir()
	skill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []skillFixtureFile{{Path: "Docs/a.md", Content: "old\n"}},
	}
	skills := &staticSkillStore{skills: []*skillFixture{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")

	skill.Files = []skillFixtureFile{{Path: "docs/A.md", Content: "new\n"}}
	mustMaterialize(t, st, target, "transition Materialize")

	newPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "docs", "A.md")
	if body, err := os.ReadFile(newPath); err != nil || string(body) != "new\n" {
		t.Fatalf("new case-folded path = %q, err=%v", body, err)
	}
	var relativeFiles []string
	skillRoot := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha")
	err := filepath.WalkDir(skillRoot, func(name string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if item.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillRoot, name)
		if err != nil {
			return err
		}
		relativeFiles = append(relativeFiles, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk transitioned skill: %v", err)
	}
	sort.Strings(relativeFiles)
	if want := []string{"SKILL.md", "docs/A.md"}; !reflect.DeepEqual(relativeFiles, want) {
		t.Fatalf("materialized files = %#v, want %#v", relativeFiles, want)
	}
}

func TestMaterializeSweepsCrashOrphanedProjectionTemporary(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "initial Materialize")
	orphanName := projectionTempPrefix + "crash-orphan"
	orphanPath := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", orphanName)
	if err := os.WriteFile(orphanPath, []byte("incomplete\n"), 0o600); err != nil {
		t.Fatalf("plant crash orphan: %v", err)
	}

	mustMaterialize(t, st, target, "reconcile after crash orphan")
	if _, err := os.Lstat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("crash-orphaned temporary still exists or failed unexpectedly: %v", err)
	}
	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	for _, recorded := range gotMarker.Paths {
		if strings.Contains(recorded, projectionTempPrefix) {
			t.Fatalf("marker recorded projection temporary %q", recorded)
		}
	}
	if validManagedPath(path.Join(AgentsSkillsDir, "alpha", orphanName)) {
		t.Fatalf("projection temporary %q is valid as a managed marker path", orphanName)
	}
}

func TestMaterializeRejectsPreGenerationPartialProjectionWithoutMarker(t *testing.T) {
	target := t.TempDir()
	skill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []skillFixtureFile{{Path: "run.sh", Content: "#!/bin/sh\n", Executable: true}},
	}
	fixtureStore := &staticSkillStore{skills: []*skillFixture{skill}}
	st := materializeStore{skills: fixtureStore}
	metadata, err := fixtureStore.List(t.Context(), "WS", store.SkillFilter{})
	if err != nil {
		t.Fatalf("publish fixture tree: %v", err)
	}
	entries, err := desiredEntries(t.Context(), st.WorkspaceFiles(), "WS", domain.ResolveSkillChainDetail(metadata, "lead"))
	if err != nil {
		t.Fatalf("load desired entries: %v", err)
	}
	partial := entries[0]
	partialPath := filepath.Join(target, filepath.FromSlash(partial.Path))
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
		t.Fatalf("create partial projection parent: %v", err)
	}
	if err := os.WriteFile(partialPath, partial.Content, partial.Mode); err != nil {
		t.Fatalf("write exact partial projection: %v", err)
	}
	if err := os.Chmod(partialPath, partial.Mode); err != nil {
		t.Fatalf("set exact partial projection mode: %v", err)
	}

	err = materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), AgentsSkillsDir) || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("Materialize error = %v, want pre-generation projection-root collision", err)
	}
	got, readErr := os.ReadFile(partialPath)
	if readErr != nil || !bytes.Equal(got, partial.Content) {
		t.Fatalf("pre-generation partial path changed: content=%q err=%v", got, readErr)
	}
}

func TestWriteMarkerAtomicallyRenamesCompletedTemporaryFile(t *testing.T) {
	root := &markerRecordingRoot{}
	if err := writeMarkerAtomically(root, []byte("complete marker\n")); err != nil {
		t.Fatalf("writeMarkerAtomically: %v", err)
	}
	if len(root.created) != 1 || root.created[0] == markerPath || !strings.HasPrefix(root.created[0], markerPath+".tmp-") {
		t.Fatalf("created paths = %#v, want one marker temporary", root.created)
	}
	if len(root.renamed) != 1 || root.renamed[0] != [2]string{root.created[0], markerPath} {
		t.Fatalf("renames = %#v, want temporary -> marker", root.renamed)
	}
}

func TestMaterializeRemovalPreservesUnrecordedFiles(t *testing.T) {
	target := t.TempDir()
	skills := &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
		Files: []skillFixtureFile{{Path: "references/managed.md", Content: "managed\n"}},
	}}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "first Materialize")
	unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "notes.user")
	if err := os.WriteFile(unrecorded, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write unrecorded file: %v", err)
	}

	skills.skills = nil
	mustMaterialize(t, st, target, "remove Materialize")
	got, err := os.ReadFile(unrecorded)
	if err != nil {
		t.Fatalf("unrecorded file was removed: %v", err)
	}
	if string(got) != "keep me\n" {
		t.Fatalf("unrecorded file = %q", got)
	}
	for _, removed := range []string{
		filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"),
		filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "references", "managed.md"),
		filepath.Join(target, filepath.FromSlash(ClaudeSkillsDir), "alpha"),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("recorded path %q still exists or failed unexpectedly: %v", removed, err)
		}
	}
	markerBytes, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(markerPath)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var gotMarker marker
	if err := json.Unmarshal(markerBytes, &gotMarker); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	wantPaths := []string{
		indexPath,
		path.Join(AgentsSkillsDir, catalogSkillName, domain.SkillFileNameSKILLMD),
		path.Join(ClaudeSkillsDir, catalogSkillName),
	}
	if !reflect.DeepEqual(gotMarker.Paths, wantPaths) {
		t.Fatalf("marker paths after removal = %#v, want %#v", gotMarker.Paths, wantPaths)
	}
}

func TestMaterializeRoleDeletionUnshadowsWorkspaceSkill(t *testing.T) {
	target := t.TempDir()
	workspaceSkill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "workspace", Content: "workspace body\n",
	}
	skills := &staticSkillStore{skills: []*skillFixture{
		workspaceSkill,
		{Name: "alpha", Scope: domain.SkillScopeRole, RoleName: "lead", Description: "role", Content: "role body\n"},
	}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "shadowed Materialize")

	skills.skills = []*skillFixture{workspaceSkill}
	mustMaterialize(t, st, target, "unshadowed Materialize")
	got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(got), "description: workspace\n") || !strings.HasSuffix(string(got), "workspace body\n") {
		t.Fatalf("unshadowed SKILL.md = %q", got)
	}
}

// Paths that collide under materialization rules are rejected by immutable
// tree publication, before any member of the tree reaches the filesystem.
func TestMaterializeRefusesToWriteInSkillPathCollisions(t *testing.T) {
	tests := []struct {
		name  string
		files []skillFixtureFile
		// paths that must not exist under the skill directory afterwards
		unwritten []string
	}{
		{
			name:      "reserved body name under different case",
			files:     []skillFixtureFile{{Path: "skill.md", Content: "collision"}},
			unwritten: []string{"skill.md"},
		},
		{
			name: "unicode normalization",
			files: []skillFixtureFile{
				{Path: "references/caf\u00e9.md", Content: "NFC"},
				{Path: "references/cafe\u0301.md", Content: "NFD"},
			},
			unwritten: []string{"references/caf\u00e9.md", "references/cafe\u0301.md"},
		},
		{
			name: "file versus directory",
			files: []skillFixtureFile{
				{Path: "a/b", Content: "file"},
				{Path: "a/b/c", Content: "child"},
			},
			unwritten: []string{"a/b", "a/b/c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body", Files: tt.files,
			}}}}
			err := materialize(t.Context(), st, "WS", "lead", target)
			if !errors.Is(err, domain.ErrInvalid) || IsStoreUnavailable(err) {
				t.Fatalf("Materialize error = %v, want fatal invalid collision", err)
			}

			skillDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha")
			if _, statErr := os.Lstat(skillDir); !os.IsNotExist(statErr) {
				t.Fatalf("colliding skill was materialized: stat err = %v", statErr)
			}
			for _, unwritten := range tt.unwritten {
				if _, statErr := os.Lstat(filepath.Join(skillDir, filepath.FromSlash(unwritten))); !os.IsNotExist(statErr) {
					t.Fatalf("colliding path %q was written: stat err = %v", unwritten, statErr)
				}
			}
			if _, statErr := os.Lstat(filepath.Join(target, filepath.FromSlash(markerPath))); !os.IsNotExist(statErr) {
				t.Fatalf("marker exists after rejected tree: %v", statErr)
			}
		})
	}
}

func TestMaterializeRejectsExistingCaseFoldCollision(t *testing.T) {
	target := t.TempDir()
	mustMaterialize(t, materializeStore{skills: &staticSkillStore{}}, target, "initial empty Materialize")
	skillDir := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("create user skill dir: %v", err)
	}
	existing := filepath.Join(skillDir, "README.md")
	if err := os.WriteFile(existing, []byte("user file\n"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []skillFixtureFile{{Path: "readme.md", Content: "managed"}},
	}}}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil {
		t.Fatal("Materialize error = nil, want collision")
	}
	for _, want := range []string{"readme.md", "README.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Materialize error = %q, want path %q", err, want)
		}
	}
	got, readErr := os.ReadFile(existing)
	if readErr != nil || string(got) != "user file\n" {
		t.Fatalf("existing file changed: content=%q err=%v", got, readErr)
	}
}

func TestMaterializeRefusesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	outsideHooks := filepath.Join(base, "outside", ".git", "hooks")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	mustMaterialize(t, materializeStore{skills: &staticSkillStore{}}, target, "initial empty Materialize")
	if err := os.MkdirAll(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha"), 0o755); err != nil {
		t.Fatalf("create planted skill dir: %v", err)
	}
	if err := os.MkdirAll(outsideHooks, 0o755); err != nil {
		t.Fatalf("create outside hooks: %v", err)
	}
	link := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "link")
	if err := os.Symlink("../../../../outside/.git/hooks", link); err != nil {
		t.Fatalf("plant escape symlink: %v", err)
	}
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []skillFixtureFile{{Path: "link/pre-commit", Content: "#!/bin/sh\n", Executable: true}},
	}}}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil {
		t.Fatal("Materialize error = nil, want planted symlink refusal")
	}
	for _, want := range []string{"link/pre-commit", ".agents/skills/alpha/link"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Materialize error = %q, want both colliding paths including %q", err, want)
		}
	}
	if _, statErr := os.Lstat(filepath.Join(outsideHooks, "pre-commit")); !os.IsNotExist(statErr) {
		t.Fatalf("escape path was written outside target: %v", statErr)
	}
}

func TestMaterializeRelativeClaudeLinkSurvivesTargetMove(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "before")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("create target: %v", err)
	}
	st := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body\n",
	}}}}
	mustMaterialize(t, st, target, "Materialize")
	moved := filepath.Join(base, "after")
	if err := os.Rename(target, moved); err != nil {
		t.Fatalf("move target: %v", err)
	}
	throughLink := filepath.Join(moved, filepath.FromSlash(ClaudeSkillsDir), "alpha", "SKILL.md")
	got, err := os.ReadFile(throughLink)
	if err != nil {
		t.Fatalf("read through moved relative link: %v", err)
	}
	if !strings.HasSuffix(string(got), "body\n") {
		t.Fatalf("SKILL.md through moved link = %q", got)
	}
}

func TestMaterializeStoreOutageLeavesProjectionUntouched(t *testing.T) {
	target := t.TempDir()
	available := materializeStore{skills: &staticSkillStore{skills: []*skillFixture{{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "old body\n",
	}}}}
	if err := materialize(t.Context(), available, "WS", "lead", target); err != nil {
		t.Fatalf("initial Materialize: %v", err)
	}
	skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "SKILL.md")
	before, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read initial projection: %v", err)
	}
	outage := &url.Error{Op: "GET", URL: "http://fleet-db/skills", Err: syscall.ECONNREFUSED}
	unavailable := materializeStore{skills: &staticSkillStore{err: outage}}
	err = materialize(t.Context(), unavailable, "WS", "lead", target)
	if !IsStoreUnavailable(err) || !errors.Is(err, outage) {
		t.Fatalf("Materialize error = %v, want store-unavailable wrapper", err)
	}
	after, readErr := os.ReadFile(skillMD)
	if readErr != nil {
		t.Fatalf("read projection after outage: %v", readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("projection changed during outage:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestMaterializeWorkspaceFileOutageLeavesProjectionUntouched(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		getTree bool
	}{
		{name: "transport", err: &url.Error{Op: "GET", URL: "http://fleet-db/workspace-files", Err: syscall.ECONNREFUSED}, getTree: true},
		{name: "server 5xx", err: errors.New("fleet-db returned HTTP 503: temporarily unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			skills := &staticSkillStore{skills: []*skillFixture{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "prior\n",
			}}}
			available := materializeStore{skills: skills}
			mustMaterialize(t, available, target, "initial Materialize")
			skillMD := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", domain.SkillFileNameSKILLMD)
			before, err := os.ReadFile(skillMD)
			if err != nil {
				t.Fatalf("read prior projection: %v", err)
			}

			skills.skills[0].Content = "replacement\n"
			failure := failingWorkspaceFileStore{WorkspaceFileStore: skills.workspaceFiles()}
			if tt.getTree {
				failure.getTreeErr = tt.err
			} else {
				failure.downloadErr = tt.err
			}
			unavailable := materializeStore{
				skills: skills,
				files:  failure,
			}
			err = materialize(t.Context(), unavailable, "WS", "lead", target)
			if !IsStoreUnavailable(err) || !errors.Is(err, tt.err) {
				t.Fatalf("Materialize error = %v, want workspace-file store-unavailable wrapper", err)
			}
			after, readErr := os.ReadFile(skillMD)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatalf("projection changed during workspace-file outage: before=%q after=%q err=%v", before, after, readErr)
			}
		})
	}
}

func TestMaterializeWorkspaceFileIntegrityAndNotFoundRemainFatal(t *testing.T) {
	registry.MarkEvidence(t, 71)
	tests := []struct {
		name  string
		files func(store.WorkspaceFileStore) store.WorkspaceFileStore
		want  error
	}{
		{
			name: "not found",
			files: func(files store.WorkspaceFileStore) store.WorkspaceFileStore {
				return failingWorkspaceFileStore{WorkspaceFileStore: files, downloadErr: domain.ErrNotFound}
			},
			want: domain.ErrNotFound,
		},
		{
			name: "integrity",
			files: func(files store.WorkspaceFileStore) store.WorkspaceFileStore {
				return failingWorkspaceFileStore{WorkspaceFileStore: files, downloadErr: domain.ErrIntegrity}
			},
			want: domain.ErrIntegrity,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			skills := &staticSkillStore{skills: []*skillFixture{{
				Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "prior\n",
			}}}
			mustMaterialize(t, materializeStore{skills: skills}, target, "initial Materialize")
			before := snapshotMaterializedProjection(t, target)

			skills.skills[0].Content = "replacement\n"
			st := materializeStore{skills: skills, files: tt.files(skills.workspaceFiles())}
			err := materialize(t.Context(), st, "WS", "lead", target)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Materialize error = %v, want %v", err, tt.want)
			}
			if IsStoreUnavailable(err) {
				t.Fatalf("fatal workspace-file error classified as unavailable: %v", err)
			}
			after := snapshotMaterializedProjection(t, target)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("projection changed after download failure:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestMaterializeDoesNotDegradeOnNonOutageStoreError(t *testing.T) {
	target := t.TempDir()
	denied := fmt.Errorf("skill list forbidden: %w", domain.ErrConflict)
	st := materializeStore{skills: &staticSkillStore{err: denied}}
	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Materialize error = %v, want store error", err)
	}
	if IsStoreUnavailable(err) {
		t.Fatalf("authorization error classified as outage: %v", err)
	}
}

func TestMaterializeDoesNotClassifyCancellationAsStoreOutage(t *testing.T) {
	target := t.TempDir()
	st := materializeStore{skills: &staticSkillStore{err: context.Canceled}}
	err := materialize(t.Context(), st, "WS", "lead", target)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Materialize error = %v, want context.Canceled", err)
	}
	if IsStoreUnavailable(err) {
		t.Fatalf("context cancellation classified as store outage: %v", err)
	}
}

func TestMaterializeRejectsOversizedAndPartialMarkersWithoutCleanup(t *testing.T) {
	tests := []struct {
		name       string
		markerBody []byte
		want       string
	}{
		{name: "oversized", markerBody: bytes.Repeat([]byte("x"), (1<<20)+1), want: "maximum size"},
		{name: "partial JSON", markerBody: []byte(`{"version":1`), want: "decode skill marker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			mustMaterialize(t, materializeStore{skills: &staticSkillStore{}}, target, "initial Materialize")
			markerPath := filepath.Join(target, filepath.FromSlash(markerPath))
			if err := os.Remove(markerPath); err != nil {
				t.Fatalf("remove valid marker: %v", err)
			}
			if err := os.WriteFile(markerPath, tt.markerBody, 0o644); err != nil {
				t.Fatalf("write marker: %v", err)
			}
			sentinel := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "keep.user")
			if err := os.WriteFile(sentinel, []byte("keep\n"), 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}

			err := materialize(t.Context(), materializeStore{skills: &staticSkillStore{}}, "WS", "lead", target)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Materialize error = %v, want %q", err, tt.want)
			}
			if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "keep\n" {
				t.Fatalf("sentinel changed after marker rejection: content=%q err=%v", got, readErr)
			}
			gotMarker, readErr := os.ReadFile(markerPath)
			if readErr != nil || !bytes.Equal(gotMarker, tt.markerBody) {
				t.Fatalf("marker changed after rejection: size=%d err=%v", len(gotMarker), readErr)
			}
		})
	}
}

func TestValidManagedPathRejectsNonSlashSeparators(t *testing.T) {
	for _, unsafe := range []string{
		`.agents/skills/x\..\..\..\README.md`,
		`.claude/skills/x\..\outside`,
	} {
		if validManagedPath(unsafe) {
			t.Fatalf("validManagedPath(%q) = true, want false", unsafe)
		}
	}
}

func TestMaterializeEnsuresGitExcludeViaGitPath(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "loom-test@example.invalid")
	runGit(t, repo, "config", "user.name", "Loom Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-qm", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, repo, "worktree", "add", "-q", "-b", "skillmat-test", linked)
	t.Cleanup(func() {
		cmd := exec.Command("git", "-C", repo, "worktree", "remove", "--force", linked) //nolint:gosec //nolint:norawexec // test fixture cleanup
		_ = cmd.Run()
	})

	st := materializeStore{skills: &staticSkillStore{}}
	for i := 0; i < 2; i++ {
		if err := materialize(t.Context(), st, "WS", "lead", linked); err != nil {
			t.Fatalf("Materialize pass %d: %v", i+1, err)
		}
	}
	excludePath := strings.TrimSpace(runGit(t, linked, "rev-parse", "--git-path", "info/exclude"))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(linked, excludePath)
	}
	b, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read resolved exclude %q: %v", excludePath, err)
	}
	for _, line := range []string{AgentsSkillsDir + "/", ClaudeSkillsDir + "/"} {
		if count := strings.Count(string(b), line+"\n"); count != 1 {
			t.Fatalf("exclude entry %q count = %d, want 1 in:\n%s", line, count, b)
		}
	}
	if status := strings.TrimSpace(runGit(t, linked, "status", "--porcelain")); status != "" {
		t.Fatalf("materialized paths are visible to git status: %s", status)
	}
}

func TestMaterializeGitExcludeIgnoresPoisonedGitEnvironment(t *testing.T) {
	target := t.TempDir()
	attacker := t.TempDir()
	runGit(t, target, "init", "-q")
	runGit(t, attacker, "init", "-q")
	targetExclude := filepath.Join(target, ".git", "info", "exclude")
	attackerExclude := filepath.Join(attacker, ".git", "info", "exclude")
	attackerBefore, err := os.ReadFile(attackerExclude)
	if err != nil {
		t.Fatalf("read attacker exclude before materialization: %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(attacker, ".git"))
	t.Setenv("GIT_WORK_TREE", attacker)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(attacker, ".git", "index"))

	if err := materialize(t.Context(), materializeStore{skills: &staticSkillStore{}}, "WS", "lead", target); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	targetBody, err := os.ReadFile(targetExclude)
	if err != nil {
		t.Fatalf("read target exclude: %v", err)
	}
	for _, line := range []string{AgentsSkillsDir + "/", ClaudeSkillsDir + "/"} {
		if !strings.Contains(string(targetBody), line+"\n") {
			t.Fatalf("target exclude missing %q:\n%s", line, targetBody)
		}
	}
	attackerAfter, err := os.ReadFile(attackerExclude)
	if err != nil {
		t.Fatalf("read attacker exclude after materialization: %v", err)
	}
	if !bytes.Equal(attackerAfter, attackerBefore) {
		t.Fatalf("poisoned Git environment redirected exclude write:\nbefore=%q\nafter=%q", attackerBefore, attackerAfter)
	}
}

func TestMaterializeGitExcludeRefusesSymlinkedInfoParent(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	gitInfo := filepath.Join(repo, ".git", "info")
	if err := os.Rename(gitInfo, filepath.Join(repo, ".git", "info.real")); err != nil {
		t.Fatalf("move real git info directory: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, gitInfo); err != nil {
		t.Fatalf("plant git info symlink: %v", err)
	}

	err := materialize(t.Context(), materializeStore{skills: &staticSkillStore{}}, "WS", "lead", repo)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Materialize error = %v, want symlink refusal", err)
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "exclude")); !os.IsNotExist(statErr) {
		t.Fatalf("symlinked git info parent was traversed: %v", statErr)
	}
}

func TestMaterializeReconcilesManagedFileDirectoryTransitions(t *testing.T) {
	tests := []struct {
		name   string
		before []skillFixtureFile
		after  []skillFixtureFile
		want   string
	}{
		{
			name:   "file becomes directory",
			before: []skillFixtureFile{{Path: "node", Content: "old"}},
			after:  []skillFixtureFile{{Path: "node/child", Content: "new"}},
			want:   "node/child",
		},
		{
			name:   "directory becomes file",
			before: []skillFixtureFile{{Path: "node/child", Content: "old"}},
			after:  []skillFixtureFile{{Path: "node", Content: "new"}},
			want:   "node",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			skill := &skillFixture{Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body", Files: tt.before}
			skills := &staticSkillStore{skills: []*skillFixture{skill}}
			st := materializeStore{skills: skills}
			mustMaterialize(t, st, target, "initial Materialize")
			skill.Files = tt.after
			mustMaterialize(t, st, target, "transition Materialize")
			got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", filepath.FromSlash(tt.want)))
			if err != nil || string(got) != "new" {
				t.Fatalf("transition path %q = %q, err=%v", tt.want, got, err)
			}
		})
	}
}

func TestMaterializeRefusesDirectoryToFileTransitionWithUnrecordedChild(t *testing.T) {
	target := t.TempDir()
	skill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []skillFixtureFile{{Path: "node/managed", Content: "old"}},
	}
	skills := &staticSkillStore{skills: []*skillFixture{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")
	unrecorded := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "node", "user")
	if err := os.WriteFile(unrecorded, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrecorded child: %v", err)
	}
	skill.Files = []skillFixtureFile{{Path: "node", Content: "new"}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "node") || !strings.Contains(err.Error(), "user") {
		t.Fatalf("Materialize error = %v, want node/user collision", err)
	}
	if got, readErr := os.ReadFile(unrecorded); readErr != nil || string(got) != "keep" {
		t.Fatalf("unrecorded child changed: content=%q err=%v", got, readErr)
	}
}

func TestMaterializeRefusesManagedFileToNonemptyDirectoryBeforeCleanup(t *testing.T) {
	target := t.TempDir()
	skill := &skillFixture{
		Name: "alpha", Scope: domain.SkillScopeWorkspace, Description: "alpha", Content: "body",
		Files: []skillFixtureFile{{Path: "node", Content: "old"}, {Path: "zzz", Content: "must remain"}},
	}
	skills := &staticSkillStore{skills: []*skillFixture{skill}}
	st := materializeStore{skills: skills}
	mustMaterialize(t, st, target, "initial Materialize")
	node := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "node")
	if err := os.Remove(node); err != nil {
		t.Fatalf("remove managed node file: %v", err)
	}
	if err := os.Mkdir(node, 0o755); err != nil {
		t.Fatalf("replace managed node file with directory: %v", err)
	}
	unrecorded := filepath.Join(node, "user")
	if err := os.WriteFile(unrecorded, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrecorded child: %v", err)
	}
	skill.Files = []skillFixtureFile{{Path: "node/child", Content: "new"}, {Path: "zzz", Content: "updated"}}

	err := materialize(t.Context(), st, "WS", "lead", target)
	if err == nil || !strings.Contains(err.Error(), "node") {
		t.Fatalf("Materialize error = %v, want managed file-to-directory drift", err)
	}
	if got, readErr := os.ReadFile(unrecorded); readErr != nil || string(got) != "keep" {
		t.Fatalf("unrecorded child changed: content=%q err=%v", got, readErr)
	}
	zzz := filepath.Join(target, filepath.FromSlash(AgentsSkillsDir), "alpha", "zzz")
	if got, readErr := os.ReadFile(zzz); readErr != nil || string(got) != "must remain" {
		t.Fatalf("lexically later managed file changed before collision refusal: content=%q err=%v", got, readErr)
	}
}

type projectedPathSnapshot struct {
	Mode       os.FileMode
	Content    string
	LinkTarget string
}

func snapshotMaterializedProjection(t *testing.T, target string) map[string]projectedPathSnapshot {
	t.Helper()
	snapshot := make(map[string]projectedPathSnapshot)
	for _, root := range []string{AgentsSkillsDir, ClaudeSkillsDir} {
		absoluteRoot := filepath.Join(target, filepath.FromSlash(root))
		resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
		if err != nil {
			t.Fatalf("resolve materialized projection %s: %v", root, err)
		}
		err = filepath.WalkDir(resolvedRoot, func(name string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relativeWithinRoot, err := filepath.Rel(resolvedRoot, name)
			if err != nil {
				return err
			}
			relative := root
			if relativeWithinRoot != "." {
				relative = path.Join(root, filepath.ToSlash(relativeWithinRoot))
			}
			info, err := os.Lstat(name)
			if err != nil {
				return err
			}
			node := projectedPathSnapshot{Mode: info.Mode()}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				node.LinkTarget, err = os.Readlink(name)
			case info.Mode().IsRegular():
				var content []byte
				// #nosec G122 -- this is a test-only snapshot rooted in t.TempDir;
				// Lstat above deliberately records symlinks without opening them.
				content, err = os.ReadFile(name)
				node.Content = string(content)
			}
			if err != nil {
				return err
			}
			snapshot[relative] = node
			return nil
		})
		if err != nil {
			t.Fatalf("snapshot materialized projection: %v", err)
		}
	}
	return snapshot
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec //nolint:norawexec // test fixture setup
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// mustMaterialize runs a materialization the test requires to succeed. label
// names the step, so a test with several materializations still says which one
// failed.
func mustMaterialize(t *testing.T, st store.Store, target, label string) {
	t.Helper()
	if err := materialize(t.Context(), st, "WS", "lead", target); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}
