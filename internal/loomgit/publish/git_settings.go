package publish

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// GitSettings are the workspace settings that pick the D29 review flow.
type GitSettings struct {
	DeliveryMode          string `json:"delivery_mode"`
	LeadMayApprovePublish bool   `json:"lead_may_approve_publish"`
	LeadMayMerge          string `json:"lead_may_merge"`
}

// GitSettingsChange names only the settings to change.
type GitSettingsChange struct {
	DeliveryMode          *string `json:"delivery_mode,omitempty"`
	LeadMayApprovePublish *bool   `json:"lead_may_approve_publish,omitempty"`
	LeadMayMerge          *string `json:"lead_may_merge,omitempty"`
}

func GitSettingsLocal(ctx context.Context, workspace string) (GitSettings, error) {
	store, err := openLocalStore()
	if err != nil {
		return GitSettings{}, err
	}
	defer func() { _ = store.Close() }()
	return readGitSettings(ctx, store, workspace)
}

// SetGitSettingsLocal applies a change and returns the new settings and any
// lead_may_merge warning.
func SetGitSettingsLocal(ctx context.Context, workspace string, change GitSettingsChange, actor review.Actor,
	env []string) (GitSettings, string, error) {
	store, err := openLocalStore()
	if err != nil {
		return GitSettings{}, "", err
	}
	defer func() { _ = store.Close() }()
	warning, err := SetGitSettings(ctx, store, workspace, change, actor, env)
	if err != nil {
		return GitSettings{}, "", err
	}
	settings, err := readGitSettings(ctx, store, workspace)
	return settings, warning, err
}

// SetGitSettings lets the human or the lead change delivery mode; the two lead
// permissions are human only (advisory in local mode, D28). Nothing is written
// unless the whole change is allowed and valid.
func SetGitSettings(ctx context.Context, store *journal.SQLite, workspace string, change GitSettingsChange,
	actor review.Actor, env []string) (string, error) {
	if change.DeliveryMode != nil {
		if actor.Kind != "human" && actor.Kind != "lead" {
			return "", loomgit.NewError(loomgit.MergeNotAuthorized, "only a human or the lead can change delivery mode", nil)
		}
		if *change.DeliveryMode != "stack" && *change.DeliveryMode != "trunk" {
			return "", fmt.Errorf("invalid delivery mode %q", *change.DeliveryMode)
		}
	}
	if change.LeadMayApprovePublish != nil || change.LeadMayMerge != nil {
		if err := requireHumanPolicyActor(actor, env); err != nil {
			return "", err
		}
	}
	if change.LeadMayMerge != nil && *change.LeadMayMerge != "off" && *change.LeadMayMerge != "when_green" {
		return "", fmt.Errorf("invalid lead_may_merge %q", *change.LeadMayMerge)
	}
	if change.DeliveryMode != nil {
		if err := store.SetDeliveryMode(ctx, workspace, *change.DeliveryMode); err != nil {
			return "", err
		}
	}
	if change.LeadMayApprovePublish != nil {
		if err := store.SetLeadMayApprovePublish(ctx, workspace, *change.LeadMayApprovePublish); err != nil {
			return "", err
		}
	}
	if change.LeadMayMerge == nil {
		return "", nil
	}
	return SetWorkspacePolicy(ctx, store, workspace, *change.LeadMayMerge, actor, env)
}

func readGitSettings(ctx context.Context, store *journal.SQLite, workspace string) (GitSettings, error) {
	mode, err := store.DeliveryMode(ctx, workspace)
	if err != nil {
		return GitSettings{}, err
	}
	approve, err := store.LeadMayApprovePublish(ctx, workspace)
	if err != nil {
		return GitSettings{}, err
	}
	merge, err := store.LeadMayMerge(ctx, workspace)
	if err != nil {
		return GitSettings{}, err
	}
	return GitSettings{DeliveryMode: mode, LeadMayApprovePublish: approve, LeadMayMerge: merge.Value}, nil
}
