package workspacemgr

import (
	"context"
	"errors"
	"testing"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

type ambiguousRepositoryAdmissionCommitTransport struct {
	infrafleetdb.RepositoryAdmissionTransport

	before    *infrafleetdb.RepositoryAdmissionRecord
	after     *infrafleetdb.RepositoryAdmissionRecord
	commitErr error
	gets      int
	commits   int
}

type repositoryAdmissionOwnershipTransport struct {
	infrafleetdb.RepositoryAdmissionTransport

	current   *infrafleetdb.RepositoryAdmissionRecord
	renewed   *infrafleetdb.RepositoryAdmissionRecord
	recovered *infrafleetdb.RepositoryAdmissionRecord
	failed    *infrafleetdb.RepositoryAdmissionRecord
}

func (transport *repositoryAdmissionOwnershipTransport) GetRepositoryAdmission(
	context.Context,
	string,
	string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	return cloneTestAdmission(transport.current), nil
}

func (transport *repositoryAdmissionOwnershipTransport) RenewRepositoryAdmission(
	context.Context,
	infrafleetdb.RepositoryAdmissionRenewInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	return cloneTestAdmission(transport.renewed), nil
}

func (transport *repositoryAdmissionOwnershipTransport) ClaimRepositoryAdmissionRecovery(
	context.Context,
	infrafleetdb.RepositoryAdmissionRecoveryClaimInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	return cloneTestAdmission(transport.recovered), nil
}

func (transport *repositoryAdmissionOwnershipTransport) FailRepositoryAdmission(
	context.Context,
	infrafleetdb.RepositoryAdmissionFailInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	return cloneTestAdmission(transport.failed), nil
}

func (transport *ambiguousRepositoryAdmissionCommitTransport) GetRepositoryAdmission(
	context.Context,
	string,
	string,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	transport.gets++
	if transport.gets == 1 {
		return cloneTestAdmission(transport.before), nil
	}
	return cloneTestAdmission(transport.after), nil
}

func (transport *ambiguousRepositoryAdmissionCommitTransport) CommitRepositoryAdmission(
	context.Context,
	infrafleetdb.RepositoryAdmissionCommitInput,
) (*infrafleetdb.RepositoryAdmissionRecord, error) {
	transport.commits++
	return nil, transport.commitErr
}

func TestRepositoryAdmissionCommitReconcilesOnlyExactLostResponse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	pending := testPendingRepositoryAdmission(now)
	repositories := []config.RepoConfig{{
		Name:          "app",
		Remote:        "origin",
		DefaultBranch: "main",
	}}
	finalization := &infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
		State:         "ready",
		DefaultBranch: "main",
	}
	commitErr := errors.New("ambiguous commit response")

	t.Run("same owner exact receipt succeeds", func(t *testing.T) {
		t.Parallel()

		committed := testCommittedRepositoryAdmission(
			pending,
			pending.OwnerID,
			pending.OwnerGenerationID,
			pending.Version+1,
			"main",
			finalization,
			now.Add(time.Second),
		)
		transport := &ambiguousRepositoryAdmissionCommitTransport{
			before: pending, after: committed, commitErr: commitErr,
		}
		process := &repositoryAdmissionProcess{
			admissions: transport,
			ownerID:    pending.OwnerID,
			now:        func() time.Time { return now },
			leases:     newRepositoryAdmissionLeaseState(),
		}

		got, err := process.commit(
			t.Context(),
			pending,
			repositories,
			finalization,
		)
		if err != nil || got == nil || got.State != "committed" {
			t.Fatalf("exact lost-response reconciliation = %#v, err=%v", got, err)
		}
	})

	t.Run("successor recovery commit is rejected", func(t *testing.T) {
		t.Parallel()

		successor := testCommittedRepositoryAdmission(
			pending,
			"loom-workspace-admission-successor",
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			pending.Version+2,
			"release",
			&infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
				State:         "ready",
				DefaultBranch: "release",
			},
			now.Add(2*time.Second),
		)
		transport := &ambiguousRepositoryAdmissionCommitTransport{
			before: pending, after: successor, commitErr: commitErr,
		}
		process := &repositoryAdmissionProcess{
			admissions: transport,
			ownerID:    pending.OwnerID,
			now:        func() time.Time { return now },
			leases:     newRepositoryAdmissionLeaseState(),
		}

		got, err := process.commit(
			t.Context(),
			pending,
			repositories,
			finalization,
		)
		if got != nil || !errors.Is(err, commitErr) {
			t.Fatalf(
				"successor commit reconciliation = %#v, err=%v; want original ambiguity",
				got,
				err,
			)
		}
	})

	t.Run("successor observed before commit is rejected", func(t *testing.T) {
		t.Parallel()

		successor := testCommittedRepositoryAdmission(
			pending,
			"loom-workspace-admission-successor",
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			pending.Version+2,
			"release",
			&infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
				State:         "ready",
				DefaultBranch: "release",
			},
			now.Add(2*time.Second),
		)
		transport := &ambiguousRepositoryAdmissionCommitTransport{
			before: successor, after: successor, commitErr: commitErr,
		}
		process := &repositoryAdmissionProcess{
			admissions: transport,
			ownerID:    pending.OwnerID,
			now:        func() time.Time { return now },
			leases:     newRepositoryAdmissionLeaseState(),
		}

		got, err := process.commit(
			t.Context(),
			pending,
			repositories,
			finalization,
		)
		if got != nil ||
			!errors.Is(err, infrafleetdb.ErrRepositoryAdmissionFenceLost) ||
			transport.commits != 0 {
			t.Fatalf(
				"pre-commit successor reconciliation = %#v, err=%v, commits=%d",
				got,
				err,
				transport.commits,
			)
		}
	})
}

