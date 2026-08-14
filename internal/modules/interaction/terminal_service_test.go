package interaction

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

type terminalStoreFake struct {
	mu   sync.Mutex
	tabs map[string]TabMetadata
}

func newTerminalStoreFake() *terminalStoreFake {
	return &terminalStoreFake{tabs: make(map[string]TabMetadata)}
}

func terminalStoreKey(workspaceKey, terminalID string) string {
	return workspaceKey + "\x00" + terminalID
}

func (store *terminalStoreFake) Get(_ context.Context, workspaceKey, terminalID string) (*TabMetadata, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.tabs[terminalStoreKey(workspaceKey, terminalID)]
	if !ok {
		return nil, nil
	}
	copy := value
	return &copy, nil
}

func (store *terminalStoreFake) List(_ context.Context, workspaceKey string) ([]TabMetadata, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	values := make([]TabMetadata, 0)
	for _, value := range store.tabs {
		if value.Workspace == workspaceKey {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].SortOrder < values[j].SortOrder })
	return values, nil
}

func (store *terminalStoreFake) ListAll(ctx context.Context) ([]TabMetadata, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	values := make([]TabMetadata, 0, len(store.tabs))
	for _, value := range store.tabs {
		values = append(values, value)
	}
	return values, nil
}

func (store *terminalStoreFake) Set(_ context.Context, metadata *TabMetadata) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tabs[terminalStoreKey(metadata.Workspace, metadata.SessionName)] = *metadata
	return nil
}

func (store *terminalStoreFake) Patch(ctx context.Context, workspaceKey, terminalID string, fields map[string]string) (*TabMetadata, error) {
	metadata, _ := store.Get(ctx, workspaceKey, terminalID)
	if metadata == nil {
		return nil, errors.New("not found")
	}
	if value, ok := fields["label"]; ok {
		metadata.Label = value
	}
	if value, ok := fields["issue_id"]; ok {
		metadata.IssueID = value
	}
	_ = store.Set(ctx, metadata)
	return metadata, nil
}

func (store *terminalStoreFake) Delete(_ context.Context, workspaceKey, terminalID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.tabs, terminalStoreKey(workspaceKey, terminalID))
	return nil
}

func (store *terminalStoreFake) EnsureDefaults(ctx context.Context, workspaceKey string, _ []string) ([]TabMetadata, error) {
	return store.List(ctx, workspaceKey)
}

func (store *terminalStoreFake) ListByIssue(_ context.Context, issueID string) ([]TabMetadata, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	values := make([]TabMetadata, 0)
	for _, value := range store.tabs {
		if value.IssueID == issueID {
			values = append(values, value)
		}
	}
	return values, nil
}

func (store *terminalStoreFake) ListIssueSessionMap(context.Context) (map[string][]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make(map[string][]string)
	for _, value := range store.tabs {
		if value.IssueID != "" {
			result[value.IssueID] = append(result[value.IssueID], value.SessionName)
		}
	}
	return result, nil
}

type terminalRuntimeFake struct {
	live         map[TerminalKey]bool
	closed       map[TerminalKey]bool
	killed       []TerminalKey
	ensure       []TerminalKey
	writes       map[TerminalKey]string
	attachments  int
	attachResult TerminalAttachment
	attachErr    error
	attachLaunch *LaunchSpec
	reattach     bool
}

func newTerminalRuntimeFake() *terminalRuntimeFake {
	return &terminalRuntimeFake{
		live: make(map[TerminalKey]bool), closed: make(map[TerminalKey]bool),
		writes: make(map[TerminalKey]string), attachments: 1,
	}
}

func (runtime *terminalRuntimeFake) Attach(_ TerminalKey, _ uint16, _ uint16, launch *LaunchSpec) (TerminalAttachment, bool, error) {
	runtime.attachLaunch = cloneLaunchSpec(launch)
	return runtime.attachResult, runtime.reattach, runtime.attachErr
}
func (*terminalRuntimeFake) Detach(TerminalKey, string) {}
func (runtime *terminalRuntimeFake) Kill(key TerminalKey) error {
	runtime.killed = append(runtime.killed, key)
	delete(runtime.live, key)
	runtime.closed[key] = true
	return nil
}
func (runtime *terminalRuntimeFake) IsLive(key TerminalKey) bool   { return runtime.live[key] }
func (runtime *terminalRuntimeFake) IsClosed(key TerminalKey) bool { return runtime.closed[key] }
func (runtime *terminalRuntimeFake) AttachmentCount(key TerminalKey) int {
	if runtime.live[key] {
		return runtime.attachments
	}
	return 0
}
func (runtime *terminalRuntimeFake) SessionCount() int          { return len(runtime.live) }
func (runtime *terminalRuntimeFake) SessionCountFor(string) int { return len(runtime.live) }
func (*terminalRuntimeFake) MaxSessions() int                   { return 10 }
func (runtime *terminalRuntimeFake) Ensure(key TerminalKey, _ uint16, _ uint16, _ *LaunchSpec) (bool, error) {
	runtime.ensure = append(runtime.ensure, key)
	created := !runtime.live[key]
	runtime.live[key] = true
	return created, nil
}
func (runtime *terminalRuntimeFake) WriteInput(key TerminalKey, input []byte) error {
	runtime.writes[key] += string(input)
	return nil
}

