package workspacecoord

import "testing"

func TestValidateCloneURLRejectsHTTPSUserinfo(t *testing.T) {
	for _, cloneURL := range []string{
		"https://user@github.com/example/repo.git",
		"https://user:secret@github.com/example/repo.git",
	} {
		t.Run(cloneURL, func(t *testing.T) {
			if err := ValidateCloneURL(cloneURL); err == nil {
				t.Fatal("clone URL userinfo was accepted")
			}
		})
	}
}

func TestValidateCloneURLAcceptsTokenFreeGitHubHTTPS(t *testing.T) {
	if err := ValidateCloneURL("https://github.com/example/repo.git"); err != nil {
		t.Fatalf("token-free GitHub URL rejected: %v", err)
	}
}

func TestWorkspaceAddReposRequiresCloneRecognizesExplicitAndLegacyURLs(t *testing.T) {
	tests := []struct {
		name string
		req  WorkspaceAddReposRequest
		want bool
	}{
		{
			name: "explicit clone URL",
			req:  WorkspaceAddReposRequest{CloneURLs: []string{"https://github.com/acme/repo.git"}},
			want: true,
		},
		{
			name: "legacy URL in repos",
			req:  WorkspaceAddReposRequest{Repos: []string{" git@github.com:acme/repo.git "}},
			want: true,
		},
		{
			name: "local path",
			req:  WorkspaceAddReposRequest{Repos: []string{"/workspace/repo"}},
			want: false,
		},
		{
			name: "blank clone URL",
			req:  WorkspaceAddReposRequest{CloneURLs: []string{"  "}, Repos: []string{"/workspace/repo"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorkspaceAddReposRequiresClone(tt.req); got != tt.want {
				t.Fatalf("WorkspaceAddReposRequiresClone() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWorkspaceCloneAdmissionRejectsHTTPSQueryAndFragment(t *testing.T) {
	for _, cloneURL := range []string{
		"https://github.com/example/repo.git?access_token=query-secret",
		"https://github.com/example/repo.git#fragment-secret",
	} {
		t.Run(cloneURL, func(t *testing.T) {
			if err := ValidateCloneURL(cloneURL); err == nil {
				t.Fatal("clone URL query or fragment was accepted")
			}
			create := WorkspaceCreateRequest{
				Name:      "test-workspace",
				Type:      "clone",
				CloneURLs: []string{cloneURL},
			}
			if err := validateWorkspaceCreateRequest(&create); err == nil {
				t.Fatal("workspace create accepted clone URL query or fragment")
			}
			add := WorkspaceAddReposRequest{
				WorkspaceID: "TEST",
				CloneURLs:   []string{cloneURL},
			}
			if err := validateWorkspaceAddReposRequest(&add); err == nil {
				t.Fatal("workspace add-repo accepted clone URL query or fragment")
			}
		})
	}
}