func TestEnsureOwnershipAllowsCommittedReplayAcrossServeOwnerChange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	pending := testPendingRepositoryAdmission(now)
	committed := testCommittedRepositoryAdmission(
		pending,
		"loom-workspace-admission-recovery-owner",
		"cccccccccccccccccccccccccccccccc",
		pending.Version+2,
		"main",
		&infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
			State:         "ready",
			DefaultBranch: "main",
		},
		now.Add(2*time.Second),
	)
	transport := &ambiguousRepositoryAdmissionCommitTransport{
		before: committed,
		after:  committed,
	}
	process := &repositoryAdmissionProcess{
		admissions: transport,
		ownerID:    "loom-workspace-admission-restarted-owner",
		now:        func() time.Time { return now },
		leases:     newRepositoryAdmissionLeaseState(),
	}

	got, err := process.ensureOwnership(t.Context(), committed)
	if err != nil || got == nil || got.State != "committed" ||
		got.OwnerID != committed.OwnerID {
		t.Fatalf("cross-owner committed operation replay = %#v, err=%v", got, err)
	}
}

func TestAcquireMaterializationOwnershipRequiresExactRecoveryAndRenewResponses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 13, 0, 0, 0, time.UTC)
	previous := testPendingRepositoryAdmission(now)
	previous.OwnerLeaseExpiresAt = now.Add(-time.Second)
	recovered := cloneTestAdmission(previous)
	recovered.OwnerID = "loom-workspace-admission-recovery"
	recovered.OwnerGenerationID = "22222222222222222222222222222222"
	recovered.OwnerLeaseExpiresAt = now.Add(repositoryAdmissionLease)
	recovered.Version++
	recovered.UpdatedAt = now
	renewedRecovery := cloneTestAdmission(recovered)
	renewedRecovery.OwnerLeaseExpiresAt = recovered.OwnerLeaseExpiresAt.Add(
		repositoryAdmissionLease,
	)
	renewedRecovery.Version++
	renewedRecovery.UpdatedAt = now.Add(time.Second)

	tests := []struct {
		name    string
		mutate  func(*infrafleetdb.RepositoryAdmissionRecord)
		wantErr bool
	}{
		{name: "exact"},
		{
			name: "version jump",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.Version++
			},
			wantErr: true,
		},
		{
			name: "owner substitution",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.OwnerID = "loom-workspace-admission-other"
			},
			wantErr: true,
		},
		{
			name: "generation reused",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.OwnerGenerationID = previous.OwnerGenerationID
			},
			wantErr: true,
		},
		{
			name: "immutable spec changed",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.Spec.Repositories[0].RemoteURL =
					"https://example.com/acme/substituted.git"
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := cloneTestAdmission(recovered)
			if test.mutate != nil {
				test.mutate(response)
			}
			transport := &repositoryAdmissionOwnershipTransport{
				current: previous, recovered: response,
				renewed: renewedRecovery,
			}
			process := &repositoryAdmissionProcess{
				admissions: transport,
				ownerID:    recovered.OwnerID,
				now:        func() time.Time { return now },
				leases:     newRepositoryAdmissionLeaseState(),
			}

			got, _, err := process.acquireMaterializationOwnership(
				t.Context(),
				previous,
			)
			if test.wantErr {
				if got != nil ||
					!errors.Is(err, infrafleetdb.ErrRepositoryAdmissionInvalid) {
					t.Fatalf("ensureOwnership() = %#v, %v; want invalid", got, err)
				}
				return
			}
			if err != nil ||
				got == nil ||
				got.OwnerGenerationID != recovered.OwnerGenerationID ||
				got.Version != previous.Version+2 {
				t.Fatalf("acquireMaterializationOwnership() = %#v, %v; want exact recovery and renewal", got, err)
			}
		})
	}
}