func TestTerminalTabsProjectRuntimeLivenessAndFenceDelete(t *testing.T) {
	ctx := context.Background()
	store := newTerminalStoreFake()
	runtime := newTerminalRuntimeFake()
	now := time.Now().UTC()
	service := NewTerminalTabs(store, runtime, now, TerminalDependencies{}).(*TerminalTabService)
	_, err := service.PutTab(ctx, PutTerminalTabCommand{
		WorkspaceKey: "WS", TerminalID: "term-1", Label: "Terminal", Backend: "shell",
	})
	if err != nil {
		t.Fatalf("put tab: %v", err)
	}
	key := TerminalKey{WorkspaceKey: "WS", TerminalID: "term-1"}
	runtime.live[key] = true
	got, err := service.GetTab(ctx, "WS", "term-1")
	if err != nil || !got.PTYAlive || got.AttachedClients != 1 {
		t.Fatalf("runtime projection = %#v, err %v", got, err)
	}
	if err := service.DeleteTab(ctx, "WS", "term-1"); err != nil {
		t.Fatalf("delete tab: %v", err)
	}
	if len(runtime.killed) != 1 || runtime.killed[0] != key {
		t.Fatalf("killed = %#v", runtime.killed)
	}
	if remaining, _ := store.Get(ctx, "WS", "term-1"); remaining != nil {
		t.Fatalf("metadata survived runtime convergence: %#v", remaining)
	}
}

func TestPutTerminalTabDerivesLaunchEnvelopeInsideInteraction(t *testing.T) {
	store := newTerminalStoreFake()
	service := NewTerminalTabs(store, newTerminalRuntimeFake(), time.Now(), TerminalDependencies{
		Placement: agentTerminalPlacementFake{configDir: "/trusted/loom-data"},
	}).(*TerminalTabService)

	meta, err := service.PutTab(t.Context(), PutTerminalTabCommand{
		WorkspaceKey: "WS", TerminalID: "codex-tab", Label: "Codex", Backend: "codex",
	})
	if err != nil {
		t.Fatalf("PutTab: %v", err)
	}
	if meta == nil || meta.Launch == nil || len(meta.Launch.Argv) != 2 {
		t.Fatalf("metadata = %#v", meta)
	}
	if got := meta.Launch.Env["LOOM_CONFIG_DIR"]; got != "/trusted/loom-data" {
		t.Fatalf("LOOM_CONFIG_DIR = %q", got)
	}
}

func TestTerminalSetupUsesTypedRuntimeCommandAndPersistsPlacement(t *testing.T) {
	ctx := context.Background()
	store := newTerminalStoreFake()
	runtime := newTerminalRuntimeFake()
	service := NewTerminalTabs(store, runtime, time.Now(), TerminalDependencies{Setup: NewTerminalSetupCatalog()}).(*TerminalTabService)
	result, err := service.StartSetup(ctx, "WS", TerminalSetupRequest{Backend: "codex", Action: "test"})
	if err != nil {
		t.Fatalf("start setup: %v", err)
	}
	key := TerminalKey{WorkspaceKey: "WS", TerminalID: "WS--lead-shell-setup-codex"}
	if len(runtime.ensure) != 1 || runtime.ensure[0] != key || !result.Created {
		t.Fatalf("ensure = %#v, result = %#v", runtime.ensure, result)
	}
	metadata, _ := store.Get(ctx, "WS", key.TerminalID)
	if metadata == nil || metadata.Kind != "setup" || metadata.Backend != "shell" {
		t.Fatalf("setup metadata = %#v", metadata)
	}
}

func TestTerminalSetupCatalogFailsClosed(t *testing.T) {
	store := newTerminalStoreFake()
	runtime := newTerminalRuntimeFake()
	service := NewTerminalTabs(store, runtime, time.Now(), TerminalDependencies{}).(*TerminalTabService)
	if _, err := service.StartSetup(t.Context(), "WS", TerminalSetupRequest{
		Backend: "codex", Action: "test",
	}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing catalog error = %v, want ErrUnavailable", err)
	}
	if len(runtime.ensure) != 0 {
		t.Fatalf("runtime started without catalog: %#v", runtime.ensure)
	}

	catalog := NewTerminalSetupCatalog()
	for _, request := range []TerminalSetupRequest{
		{Backend: "unknown", Action: "test"},
		{Backend: "codex", Action: "arbitrary-shell"},
	} {
		service.agentTerminal.Setup = catalog
		if _, err := service.StartSetup(t.Context(), "WS", request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("request %#v error = %v, want ErrInvalid", request, err)
		}
	}
	if len(runtime.ensure) != 0 {
		t.Fatalf("runtime started for rejected intent: %#v", runtime.ensure)
	}
}

func TestPublicTerminalErrorMessageHidesInfrastructureCause(t *testing.T) {
	err := terminalError(ErrUnavailable, "failed to list tab metadata", errors.New("redis password leaked in diagnostic"))
	if got := PublicTerminalErrorMessage(err); got != "failed to list tab metadata" {
		t.Fatalf("message = %q, want client-safe owner message", got)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("terminal failure lost its capability classification")
	}
}
