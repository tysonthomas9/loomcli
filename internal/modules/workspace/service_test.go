package workspace

import (
	"context"
	"errors"
	"testing"
)

type fakeCatalog struct {
	byKey     *Reference
	byName    *Reference
	keyErr    error
	nameErr   error
	keyQuery  string
	nameQuery string
}

func (f *fakeCatalog) GetByKey(_ context.Context, key string) (*Reference, error) {
	f.keyQuery = key
	return f.byKey, f.keyErr
}

func (f *fakeCatalog) GetByName(_ context.Context, name string) (*Reference, error) {
	f.nameQuery = name
	return f.byName, f.nameErr
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
