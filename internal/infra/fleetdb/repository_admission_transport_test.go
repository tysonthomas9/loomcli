package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

func TestValidRepositoryAdmissionBranchMatchesFleetDBConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		branch string
		valid  bool
	}{
		{name: "main", branch: "main", valid: true},
		{name: "release with dot", branch: "release/2026.07", valid: true},
		{name: "feature", branch: "feature/topic-name", valid: true},
		{name: "empty", branch: "", valid: false},
		{name: "at", branch: "@", valid: false},
		{name: "leading dash", branch: "-main", valid: false},
		{name: "hidden root", branch: ".hidden", valid: false},
		{name: "hidden component", branch: "feature/.hidden", valid: false},
		{name: "lock root", branch: "main.lock", valid: false},
		{name: "lock component", branch: "feature/main.lock", valid: false},
		{name: "empty component", branch: "feature//topic", valid: false},
		{name: "dot dot", branch: "feature/../topic", valid: false},
		{name: "reflog syntax", branch: "feature@{topic", valid: false},
		{name: "space", branch: "feature topic", valid: false},
		{name: "trailing dot", branch: "feature/topic.", valid: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validRepositoryAdmissionBranch(test.branch); got != test.valid {
				t.Fatalf(
					"validRepositoryAdmissionBranch(%q) = %t, want %t",
					test.branch,
					got,
					test.valid,
				)
			}
		})
	}
}

func TestValidateRepositoryAdmissionReceiptBindsImmutableIntent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RepositoryAdmissionRecord)
		valid  bool
	}{
		{name: "exact receipt", valid: true},
		{
			name: "remote URL",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Receipt.Repositories[0].Repository.RemoteURL =
					"https://example.com/acme/other.git"
			},
		},
		{
			name: "remote name",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Receipt.Repositories[0].Repository.Remote = "upstream"
			},
		},
		{
			name: "groups",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Receipt.Repositories[0].Repository.Groups =
					[]string{"frontend", "backend"}
			},
		},
		{
			name: "source repo",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Receipt.Repositories[0].Repository.SourceRepoID = "other"
			},
		},
		{
			name: "requested branch",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Receipt.Repositories[0].Repository.DefaultBranch = "release"
			},
		},
		{
			name: "commit terminal timestamp",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Receipt.CommittedAt = record.Receipt.CommittedAt.Add(time.Second)
			},
		},
		{
			name: "terminal outside record lifetime",
			mutate: func(record *RepositoryAdmissionRecord) {
				terminal := record.UpdatedAt.Add(time.Second)
				record.TerminalAt = &terminal
				record.Receipt.CommittedAt = terminal
			},
		},
		{
			name: "owner lease beyond maximum response horizon",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.OwnerLeaseExpiresAt = record.UpdatedAt.Add(
					maxRepositoryAdmissionOwnerLease + time.Second,
				)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			record := exactRepositoryAdmissionReceiptRecord()
			if test.mutate != nil {
				test.mutate(record)
			}
			err := validateRepositoryAdmissionRecord(record, false)
			if test.valid && err != nil {
				t.Fatalf("exact receipt rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("divergent receipt accepted")
			}
		})
	}
}

func TestValidateRepositoryAdmissionBeginIntentBindsCanonicalRequest(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RepositoryAdmissionRecord)
		valid  bool
	}{
		{name: "exact response", valid: true},
		{
			name: "equivalent repository and group order",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Spec.Repositories[0], record.Spec.Repositories[1] =
					record.Spec.Repositories[1], record.Spec.Repositories[0]
				record.Spec.Repositories[1].Groups =
					[]string{"frontend", "backend"}
			},
			valid: true,
		},
		{
			name: "token free remote substitution",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Spec.Repositories[0].RemoteURL =
					"https://example.com/acme/substitute.git"
			},
		},
		{
			name: "explicit branch substitution",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Spec.Repositories[0].DefaultBranch = "release"
			},
		},
		{
			name: "repository substitution",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.Spec.Repositories[0].Name = "substitute"
			},
		},
		{
			name: "owner substitution",
			mutate: func(record *RepositoryAdmissionRecord) {
				record.OwnerID = "loom-workspace-admission-other"
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			record, input := exactRepositoryAdmissionBeginIntent()
			if test.mutate != nil {
				test.mutate(record)
			}
			err := validateRepositoryAdmissionBeginIntent(
				record,
				"WORK",
				input,
			)
			if test.valid && err != nil {
				t.Fatalf("exact begin response rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("divergent begin response accepted")
			}
		})
	}
}

