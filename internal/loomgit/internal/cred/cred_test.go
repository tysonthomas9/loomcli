package cred

import "testing"

func TestSourceIsOneShotAndRepoScoped(t *testing.T) {
	s := New("https://github.com/owner/repo.git", "secret")
	for _, other := range []string{
		"https://github.com/owner/other.git",
		"https://github.com/owner/repo.git/evil",
		"http://github.com/owner/repo.git",
		"https://attacker.example/owner/repo.git",
		"https://github.com/owner/repo.git?x=1",
	} {
		if _, err := s.Take(other); err == nil {
			t.Fatalf("accepted foreign URL %q", other)
		}
	}
	got, err := s.Take("https://github.com/owner/repo.git")
	if err != nil || got != "secret" {
		t.Fatalf("Take intended URL = %q, %v", got, err)
	}
	if _, err := s.Take("https://github.com/owner/repo.git"); err == nil {
		t.Fatal("credential was served twice")
	}
}
