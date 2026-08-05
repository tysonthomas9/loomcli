package admissionstore

import (
	"reflect"
	"slices"
	"testing"
)

func TestStoreFacetSetIsExact(t *testing.T) {
	storeType := reflect.TypeOf((*Store)(nil)).Elem()
	got := make([]string, 0, storeType.NumMethod())
	for index := 0; index < storeType.NumMethod(); index++ {
		got = append(got, storeType.Method(index).Name)
	}
	want := []string{"AgentServices", "Repos", "Roles", "Workspaces"}
	if !slices.Equal(got, want) {
		t.Fatalf("Workspace admission Store facets = %v, want %v", got, want)
	}
}