func TestValidateWorkspaceRepositoryAdmissionBeginResultBindsProjection(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*WorkspaceRepositoryAdmissionBeginResult)
		valid  bool
	}{
		{name: "exact response", valid: true},
		{
			name: "workspace name",
			mutate: func(result *WorkspaceRepositoryAdmissionBeginResult) {
				result.Workspace.Name = "Other"
			},
		},
		{
			name: "workspace state",
			mutate: func(result *WorkspaceRepositoryAdmissionBeginResult) {
				result.Workspace.State = workspacemodule.StateReady
			},
		},
		{
			name: "workspace branch",
			mutate: func(result *WorkspaceRepositoryAdmissionBeginResult) {
				result.Workspace.DefaultBranch = "release"
			},
		},
		{
			name: "admission workspace",
			mutate: func(result *WorkspaceRepositoryAdmissionBeginResult) {
				result.Admission.WorkspaceKey = "OTHER"
			},
		},
		{
			name: "event identity whitespace",
			mutate: func(result *WorkspaceRepositoryAdmissionBeginResult) {
				result.WorkspaceEventID = " workspace-event "
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			record, input := exactRepositoryAdmissionBeginIntent()
			workspace := RepositoryAdmissionWorkspaceInput{
				Key:           "WORK",
				Name:          "Work",
				Description:   "Workspace",
				State:         "creating",
				DefaultBranch: "main",
				DesignFormat:  "markdown",
			}
			result := &WorkspaceRepositoryAdmissionBeginResult{
				Workspace: &workspacemodule.Workspace{
					Key:           workspace.Key,
					Name:          workspace.Name,
					Description:   workspace.Description,
					State:         workspacemodule.State(workspace.State),
					DefaultBranch: workspace.DefaultBranch,
					DesignFormat:  workspace.DesignFormat,
					CreatedAt:     record.CreatedAt,
					UpdatedAt:     record.CreatedAt,
				},
				Admission:        record,
				WorkspaceEventID: "workspace-event",
			}
			if test.mutate != nil {
				test.mutate(result)
			}
			if err := validateRepositoryAdmissionBeginIntent(
				result.Admission,
				workspace.Key,
				input,
			); err != nil && test.valid {
				t.Fatalf("exact admission intent rejected: %v", err)
			}
			err := validateWorkspaceRepositoryAdmissionBeginResult(
				result,
				workspace,
			)
			if test.valid && err != nil {
				t.Fatalf("exact workspace response rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("divergent workspace response accepted")
			}
		})
	}
}

func TestRepositoryAdmissionTransportRejectsDivergentResponseBindings(
	t *testing.T,
) {
	t.Parallel()

	t.Run("get admission coordinates", func(t *testing.T) {
		t.Parallel()
		record, _ := exactRepositoryAdmissionBeginIntent()
		record.AdmissionID = "cccccccccccccccccccccccccccccccc"
		transport := newRepositoryAdmissionResponseTransport(t, record)
		if _, err := transport.GetRepositoryAdmission(
			t.Context(),
			"WORK",
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		); !errors.Is(err, ErrRepositoryAdmissionInvalid) {
			t.Fatalf("divergent get response error = %v", err)
		}
	})

	t.Run("get operation coordinates", func(t *testing.T) {
		t.Parallel()
		record, _ := exactRepositoryAdmissionBeginIntent()
		record.OperationID = "workspace-add_repositories:cccccccccccccccccccccccccccccccc"
		record.Spec.OperationID = record.OperationID
		record.OwnerID = ""
		record.OwnerGenerationID = ""
		transport := newRepositoryAdmissionResponseTransport(t, record)
		if _, err := transport.GetRepositoryAdmissionByOperation(
			t.Context(),
			"WORK",
			"workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		); !errors.Is(err, ErrRepositoryAdmissionInvalid) {
			t.Fatalf("divergent operation response error = %v", err)
		}
	})

	t.Run("renew exact version", func(t *testing.T) {
		t.Parallel()
		record, _ := exactRepositoryAdmissionBeginIntent()
		record.Version = 3
		record.UpdatedAt = record.UpdatedAt.Add(time.Second)
		record.OwnerLeaseExpiresAt = record.OwnerLeaseExpiresAt.Add(time.Minute)
		transport := newRepositoryAdmissionResponseTransport(t, record)
		if _, err := transport.RenewRepositoryAdmission(
			t.Context(),
			RepositoryAdmissionRenewInput{
				RepositoryAdmissionGuard: testRepositoryAdmissionGuard(record, 1),
				Lease:                    time.Minute,
			},
		); !errors.Is(err, ErrRepositoryAdmissionInvalid) {
			t.Fatalf("renew version jump error = %v", err)
		}
	})

	t.Run("recovery exact owner and version", func(t *testing.T) {
		t.Parallel()
		record, _ := exactRepositoryAdmissionBeginIntent()
		record.OwnerID = "loom-workspace-admission-new"
		record.OwnerGenerationID = "cccccccccccccccccccccccccccccccc"
		record.Version = 3
		record.UpdatedAt = record.UpdatedAt.Add(time.Second)
		transport := newRepositoryAdmissionResponseTransport(t, record)
		if _, err := transport.ClaimRepositoryAdmissionRecovery(
			t.Context(),
			RepositoryAdmissionRecoveryClaimInput{
				WorkspaceKey: "WORK", AdmissionID: record.AdmissionID,
				ExpectedSpecFingerprint: record.SpecFingerprint,
				ExpectedVersion:         1,
				NewOwnerID:              record.OwnerID,
				Lease:                   time.Minute,
			},
		); !errors.Is(err, ErrRepositoryAdmissionInvalid) {
			t.Fatalf("recovery version jump error = %v", err)
		}
	})

	t.Run("commit exact resolution", func(t *testing.T) {
		t.Parallel()
		record := exactRepositoryAdmissionReceiptRecord()
		transport := newRepositoryAdmissionResponseTransport(t, record)
		if _, err := transport.CommitRepositoryAdmission(
			t.Context(),
			RepositoryAdmissionCommitInput{
				RepositoryAdmissionGuard: testRepositoryAdmissionGuard(record, 1),
				ResolvedDefaultBranches: []RepositoryAdmissionResolvedBranch{{
					Name: "app", DefaultBranch: "release",
				}},
			},
		); !errors.Is(err, ErrRepositoryAdmissionInvalid) {
			t.Fatalf("divergent commit resolution error = %v", err)
		}
	})
}

