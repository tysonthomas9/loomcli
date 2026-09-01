package fleetdb

import (
	"context"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	skillMaterializationLeaseConflictCode         = "skill_materialization_lease_conflict"
	skillMaterializationLeaseTokenMismatchCode    = "skill_materialization_lease_token_mismatch"
	skillMaterializationLeaseStoreUnavailableCode = "skill_materialization_lease_store_unavailable"
)

type skillMaterializationLeaseStore struct{ client *Client }

var _ store.SkillMaterializationLeaseStore = (*skillMaterializationLeaseStore)(nil)

type acquireSkillMaterializationLeaseBody struct {
	Holder        string   `json:"holder"`
	TargetKey     string   `json:"target_key"`
	TreeRevisions []string `json:"tree_revisions"`
	TTLSeconds    int      `json:"ttl_seconds,omitempty"`
}

type renewSkillMaterializationLeaseBody struct {
	Token      string `json:"token"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type releaseSkillMaterializationLeaseBody struct {
	Token string `json:"token"`
}

func (s *skillMaterializationLeaseStore) Acquire(ctx context.Context, in store.SkillMaterializationLeaseAcquire) (*domain.SkillMaterializationLease, error) {
	body := acquireSkillMaterializationLeaseBody{
		Holder: in.Holder, TargetKey: in.TargetKey,
		TreeRevisions: append([]string{}, in.TreeRevisions...), TTLSeconds: ttlSeconds(in.TTL),
	}
	var out domain.SkillMaterializationLease
	path := "/api/v1/" + pathEscape(in.WorkspaceKey) + "/skill-materialization-leases"
	if _, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPost, path, body, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *skillMaterializationLeaseStore) Renew(ctx context.Context, ws, targetKey, token string, ttl time.Duration) (time.Time, error) {
	body := renewSkillMaterializationLeaseBody{Token: token, TTLSeconds: ttlSeconds(ttl)}
	var out struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := skillMaterializationLeasePath(ws, targetKey)
	if _, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPut, path, body, &out, nil); err != nil {
		return time.Time{}, err
	}
	return out.ExpiresAt, nil
}

func (s *skillMaterializationLeaseStore) Release(ctx context.Context, ws, targetKey, token string) error {
	_, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodDelete,
		skillMaterializationLeasePath(ws, targetKey), releaseSkillMaterializationLeaseBody{Token: token}, nil, nil)
	return err
}

func skillMaterializationLeasePath(ws, targetKey string) string {
	return "/api/v1/" + pathEscape(ws) + "/skill-materialization-leases/" + pathEscape(targetKey)
}

func skillMaterializationLeaseConflictError(prefix string, body []byte) error {
	meta := extractErrorMeta(body)
	expiresAt, _ := time.Parse(time.RFC3339Nano, meta["expires_at"])
	return &domain.SkillMaterializationLeaseConflictError{
		Message: prefix, Holder: meta["holder"], ExpiresAt: expiresAt,
	}
}
