package workspace

import (
	"context"
	"errors"
	"testing"
)

type fakeCatalog struct {
	created         *Reference
	createIn        CreateInput
	createErr       error
	byKey           *Reference
	byName          *Reference
	listed          []Reference
	updated         *Reference
	keyErr          error
	nameErr         error
	listErr         error
	updateErr       error
	keyQuery        string
	nameQuery       string
	renameKey       string
	renameTo        string
	formatKey       string
	formatTo        string
	lifecycleKey    string
	lifecycleUpdate LifecycleUpdate
	deletedKey      string
	deleteErr       error
}

type fakeRepositoryCatalog struct {
	value        *Repository
	values       []Repository
	err          error
	workspaceKey string
	name         string
	created      RepositoryInput
	updated      RepositoryUpdate
	deleted      string
}

func (f *fakeRepositoryCatalog) Create(_ context.Context, input RepositoryInput) (*Repository, error) {
	f.created = input
	return f.value, f.err
}

func (f *fakeRepositoryCatalog) Get(_ context.Context, workspaceKey, name string) (*Repository, error) {
	f.workspaceKey, f.name = workspaceKey, name
	return f.value, f.err
}

func (f *fakeRepositoryCatalog) List(_ context.Context, workspaceKey string) ([]Repository, error) {
	f.workspaceKey = workspaceKey
	return f.values, f.err
}

func (f *fakeRepositoryCatalog) Update(_ context.Context, workspaceKey, name string, update RepositoryUpdate) (*Repository, error) {
	f.workspaceKey, f.name, f.updated = workspaceKey, name, update
	return f.value, f.err
}

func (f *fakeRepositoryCatalog) Delete(_ context.Context, workspaceKey, name string) error {
	f.workspaceKey, f.deleted = workspaceKey, name
	return f.err
}

func (f *fakeCatalog) Create(_ context.Context, input CreateInput) (*Reference, error) {
	f.createIn = input
	return f.created, f.createErr
}

func (f *fakeCatalog) GetByKey(_ context.Context, key string) (*Reference, error) {
	f.keyQuery = key
	return f.byKey, f.keyErr
}

func (f *fakeCatalog) GetByName(_ context.Context, name string) (*Reference, error) {
	f.nameQuery = name
	return f.byName, f.nameErr
}

func (f *fakeCatalog) List(context.Context) ([]Reference, error) {
	return f.listed, f.listErr
}

func (f *fakeCatalog) Rename(_ context.Context, key, name string) (*Reference, error) {
	f.renameKey, f.renameTo = key, name
	return f.updated, f.updateErr
}

func (f *fakeCatalog) SetDesignFormat(_ context.Context, key, format string) (*Reference, error) {
	f.formatKey, f.formatTo = key, format
	return f.updated, f.updateErr
}

func (f *fakeCatalog) SetLifecycle(_ context.Context, key string, update LifecycleUpdate) (*Reference, error) {
	f.lifecycleKey, f.lifecycleUpdate = key, update
	return f.updated, f.updateErr
}

func (f *fakeCatalog) Delete(_ context.Context, key string) error {
	f.deletedKey = key
	return f.deleteErr
}

