package repo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// `loom repo set <NAME> <FIELD> <VALUE>` (D-54).
//
// store.RepoUpdate has supported every one of these fields since it existed;
// the CLI exposed none of them, so fixing a typo'd remote URL or moving a repo
// to a different default branch meant `repo remove` + `repo add` — which
// discards the local checkout the add path clones and re-registers the repo
// under a fresh identity. Same shape as `role set` and `agentdef update`: a
// closed field vocabulary, validated client-side so a typo names the accepted
// list instead of round-tripping into a server 400.

var repoSetCmd = &cobra.Command{
	Use:   "set <NAME> <FIELD> <VALUE>",
	Short: "Set a single field on an existing repo",
	Long: `Set a repo field by name. Supported fields:
  remote_url      git remote URL (may not be blanked — a repo without one cannot be cloned)
  remote          git remote name (empty restores the "origin" default)
  default_branch  default branch (empty restores the "main" default)
  groups          logical groups, comma-separated (empty clears them)
  source_repo_id  source repo id used for issue routing (empty restores the repo name)

Examples:
  loom repo set app remote_url git@github.com:acme/app.git
  loom repo set app default_branch v5
  loom repo set app groups backend,infra
  loom repo set app groups ""            # clear`,
	Args: cobra.ExactArgs(3),
	RunE: runRepoSet,
}

// repoSetFields is the closed vocabulary, kept as data so the error message
// and the help text cannot drift from what the switch accepts.
var repoSetFields = map[string]string{
	"remote_url":     "git remote URL",
	"remote":         "git remote name",
	"default_branch": "default branch",
	"groups":         "logical groups (comma-separated)",
	"source_repo_id": "source repo id for issue routing",
}

func runRepoSet(_ *cobra.Command, args []string) error {
	name, field, value := args[0], args[1], args[2]
	patch, err := buildRepoPatch(field, value)
	if err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		if _, err := h.Store.Repos().Update(ctx, ws, name, patch); err != nil {
			return fmt.Errorf("update repo: %w", err)
		}
		if value == "" {
			fmt.Printf("Cleared %s/%s.%s\n", ws, name, field)
		} else {
			fmt.Printf("Set %s/%s.%s = %s\n", ws, name, field, value)
		}
		return nil
	})
}

// buildRepoPatch maps one field/value pair onto a store patch. Split from the
// command so the vocabulary is testable without a store.
func buildRepoPatch(field, value string) (store.RepoUpdate, error) {
	patch := store.RepoUpdate{}
	switch field {
	case "remote_url":
		// The one field with no meaningful empty state: a repo whose remote
		// URL is blank cannot be cloned or pushed, and the add path requires
		// one. Refuse rather than persist a repo that looks registered and
		// fails at first use.
		if strings.TrimSpace(value) == "" {
			return store.RepoUpdate{}, fmt.Errorf("remote_url cannot be empty; pass a git URL (or `repo remove` the repo)")
		}
		patch.RemoteURL = &value
	case "remote":
		patch.Remote = &value
	case "default_branch":
		patch.DefaultBranch = &value
	case "groups":
		groups := splitGroups(value)
		patch.Groups = &groups
	case "source_repo_id":
		patch.SourceRepoID = &value
	default:
		return store.RepoUpdate{}, fmt.Errorf("unknown repo field %q; accepted: %s", field, strings.Join(repoSetFieldNames(), ", "))
	}
	return patch, nil
}

// splitGroups parses the comma-separated group list, dropping blanks so
// "a,,b" and a trailing comma do not persist empty group names. An empty
// value yields an empty (non-nil) slice: the patch clears the groups.
func splitGroups(value string) []string {
	groups := []string{}
	for _, g := range strings.Split(value, ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

func repoSetFieldNames() []string {
	names := make([]string, 0, len(repoSetFields))
	for f := range repoSetFields {
		names = append(names, f)
	}
	sort.Strings(names)
	return names
}
