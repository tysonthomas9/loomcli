package gitauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

func TestLocalSettingsSourceResolvesFreshGitHubCredential(t *testing.T) {
	dataDir := t.TempDir()
	saveGitHubCredential(t, dataDir, "github-token-first", time.Now())
	source := LocalSettingsSource{DataDir: dataDir}

	first, err := source.Resolve(context.Background(), "https://github.com/acme/private.git")
	if err != nil {
		t.Fatalf("resolve first credential: %v", err)
	}
	if first == nil || first.Username != "x-access-token" || string(first.Password) != "github-token-first" {
		t.Fatalf("first credential = %#v", first)
	}
	first.Close()

	saveGitHubCredential(t, dataDir, "github-token-rotated", time.Now().Add(time.Minute))
	second, err := source.Resolve(context.Background(), "https://github.com/acme/private.git")
	if err != nil {
		t.Fatalf("resolve rotated credential: %v", err)
	}
	if second == nil || string(second.Password) != "github-token-rotated" {
		t.Fatalf("rotated credential = %#v", second)
	}
	second.Close()
}

func TestLocalSettingsSourceLeavesNonGitHubAndSSHRemotesUntouched(t *testing.T) {
	dataDir := t.TempDir()
	saveGitHubCredential(t, dataDir, "github-token-private", time.Now())
	source := LocalSettingsSource{DataDir: dataDir}

	for _, remote := range []string{
		"/tmp/repo.git",
		"file:///tmp/repo.git",
		"git@github.com:acme/private.git",
		"ssh://git@github.com/acme/private.git",
		"https://gitlab.com/acme/private.git",
		"https://github.com.evil.example/acme/private.git",
	} {
		credential, err := source.Resolve(context.Background(), remote)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", remote, err)
		}
		if credential != nil {
			credential.Close()
			t.Fatalf("Resolve(%q) returned a credential", remote)
		}
	}
}

func TestLocalSettingsSourceNoConfiguredCredentialUsesAnonymousGit(t *testing.T) {
	source := LocalSettingsSource{DataDir: t.TempDir()}
	credential, err := source.Resolve(context.Background(), "https://github.com/acme/public.git")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if credential != nil {
		credential.Close()
		t.Fatal("Resolve returned a credential when Settings has none")
	}
}

func TestLocalSettingsSourceRejectsCredentialProtocolInjection(t *testing.T) {
	dataDir := t.TempDir()
	saveGitHubCredential(t, dataDir, "github-token\npassword=attacker", time.Now())
	_, err := (LocalSettingsSource{DataDir: dataDir}).Resolve(
		context.Background(),
		"https://github.com/acme/private.git",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid control characters") {
		t.Fatalf("Resolve error = %v, want invalid-control-character error", err)
	}
	if strings.Contains(err.Error(), "github-token") || strings.Contains(err.Error(), "attacker") {
		t.Fatalf("Resolve error leaked credential: %v", err)
	}
}

func TestLocalSettingsSourceRejectsURLQueryAndFragment(t *testing.T) {
	dataDir := t.TempDir()
	saveGitHubCredential(t, dataDir, "github-token-private", time.Now())
	source := LocalSettingsSource{DataDir: dataDir}

	for _, remoteURL := range []string{
		"https://github.com/acme/private.git?access_token=query-secret",
		"https://github.com/acme/private.git#fragment-secret",
	} {
		t.Run(remoteURL, func(t *testing.T) {
			credential, err := source.Resolve(context.Background(), remoteURL)
			if credential != nil {
				credential.Close()
				t.Fatal("Resolve returned a credential for URL carrying a query or fragment")
			}
			if err == nil || !strings.Contains(err.Error(), "forbidden") {
				t.Fatalf("Resolve error = %v, want forbidden URL component", err)
			}
			if strings.Contains(err.Error(), "query-secret") ||
				strings.Contains(err.Error(), "fragment-secret") {
				t.Fatalf("Resolve error reflected URL secret: %v", err)
			}
		})
	}
}

func saveGitHubCredential(t *testing.T, dataDir, token string, now time.Time) {
	t.Helper()
	credential, err := localsettings.SealRuntimeCredential(
		dataDir,
		localsettings.RuntimeCredentialProviderGitHub,
		token,
		now,
	)
	if err != nil {
		t.Fatalf("seal GitHub credential: %v", err)
	}
	settings := localsettings.Default()
	settings.RuntimeCredentials.GitHub = credential
	if err := localsettings.Save(dataDir, settings); err != nil {
		t.Fatalf("save GitHub credential: %v", err)
	}
}
