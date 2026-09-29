package service

import "testing"

func TestP120CreateAcceptsAndValidatesMixedRepositories(t *testing.T) {
	good := WorkspaceCreateRequest{Name: "mixed", Type: "empty", Repos: []string{"/some/repo"}, CloneURLs: []string{"https://github.com/example/api.git"}}
	if err := validateWorkspaceCreateRequest(&good); err != nil {
		t.Fatalf("mixed create rejected: %v", err)
	}
	bad := good
	bad.CloneURLs = []string{"file:///private/repo"}
	if err := validateWorkspaceCreateRequest(&bad); err == nil {
		t.Fatal("unsafe clone URL accepted")
	}
}
