package workspace

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

func TestValidDesignFormat(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "", want: true},
		{input: "markdown", want: true},
		{input: "html", want: true},
		{input: "HTML", want: false},
		{input: "Markdown", want: false},
		{input: "yaml", want: false},
		{input: "htm", want: false},
		{input: " html", want: false},
	}
	for _, tc := range tests {
		t.Run("input="+tc.input, func(t *testing.T) {
			if got := validDesignFormat(tc.input); got != tc.want {
				t.Errorf("validDesignFormat(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// newSetCmdForTest builds a fresh cobra command bound to the package-level
// wsSetDesignFormat flag var, mirroring workspaceSetCmd's flag registration.
// A fresh command per test keeps the FlagSet's Changed state isolated.
func newSetCmdForTest(t *testing.T) *cobra.Command {
	t.Helper()
	orig := wsSetDesignFormat
	t.Cleanup(func() { wsSetDesignFormat = orig })
	cmd := &cobra.Command{Use: "set <KEY>"}
	cmd.Flags().StringVar(&wsSetDesignFormat, "design-format", "", "")
	return cmd
}

func TestRunWorkspaceSet_NoFlags(t *testing.T) {
	cmd := newSetCmdForTest(t)
	err := runWorkspaceSet(cmd, []string{"MYWS"})
	if err == nil {
		t.Fatal("expected error when no flags are passed")
	}
	if !strings.Contains(err.Error(), "nothing to set") {
		t.Errorf("error = %q, want mention of 'nothing to set'", err)
	}
}

func TestRunWorkspaceSet_InvalidDesignFormat(t *testing.T) {
	for _, bad := range []string{"yaml", "HTML", "rich-text"} {
		t.Run(bad, func(t *testing.T) {
			cmd := newSetCmdForTest(t)
			if err := cmd.Flags().Set("design-format", bad); err != nil {
				t.Fatal(err)
			}
			err := runWorkspaceSet(cmd, []string{"MYWS"})
			if err == nil {
				t.Fatalf("expected error for --design-format %q", bad)
			}
			if !strings.Contains(err.Error(), "invalid --design-format") {
				t.Errorf("error = %q, want mention of 'invalid --design-format'", err)
			}
		})
	}
}

// TestWorkspaceSetPersistsDesignFormat verifies the owner command persists
// design-format changes, including clearing via --design-format "".
func TestWorkspaceSetPersistsDesignFormat(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.Workspaces().Create(ctx, workspacemodule.WorkspaceCreate{Key: "MYWS", Name: "MYWS"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspace, err := workspacemodule.NewFromRecordStores(st.Workspaces(), st.Repos())
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{"html", "markdown", ""} {
		if _, err := workspace.SetDesignFormat(ctx, workspacemodule.SetDesignFormatCommand{Reference: "MYWS", Format: v}); err != nil {
			t.Fatalf("update design_format=%q: %v", v, err)
		}
		got, err := st.Workspaces().Get(ctx, "MYWS")
		if err != nil {
			t.Fatalf("get workspace: %v", err)
		}
		if got.DesignFormat != v {
			t.Errorf("DesignFormat = %q, want %q", got.DesignFormat, v)
		}
	}
}

func TestRunWorkspaceAdd_InvalidDesignFormat(t *testing.T) {
	orig := wsAddDesignFormat
	t.Cleanup(func() { wsAddDesignFormat = orig })
	wsAddDesignFormat = "yaml"

	err := runWorkspaceAdd(nil, []string{"MYWS"})
	if err == nil {
		t.Fatal("expected error for invalid --design-format on add")
	}
	if !strings.Contains(err.Error(), "invalid --design-format") {
		t.Errorf("error = %q, want mention of 'invalid --design-format'", err)
	}
}

func TestDisplayDesignFormat(t *testing.T) {
	if got := displayDesignFormat(""); got != "markdown (default)" {
		t.Errorf("displayDesignFormat(\"\") = %q, want 'markdown (default)'", got)
	}
	if got := displayDesignFormat("html"); got != "html" {
		t.Errorf("displayDesignFormat(\"html\") = %q, want html", got)
	}
}
