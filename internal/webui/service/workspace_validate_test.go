package service

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