func TestCreateOwnsValidationAndReturnsPersistedReference(t *testing.T) {
	catalog := &fakeCatalog{created: &Reference{Key: "HELLO", Name: "Hello"}}
	service, _ := New(catalog)
	value, err := service.Create(context.Background(), CreateCommand{
		Key: " HELLO ", Name: " Hello ", Description: " demo ",
		DefaultBranch: " main ", DesignFormat: " markdown ",
	})
	if err != nil || value.Key != "HELLO" {
		t.Fatalf("create value=%#v err=%v", value, err)
	}
	if catalog.createIn.Key != "HELLO" || catalog.createIn.Name != "Hello" ||
		catalog.createIn.Description != "demo" || catalog.createIn.DefaultBranch != "main" ||
		catalog.createIn.DesignFormat != DesignFormatMarkdown {
		t.Fatalf("create input=%#v", catalog.createIn)
	}
	if _, err := service.Create(context.Background(), CreateCommand{Name: "Hello"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing key err=%v", err)
	}
	if _, err := service.Create(context.Background(), CreateCommand{Key: "HELLO", Name: "bad name"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid name err=%v", err)
	}
	if _, err := service.Create(context.Background(), CreateCommand{Key: "HELLO", Name: "Hello", DesignFormat: "pdf"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid design format err=%v", err)
	}
}

func TestResolvePrefersKeyAndReturnsCopy(t *testing.T) {
	store := &fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}}
	service, _ := New(store)
	value, err := service.Resolve(context.Background(), ResolveQuery{Reference: " HELLO "})
	if err != nil {
		t.Fatal(err)
	}
	if store.keyQuery != "HELLO" || store.nameQuery != "" || value.Key != "HELLO" {
		t.Fatalf("unexpected resolution: store=%#v value=%#v", store, value)
	}
	value.Name = "mutated"
	if store.byKey.Name != "Hello" {
		t.Fatal("resolve leaked persisted reference")
	}
}

func TestResolveFallsBackToName(t *testing.T) {
	store := &fakeCatalog{keyErr: ErrNotFound, byName: &Reference{Key: "HELLO", Name: "Hello"}}
	service, _ := New(store)
	value, err := service.Resolve(context.Background(), ResolveQuery{Reference: "Hello"})
	if err != nil || value.Key != "HELLO" || store.nameQuery != "Hello" {
		t.Fatalf("unexpected name resolution: value=%#v err=%v store=%#v", value, err, store)
	}
}

func TestResolveRejectsEmptyAndInvalidPersistence(t *testing.T) {
	service, _ := New(&fakeCatalog{})
	if _, err := service.Resolve(context.Background(), ResolveQuery{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid reference, got %v", err)
	}
	if _, err := service.Resolve(context.Background(), ResolveQuery{Reference: "HELLO"}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid persisted state, got %v", err)
	}
}

func TestListReturnsDefensiveValidatedReferences(t *testing.T) {
	store := &fakeCatalog{listed: []Reference{{Key: "ONE", Name: "One"}, {Key: "TWO", Name: "Two"}}}
	service, _ := New(store)
	values, err := service.List(context.Background(), ListQuery{})
	if err != nil || len(values) != 2 || values[1].Key != "TWO" {
		t.Fatalf("unexpected list: values=%#v err=%v", values, err)
	}
	values[0].Name = "mutated"
	if store.listed[0].Name != "One" {
		t.Fatal("list leaked persisted reference")
	}

	store.listed = []Reference{{Key: "BROKEN"}}
	if _, err := service.List(context.Background(), ListQuery{}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid persisted state, got %v", err)
	}
}

func TestRenameOwnsValidationUniquenessAndNoOp(t *testing.T) {
	t.Run("updates canonical key", func(t *testing.T) {
		store := &fakeCatalog{
			byKey:   &Reference{Key: "HELLO", Name: "Hello"},
			nameErr: ErrNotFound,
			updated: &Reference{Key: "HELLO", Name: "Renamed"},
		}
		service, _ := New(store)
		value, err := service.Rename(context.Background(), RenameCommand{Reference: "HELLO", Name: " Renamed "})
		if err != nil || value.Name != "Renamed" || store.renameKey != "HELLO" || store.renameTo != "Renamed" {
			t.Fatalf("unexpected rename: value=%#v err=%v store=%#v", value, err, store)
		}
	})

	t.Run("rejects invalid and duplicate names", func(t *testing.T) {
		service, _ := New(&fakeCatalog{})
		if _, err := service.Rename(context.Background(), RenameCommand{Name: "not valid"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid name, got %v", err)
		}

		store := &fakeCatalog{
			byKey:  &Reference{Key: "HELLO", Name: "Hello"},
			byName: &Reference{Key: "OTHER", Name: "Taken"},
		}
		service, _ = New(store)
		if _, err := service.Rename(context.Background(), RenameCommand{Reference: "HELLO", Name: "Taken"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("expected conflict, got %v", err)
		}
	})

	t.Run("same name is idempotent", func(t *testing.T) {
		store := &fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}}
		service, _ := New(store)
		if _, err := service.Rename(context.Background(), RenameCommand{Reference: "HELLO", Name: "Hello"}); err != nil {
			t.Fatal(err)
		}
		if store.renameKey != "" {
			t.Fatal("idempotent rename wrote persistence")
		}
	})
}

func TestSetDesignFormatOwnsValidationAndNoOp(t *testing.T) {
	store := &fakeCatalog{
		byKey:   &Reference{Key: "HELLO", Name: "Hello", DesignFormat: DesignFormatMarkdown},
		updated: &Reference{Key: "HELLO", Name: "Hello", DesignFormat: DesignFormatHTML},
	}
	service, _ := New(store)
	value, err := service.SetDesignFormat(context.Background(), SetDesignFormatCommand{Reference: "HELLO", Format: " html "})
	if err != nil || value.DesignFormat != DesignFormatHTML || store.formatKey != "HELLO" || store.formatTo != DesignFormatHTML {
		t.Fatalf("unexpected update: value=%#v err=%v store=%#v", value, err, store)
	}
	if _, err := service.SetDesignFormat(context.Background(), SetDesignFormatCommand{Reference: "HELLO", Format: "svg"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid format, got %v", err)
	}

	store.byKey.DesignFormat = DesignFormatHTML
	store.updated.DesignFormat = ""
	value, err = service.SetDesignFormat(context.Background(), SetDesignFormatCommand{Reference: "HELLO", Format: ""})
	if err != nil || value.DesignFormat != "" || store.formatTo != "" {
		t.Fatalf("clear format value=%#v err=%v store=%#v", value, err, store)
	}

	store.byKey.DesignFormat = DesignFormatHTML
	store.formatKey = ""
	if _, err := service.SetDesignFormat(context.Background(), SetDesignFormatCommand{Reference: "HELLO", Format: DesignFormatHTML}); err != nil {
		t.Fatal(err)
	}
	if store.formatKey != "" {
		t.Fatal("idempotent design-format update wrote persistence")
	}
}

func TestSetLifecycleResolvesCanonicalKeyAndOwnsStateValidation(t *testing.T) {
	branch := " trunk "
	catalog := &fakeCatalog{
		keyErr: ErrNotFound, byName: &Reference{Key: "HELLO", Name: "Hello"},
		updated: &Reference{Key: "HELLO", Name: "Hello", State: StateReady, DefaultBranch: "trunk"},
	}
	service, _ := New(catalog)
	value, err := service.SetLifecycle(context.Background(), SetLifecycleCommand{
		Reference: "Hello", State: StateReady, ErrorMessage: " cleared ", DefaultBranch: &branch,
	})
	if err != nil || value.State != StateReady || catalog.lifecycleKey != "HELLO" {
		t.Fatalf("lifecycle value=%#v err=%v catalog=%#v", value, err, catalog)
	}
	if catalog.lifecycleUpdate.ErrorMessage != "cleared" || catalog.lifecycleUpdate.DefaultBranch == nil || *catalog.lifecycleUpdate.DefaultBranch != "trunk" {
		t.Fatalf("lifecycle update=%#v", catalog.lifecycleUpdate)
	}
	if _, err := service.SetLifecycle(context.Background(), SetLifecycleCommand{Reference: "Hello", State: "unknown"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid state err=%v", err)
	}
}

func TestDeleteResolvesCanonicalWorkspaceBeforeOwnerWrite(t *testing.T) {
	catalog := &fakeCatalog{keyErr: ErrNotFound, byName: &Reference{Key: "HELLO", Name: "Hello"}}
	service, err := New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := service.Delete(context.Background(), DeleteCommand{Reference: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.deletedKey != "HELLO" || deleted.Key != "HELLO" {
		t.Fatalf("deleted key=%q result=%#v", catalog.deletedKey, deleted)
	}
}

func TestRepositoryQueriesResolveWorkspaceAndDefensivelyCopy(t *testing.T) {
	repositories := &fakeRepositoryCatalog{
		value:  &Repository{WorkspaceKey: "HELLO", Name: "loom", Groups: []string{"core"}},
		values: []Repository{{WorkspaceKey: "HELLO", Name: "loom", Groups: []string{"core"}}},
	}
	service, err := New(
		&fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}},
		WithRepositoryCatalog(repositories),
	)
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.GetRepository(context.Background(), GetRepositoryQuery{WorkspaceReference: "HELLO", Name: " loom "})
	if err != nil || repositories.workspaceKey != "HELLO" || repositories.name != "loom" || value.Name != "loom" {
		t.Fatalf("unexpected get: value=%#v repositories=%#v err=%v", value, repositories, err)
	}
	value.Groups[0] = "mutated"
	if repositories.value.Groups[0] != "core" {
		t.Fatal("get leaked persisted repository groups")
	}

	values, err := service.ListRepositories(context.Background(), ListRepositoriesQuery{WorkspaceReference: "HELLO"})
	if err != nil || len(values) != 1 || values[0].Name != "loom" {
		t.Fatalf("unexpected list: values=%#v err=%v", values, err)
	}
	values[0].Groups[0] = "mutated"
	if repositories.values[0].Groups[0] != "core" {
		t.Fatal("list leaked persisted repository groups")
	}
}

func TestRepositoryQueriesFailClosedForMissingPortAndInvalidOwnership(t *testing.T) {
	service, _ := New(&fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}})
	if _, err := service.ListRepositories(context.Background(), ListRepositoriesQuery{WorkspaceReference: "HELLO"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable repository catalog, got %v", err)
	}

	repositories := &fakeRepositoryCatalog{value: &Repository{WorkspaceKey: "OTHER", Name: "loom"}}
	service, _ = New(
		&fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}},
		WithRepositoryCatalog(repositories),
	)
	if _, err := service.GetRepository(context.Background(), GetRepositoryQuery{WorkspaceReference: "HELLO", Name: "loom"}); !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("expected invalid persisted ownership, got %v", err)
	}
}

