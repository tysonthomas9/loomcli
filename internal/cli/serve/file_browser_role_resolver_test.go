package serve

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// TestBuildFileBrowserRoleResolver covers buildFileBrowserRoleResolver, which
// turns LOOM_FILE_BROWSER_DEFAULT_ROLE into a WorkspaceRoleResolver. It is the
// security-sensitive knob that grants EVERY authenticated user a fixed file
// role, so both the fail-closed paths (unset / unknown -> nil) and the enabled
// path (valid role -> resolver returning that role) must stay verified.
func TestBuildFileBrowserRoleResolver(t *testing.T) {
	t.Run("unset disables remote file access", func(t *testing.T) {
		t.Setenv("LOOM_FILE_BROWSER_DEFAULT_ROLE", "")
		if r := buildFileBrowserRoleResolver(); r != nil {
			t.Fatalf("resolver = %v, want nil when env unset", r)
		}
	})

	t.Run("whitespace-only disables remote file access", func(t *testing.T) {
		t.Setenv("LOOM_FILE_BROWSER_DEFAULT_ROLE", "   ")
		if r := buildFileBrowserRoleResolver(); r != nil {
			t.Fatalf("resolver = %v, want nil when env is whitespace", r)
		}
	})

	t.Run("unknown role fails closed", func(t *testing.T) {
		t.Setenv("LOOM_FILE_BROWSER_DEFAULT_ROLE", "superuser")
		if r := buildFileBrowserRoleResolver(); r != nil {
			t.Fatalf("resolver = %v, want nil for unrecognized role", r)
		}
	})

	t.Run("valid role resolves to that role for any identity", func(t *testing.T) {
		for _, in := range []struct{ env, want string }{
			{env: "viewer", want: "viewer"},
			{env: "editor", want: "editor"},
			{env: "  VIEWER  ", want: "viewer"}, // trimmed + lowercased by the builder
		} {
			t.Run(in.env, func(t *testing.T) {
				t.Setenv("LOOM_FILE_BROWSER_DEFAULT_ROLE", in.env)
				resolver := buildFileBrowserRoleResolver()
				if resolver == nil {
					t.Fatalf("resolver = nil, want non-nil for role %q", in.env)
				}
				// Identity and workspace are ignored: this policy is deployment-wide.
				got, err := resolver(context.Background(), "any-workspace", middleware.UserIdentity{UserID: "user-1"})
				if err != nil {
					t.Fatalf("resolver returned error: %v", err)
				}
				if got != in.want {
					t.Fatalf("resolved role = %q, want %q", got, in.want)
				}
				if !middleware.KnownFileRole(got) {
					t.Fatalf("resolved role %q is not a known file role", got)
				}
			})
		}
	})
}