func TestAcquireMaterializationOwnershipRequiresExactRenewResponse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	previous := testPendingRepositoryAdmission(now)
	previous.OwnerLeaseExpiresAt = now.Add(30 * time.Second)
	renewed := cloneTestAdmission(previous)
	renewed.OwnerLeaseExpiresAt = now.Add(repositoryAdmissionLease)
	renewed.Version++
	renewed.UpdatedAt = now.Add(time.Second)

	tests := []struct {
		name    string
		mutate  func(*infrafleetdb.RepositoryAdmissionRecord)
		wantErr bool
	}{
		{name: "exact"},
		{
			name: "version jump",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.Version++
			},
			wantErr: true,
		},
		{
			name: "generation substitution",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.OwnerGenerationID = "33333333333333333333333333333333"
			},
			wantErr: true,
		},
		{
			name: "immutable spec changed",
			mutate: func(record *infrafleetdb.RepositoryAdmissionRecord) {
				record.Spec.Repositories[0].SourceRepoID = "substituted"
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := cloneTestAdmission(renewed)
			if test.mutate != nil {
				test.mutate(response)
			}
			transport := &repositoryAdmissionOwnershipTransport{
				current: previous, renewed: response,
			}
			process := &repositoryAdmissionProcess{
				admissions: transport,
				ownerID:    previous.OwnerID,
				now:        func() time.Time { return now },
				leases:     newRepositoryAdmissionLeaseState(),
			}

			got, _, err := process.acquireMaterializationOwnership(
				t.Context(),
				previous,
			)
			if test.wantErr {
				if got != nil ||
					!errors.Is(err, infrafleetdb.ErrRepositoryAdmissionInvalid) {
					t.Fatalf("ensureOwnership() = %#v, %v; want invalid", got, err)
				}
				return
			}
			if err != nil || got == nil || got.Version != previous.Version+1 {
				t.Fatalf("ensureOwnership() = %#v, %v; want exact renewal", got, err)
			}
		})
	}
}

func TestRepositoryAdmissionFailRequiresExactTransitionResponse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 15, 0, 0, 0, time.UTC)
	previous := testPendingRepositoryAdmission(now)
	failed := cloneTestAdmission(previous)
	failed.State = "retryable_failed"
	failed.LastErrorClass = "materialization_interrupted"
	failed.Version++
	failed.UpdatedAt = now.Add(time.Second)
	failed.Version++

	transport := &repositoryAdmissionOwnershipTransport{
		current: previous,
		failed:  failed,
	}
	process := &repositoryAdmissionProcess{
		admissions: transport,
		ownerID:    previous.OwnerID,
		now:        func() time.Time { return now },
		leases:     newRepositoryAdmissionLeaseState(),
	}
	cause := context.DeadlineExceeded

	err := process.fail(t.Context(), previous, cause)
	if !errors.Is(err, cause) ||
		!errors.Is(err, infrafleetdb.ErrRepositoryAdmissionInvalid) {
		t.Fatalf("fail() error = %v; want cause joined with invalid response", err)
	}
}

