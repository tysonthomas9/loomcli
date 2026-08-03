package agents

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RuntimeMetadata is the typed placement policy carried by a canonical Agent.
// The Agent remains the identity owner; Source Control and Interaction consume
// this projection without reading the retired supervised-assignment record.
type RuntimeMetadata struct {
	RoleKind         string
	Backend          string
	FallbackBackends []string
	Repos            []string
	RepoGroups       []string
	CrossRepo        bool
	Auto             bool
}

// RuntimeIdentity is the canonical, secret-free Agent projection consumed by
// Interaction and Source Control launch adapters. It is derived exclusively
// from the Agents aggregate and its validated runtime metadata; it must never
// be reconstructed from the retired supervised-assignment record.
type RuntimeIdentity struct {
	WorkspaceKey     string
	AgentID          string
	RoleName         string
	Auto             bool
	Backend          string
	FallbackBackends []string
	Repos            []string
	RepoGroups       []string
	CrossRepo        bool
	MaxInstances     int
	BudgetPolicy     string
	DesiredState     DesiredState
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ResolveRuntimeIdentity validates and projects one canonical Agent for a
// runtime consumer. The returned slices never alias persisted metadata.
func ResolveRuntimeIdentity(record *Agent) (*RuntimeIdentity, error) {
	if record == nil {
		return nil, ErrInvalidPersistedState
	}
	runtime, err := ParseRuntimeMetadata(record.Metadata)
	if err != nil {
		return nil, err
	}
	return &RuntimeIdentity{
		WorkspaceKey: record.WorkspaceKey,
		AgentID:      record.AgentID,
		RoleName:     record.Behavior.RoleName,
		Auto:         runtime.Auto,
		Backend:      runtime.Backend,
		FallbackBackends: append(
			[]string(nil), runtime.FallbackBackends...,
		),
		Repos:        append([]string(nil), runtime.Repos...),
		RepoGroups:   append([]string(nil), runtime.RepoGroups...),
		CrossRepo:    runtime.CrossRepo,
		MaxInstances: record.MaxInstances,
		BudgetPolicy: record.BudgetPolicy,
		DesiredState: record.DesiredState,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}, nil
}

const (
	MetadataBackend          = "backend"
	MetadataRoleKind         = "loom.agent.role_kind"
	MetadataFallbackBackends = "loom.agent.fallback_backends"
	MetadataRepos            = "loom.agent.repos"
	MetadataRepoGroups       = "loom.agent.repo_groups"
	MetadataCrossRepo        = "loom.agent.cross_repo"
	MetadataAuto             = "loom.agent.auto"
)

// WithRuntimeMetadata returns a copy of base with a canonical, secret-free
// runtime projection. Empty slices are encoded explicitly so workspace-wide
// scope cannot be confused with missing migration data.
func WithRuntimeMetadata(base map[string]string, runtime RuntimeMetadata) (map[string]string, error) {
	out := cloneMetadata(base)
	roleKind := strings.TrimSpace(runtime.RoleKind)
	backend := strings.TrimSpace(runtime.Backend)
	if roleKind != runtime.RoleKind || backend != runtime.Backend {
		return nil, fmt.Errorf("runtime metadata must be canonical: %w", ErrInvalid)
	}
	fallback, err := encodeCanonicalStringList("fallback backend", runtime.FallbackBackends)
	if err != nil {
		return nil, err
	}
	repos, err := encodeCanonicalStringList("repository", runtime.Repos)
	if err != nil {
		return nil, err
	}
	groups, err := encodeCanonicalStringList("repository group", runtime.RepoGroups)
	if err != nil {
		return nil, err
	}
	out[MetadataRoleKind] = roleKind
	out[MetadataBackend] = backend
	out[MetadataFallbackBackends] = fallback
	out[MetadataRepos] = repos
	out[MetadataRepoGroups] = groups
	out[MetadataCrossRepo] = strconv.FormatBool(runtime.CrossRepo)
	out[MetadataAuto] = strconv.FormatBool(runtime.Auto)
	return out, nil
}

// ParseRuntimeMetadata validates and decodes the canonical runtime projection.
// A record without the namespaced fields is valid and represents a managed
// workflow Agent rather than an interactive/worktree Agent.
func ParseRuntimeMetadata(metadata map[string]string) (RuntimeMetadata, error) {
	out := RuntimeMetadata{Backend: strings.TrimSpace(metadata[MetadataBackend])}
	if value, present := metadata[MetadataBackend]; present && value != out.Backend {
		return RuntimeMetadata{}, fmt.Errorf("backend metadata is not canonical: %w", ErrInvalidPersistedState)
	}
	if !hasRuntimeMetadata(metadata) {
		return out, nil
	}
	out.RoleKind = strings.TrimSpace(metadata[MetadataRoleKind])
	if out.RoleKind != metadata[MetadataRoleKind] {
		return RuntimeMetadata{}, fmt.Errorf("role kind metadata is not canonical: %w", ErrInvalidPersistedState)
	}
	var err error
	if out.FallbackBackends, err = decodeCanonicalStringList("fallback backend", metadata[MetadataFallbackBackends]); err != nil {
		return RuntimeMetadata{}, err
	}
	if out.Repos, err = decodeCanonicalStringList("repository", metadata[MetadataRepos]); err != nil {
		return RuntimeMetadata{}, err
	}
	if out.RepoGroups, err = decodeCanonicalStringList("repository group", metadata[MetadataRepoGroups]); err != nil {
		return RuntimeMetadata{}, err
	}
	if out.CrossRepo, err = parseRequiredBoolMetadata(MetadataCrossRepo, metadata); err != nil {
		return RuntimeMetadata{}, err
	}
	if out.Auto, err = parseRequiredBoolMetadata(MetadataAuto, metadata); err != nil {
		return RuntimeMetadata{}, err
	}
	return out, nil
}

func hasRuntimeMetadata(metadata map[string]string) bool {
	for _, key := range [...]string{
		MetadataRoleKind, MetadataFallbackBackends, MetadataRepos,
		MetadataRepoGroups, MetadataCrossRepo, MetadataAuto,
	} {
		if _, ok := metadata[key]; ok {
			return true
		}
	}
	return false
}

func encodeCanonicalStringList(label string, values []string) (string, error) {
	normalized, err := normalizeCanonicalList(label, append([]string(nil), values...))
	if err != nil {
		return "", err
	}
	if normalized == nil {
		normalized = []string{}
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode %s metadata: %w", label, err)
	}
	return string(encoded), nil
}

func decodeCanonicalStringList(label, value string) ([]string, error) {
	var decoded []string
	if err := json.Unmarshal([]byte(value), &decoded); err != nil || decoded == nil {
		return nil, fmt.Errorf("%s metadata is invalid: %w", label, ErrInvalidPersistedState)
	}
	normalized, err := normalizeCanonicalList(label, append([]string(nil), decoded...))
	if err != nil || len(normalized) != len(decoded) {
		return nil, fmt.Errorf("%s metadata is not canonical: %w", label, ErrInvalidPersistedState)
	}
	for index := range decoded {
		if decoded[index] != normalized[index] {
			return nil, fmt.Errorf("%s metadata is not canonical: %w", label, ErrInvalidPersistedState)
		}
	}
	return decoded, nil
}

func parseRequiredBoolMetadata(key string, metadata map[string]string) (bool, error) {
	value, ok := metadata[key]
	if !ok {
		return false, fmt.Errorf("%s metadata is required: %w", key, ErrInvalidPersistedState)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil || strconv.FormatBool(parsed) != value {
		return false, fmt.Errorf("%s metadata is invalid: %w", key, ErrInvalidPersistedState)
	}
	return parsed, nil
}

func cloneMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+7)
	for key, value := range in {
		out[key] = value
	}
	return out
}
