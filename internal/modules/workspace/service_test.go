package workspace

import (
	"context"
	"errors"
	"testing"
)

type fakeCatalog struct {
	byKey     *Reference
	byName    *Reference
	listed    []Reference
	updated   *Reference
	keyErr    error
	nameErr   error
	listErr   error
	updateErr error
	keyQuery  string
	nameQuery string
	renameKey string
	renameTo  string
	formatKey string
	formatTo  string
}

type fakeRepositoryCatalog struct {
	value        *Repository
	values       []Repository
	err          error
	workspaceKey string
	name         string
}

func (f *fakeRepositoryCatalog) Get(_ context.Context, workspaceKey, name string) (*Repository, error) {
	f.workspaceKey, f.name = workspaceKey, name
	return f.value, f.err
}

func (f *fakeRepositoryCatalog) List(_ context.Context, workspaceKey string) ([]Repository, error) {
	f.workspaceKey = workspaceKey
	return f.values, f.err
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
	store.formatKey = ""
	if _, err := service.SetDesignFormat(context.Background(), SetDesignFormatCommand{Reference: "HELLO", Format: DesignFormatHTML}); err != nil {
		t.Fatal(err)
	}
	if store.formatKey != "" {
		t.Fatal("idempotent design-format update wrote persistence")
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