func newRepositoryAdmissionResponseTransport(
	t *testing.T,
	response any,
) RepositoryAdmissionTransport {
	t.Helper()
	return newRepositoryAdmissionTransport(repositoryAdmissionResponseRequester{response: response})
}

type repositoryAdmissionResponseRequester struct {
	response any
}

func (requester repositoryAdmissionResponseRequester) Do(
	_ context.Context,
	_,
	_ string,
	_ any,
	out any,
) error {
	encoded, err := json.Marshal(requester.response)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
}

func (requester repositoryAdmissionResponseRequester) DoWithHeaders(
	ctx context.Context,
	method,
	path string,
	body,
	out any,
	_ map[string]string,
) error {
	return requester.Do(ctx, method, path, body, out)
}

func testRepositoryAdmissionGuard(
	record *RepositoryAdmissionRecord,
	version int64,
) RepositoryAdmissionGuard {
	return RepositoryAdmissionGuard{
		WorkspaceKey: record.WorkspaceKey, AdmissionID: record.AdmissionID,
		OwnerID: record.OwnerID, OwnerGenerationID: record.OwnerGenerationID,
		SpecFingerprint: record.SpecFingerprint, ExpectedVersion: version,
	}
}

func exactRepositoryAdmissionBeginIntent() (
	*RepositoryAdmissionRecord,
	RepositoryAdmissionBeginInput,
) {
	createdAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	repositories := []RepositoryAdmissionRepoSpec{
		{
			Name:          "app",
			RemoteURL:     "https://example.com/acme/app.git",
			Remote:        "origin",
			DefaultBranch: "main",
			Groups:        []string{"backend", "frontend"},
			SourceRepoID:  "app-source",
		},
		{
			Name:         "docs",
			RemoteURL:    "https://example.com/acme/docs.git",
			Remote:       "origin",
			SourceRepoID: "docs-source",
		},
	}
	input := RepositoryAdmissionBeginInput{
		OperationID:  "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerID:      "loom-workspace-admission-owner",
		OwnerLease:   time.Minute,
		Repositories: repositories,
	}
	return &RepositoryAdmissionRecord{
		AdmissionID:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkspaceKey:        "WORK",
		OperationID:         input.OperationID,
		OwnerID:             input.OwnerID,
		OwnerGenerationID:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		OwnerLeaseExpiresAt: createdAt.Add(input.OwnerLease),
		SpecFingerprint:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec: RepositoryAdmissionSpec{
			WorkspaceKey: "WORK",
			OperationID:  input.OperationID,
			Repositories: append([]RepositoryAdmissionRepoSpec(nil), repositories...),
		},
		State:     "pending",
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}, input
}

func exactRepositoryAdmissionReceiptRecord() *RepositoryAdmissionRecord {
	createdAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	committedAt := createdAt.Add(time.Second)
	return &RepositoryAdmissionRecord{
		AdmissionID:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkspaceKey:        "WORK",
		OperationID:         "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerID:             "loom-workspace-admission-owner",
		OwnerGenerationID:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		OwnerLeaseExpiresAt: createdAt.Add(time.Minute),
		SpecFingerprint:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec: RepositoryAdmissionSpec{
			WorkspaceKey: "WORK",
			OperationID:  "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Repositories: []RepositoryAdmissionRepoSpec{{
				Name:          "app",
				RemoteURL:     "https://example.com/acme/app.git",
				Remote:        "origin",
				DefaultBranch: "main",
				Groups:        []string{"backend", "frontend"},
				SourceRepoID:  "app-source",
			}},
		},
		State:      "committed",
		Version:    2,
		CreatedAt:  createdAt,
		UpdatedAt:  committedAt,
		TerminalAt: &committedAt,
		Receipt: &RepositoryAdmissionReceipt{
			AdmissionID:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SpecFingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Repositories: []RepositoryAdmissionRepoReceipt{{
				Repository: workspacemodule.Repository{
					WorkspaceKey:  "WORK",
					Name:          "app",
					RemoteURL:     "https://example.com/acme/app.git",
					Remote:        "origin",
					DefaultBranch: "main",
					Groups:        []string{"backend", "frontend"},
					SourceRepoID:  "app-source",
					CreatedAt:     committedAt,
					UpdatedAt:     committedAt,
				},
				EventID: "repo-event-app",
			}},
			CommittedAt: committedAt,
		},
	}
}