func TestRepositoryCommandsResolveWorkspaceAndOwnDefensiveInputs(t *testing.T) {
	repositories := &fakeRepositoryCatalog{value: &Repository{
		WorkspaceKey: "HELLO", Name: "loom", Groups: []string{"core"},
	}}
	service, err := New(
		&fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}},
		WithRepositoryCatalog(repositories),
	)
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"core"}
	value, err := service.RegisterRepository(context.Background(), RegisterRepositoryCommand{
		WorkspaceReference: "HELLO", Name: " loom ", RemoteURL: " git@example ",
		Remote: " origin ", DefaultBranch: " main ", Groups: groups, SourceRepoID: " source ",
	})
	if err != nil || value.Name != "loom" {
		t.Fatalf("register value=%#v err=%v", value, err)
	}
	groups[0] = "mutated"
	if repositories.created.WorkspaceKey != "HELLO" || repositories.created.Name != "loom" ||
		repositories.created.Groups[0] != "core" || repositories.created.RemoteURL != "git@example" {
		t.Fatalf("repository input=%#v", repositories.created)
	}
	branch := " trunk "
	updatedGroups := []string{"docs"}
	updated, err := service.UpdateRepository(context.Background(), UpdateRepositoryCommand{
		WorkspaceReference: "HELLO", Name: " loom ", DefaultBranch: &branch, Groups: &updatedGroups,
	})
	if err != nil || updated.Name != "loom" {
		t.Fatalf("update value=%#v err=%v", updated, err)
	}
	updatedGroups[0] = "mutated"
	if repositories.updated.DefaultBranch == nil || *repositories.updated.DefaultBranch != "trunk" ||
		repositories.updated.Groups == nil || (*repositories.updated.Groups)[0] != "docs" {
		t.Fatalf("repository update=%#v", repositories.updated)
	}
	deleted, err := service.UnregisterRepository(context.Background(), UnregisterRepositoryCommand{
		WorkspaceReference: "HELLO", Name: "loom",
	})
	if err != nil || deleted.Name != "loom" || repositories.deleted != "loom" {
		t.Fatalf("unregister value=%#v err=%v repositories=%#v", deleted, err, repositories)
	}
}

func TestUpdateRepositoryRejectsMissingPortAndName(t *testing.T) {
	service, _ := New(&fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}})
	if _, err := service.UpdateRepository(context.Background(), UpdateRepositoryCommand{WorkspaceReference: "HELLO", Name: "loom"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable repository catalog, got %v", err)
	}

	service, _ = New(
		&fakeCatalog{byKey: &Reference{Key: "HELLO", Name: "Hello"}},
		WithRepositoryCatalog(&fakeRepositoryCatalog{}),
	)
	if _, err := service.UpdateRepository(context.Background(), UpdateRepositoryCommand{WorkspaceReference: "HELLO"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid repository name, got %v", err)
	}
	if _, err := service.UpdateRepository(context.Background(), UpdateRepositoryCommand{WorkspaceReference: "HELLO", Name: "loom"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid empty update, got %v", err)
	}
}