func TestValidCommittedRepositoryAdmissionReplayRequiresOperationShape(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 16, 0, 0, 0, time.UTC)
	pending := testPendingRepositoryAdmission(now)
	create := testCommittedRepositoryAdmission(
		pending,
		pending.OwnerID,
		pending.OwnerGenerationID,
		pending.Version+1,
		"main",
		&infrafleetdb.RepositoryAdmissionWorkspaceFinalization{
			State:         "ready",
			DefaultBranch: "main",
		},
		now.Add(time.Second),
	)
	add := cloneTestAdmission(create)
	add.Receipt.WorkspaceFinalization = nil

	if !validCommittedRepositoryAdmissionReplay(create, true) {
		t.Fatal("create replay with exact finalization was rejected")
	}
	if validCommittedRepositoryAdmissionReplay(create, false) {
		t.Fatal("add-repositories replay accepted workspace finalization")
	}
	if !validCommittedRepositoryAdmissionReplay(add, false) {
		t.Fatal("add-repositories replay without finalization was rejected")
	}
	if validCommittedRepositoryAdmissionReplay(add, true) {
		t.Fatal("create replay accepted missing workspace finalization")
	}
	create.Receipt.WorkspaceFinalization.DefaultBranch = "release"
	if validCommittedRepositoryAdmissionReplay(create, true) {
		t.Fatal("create replay accepted divergent workspace default branch")
	}
}

func testPendingRepositoryAdmission(
	now time.Time,
) *infrafleetdb.RepositoryAdmissionRecord {
	return &infrafleetdb.RepositoryAdmissionRecord{
		AdmissionID:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkspaceKey:        "WORK",
		OperationID:         "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerID:             "loom-workspace-admission-original",
		OwnerGenerationID:   "11111111111111111111111111111111",
		OwnerLeaseExpiresAt: now.Add(2 * time.Minute),
		SpecFingerprint:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec: infrafleetdb.RepositoryAdmissionSpec{
			WorkspaceKey: "WORK",
			OperationID:  "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Repositories: []infrafleetdb.RepositoryAdmissionRepoSpec{{
				Name:          "app",
				RemoteURL:     "https://example.com/acme/app.git",
				Remote:        "origin",
				DefaultBranch: "",
			}},
		},
		State:     "pending",
		Version:   7,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	}
}

func testCommittedRepositoryAdmission(
	pending *infrafleetdb.RepositoryAdmissionRecord,
	ownerID string,
	ownerGenerationID string,
	version int64,
	branch string,
	finalization *infrafleetdb.RepositoryAdmissionWorkspaceFinalization,
	committedAt time.Time,
) *infrafleetdb.RepositoryAdmissionRecord {
	committed := cloneTestAdmission(pending)
	committed.OwnerID = ownerID
	committed.OwnerGenerationID = ownerGenerationID
	committed.State = "committed"
	committed.Version = version
	committed.UpdatedAt = committedAt
	committed.TerminalAt = &committedAt
	committed.Receipt = &infrafleetdb.RepositoryAdmissionReceipt{
		AdmissionID:     committed.AdmissionID,
		SpecFingerprint: committed.SpecFingerprint,
		Repositories: []infrafleetdb.RepositoryAdmissionRepoReceipt{{
			Repository: workspacemodule.Repository{
				WorkspaceKey:  committed.WorkspaceKey,
				Name:          "app",
				RemoteURL:     "https://example.com/acme/app.git",
				Remote:        "origin",
				DefaultBranch: branch,
				CreatedAt:     committedAt,
				UpdatedAt:     committedAt,
			},
			EventID: "repo-event-app",
		}},
		WorkspaceFinalization: finalization,
		CommittedAt:           committedAt,
	}
	return committed
}
