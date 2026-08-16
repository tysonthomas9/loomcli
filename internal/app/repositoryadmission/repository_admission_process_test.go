package repositoryadmission

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

func cloneTestAdmission(record *Record) *Record {
	if record == nil {
		return nil
	}
	encoded, _ := json.Marshal(record)
	var result Record
	_ = json.Unmarshal(encoded, &result)
	return &result
}

type ambiguousRepositoryAdmissionCommitTransport struct {
	DurableAdmissions

	before    *Record
	after     *Record
	commitErr error
	gets      int
	commits   int
}

type repositoryAdmissionOwnershipTransport struct {
	DurableAdmissions

	current   *Record
	renewed   *Record
	recovered *Record
	failed    *Record
}

func (transport *repositoryAdmissionOwnershipTransport) Get(
	context.Context,
	string,
	string,
) (*Record, error) {
	return cloneTestAdmission(transport.current), nil
}

func (transport *repositoryAdmissionOwnershipTransport) Renew(
	context.Context,
	Renew,
) (*Record, error) {
	return cloneTestAdmission(transport.renewed), nil
}

func (transport *repositoryAdmissionOwnershipTransport) ClaimRecovery(
	context.Context,
	RecoveryClaim,
) (*Record, error) {
	return cloneTestAdmission(transport.recovered), nil
}

func (transport *repositoryAdmissionOwnershipTransport) Fail(
	context.Context,
	Fail,
) (*Record, error) {
	return cloneTestAdmission(transport.failed), nil
}

func (transport *ambiguousRepositoryAdmissionCommitTransport) Get(
	context.Context,
	string,
	string,
) (*Record, error) {
	transport.gets++
	if transport.gets == 1 {
		return cloneTestAdmission(transport.before), nil
	}
	return cloneTestAdmission(transport.after), nil
}

func (transport *ambiguousRepositoryAdmissionCommitTransport) Commit(
	context.Context,
	Commit,
) (*Record, error) {
	transport.commits++
	return nil, transport.commitErr
}

func TestRepositoryAdmissionCommitReconcilesOnlyExactLostResponse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	pending := testPendingRepositoryAdmission(now)
	repositories := []RepositoryPlacement{{
		Name:          "app",
		Remote:        "origin",
		DefaultBranch: "main",
	}}
	finalization := &WorkspaceFinalization{
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
			&WorkspaceFinalization{
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
			&WorkspaceFinalization{
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
			!errors.Is(err, ErrFenceLost) ||
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
		&WorkspaceFinalization{
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
		mutate  func(*Record)
		wantErr bool
	}{
		{name: "exact"},
		{
			name: "version jump",
			mutate: func(record *Record) {
				record.Version++
			},
			wantErr: true,
		},
		{
			name: "owner substitution",
			mutate: func(record *Record) {
				record.OwnerID = "loom-workspace-admission-other"
			},
			wantErr: true,
		},
		{
			name: "generation reused",
			mutate: func(record *Record) {
				record.OwnerGenerationID = previous.OwnerGenerationID
			},
			wantErr: true,
		},
		{
			name: "immutable spec changed",
			mutate: func(record *Record) {
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
					!errors.Is(err, ErrInvalid) {
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
		mutate  func(*Record)
		wantErr bool
	}{
		{name: "exact"},
		{
			name: "version jump",
			mutate: func(record *Record) {
				record.Version++
			},
			wantErr: true,
		},
		{
			name: "generation substitution",
			mutate: func(record *Record) {
				record.OwnerGenerationID = "33333333333333333333333333333333"
			},
			wantErr: true,
		},
		{
			name: "immutable spec changed",
			mutate: func(record *Record) {
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
					!errors.Is(err, ErrInvalid) {
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
		!errors.Is(err, ErrInvalid) {
		t.Fatalf("fail() error = %v; want cause joined with invalid response", err)
	}
}

func testPendingRepositoryAdmission(
	now time.Time,
) *Record {
	return &Record{
		AdmissionID:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkspaceKey:        "WORK",
		OperationID:         "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OwnerID:             "loom-workspace-admission-original",
		OwnerGenerationID:   "11111111111111111111111111111111",
		OwnerLeaseExpiresAt: now.Add(2 * time.Minute),
		SpecFingerprint:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec: Spec{
			WorkspaceKey: "WORK",
			OperationID:  "workspace-add_repositories:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Repositories: []RepositorySpec{{
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
	pending *Record,
	ownerID string,
	ownerGenerationID string,
	version int64,
	branch string,
	finalization *WorkspaceFinalization,
	committedAt time.Time,
) *Record {
	committed := cloneTestAdmission(pending)
	committed.OwnerID = ownerID
	committed.OwnerGenerationID = ownerGenerationID
	committed.State = "committed"
	committed.Version = version
	committed.UpdatedAt = committedAt
	committed.TerminalAt = &committedAt
	committed.Receipt = &Receipt{
		AdmissionID:     committed.AdmissionID,
		SpecFingerprint: committed.SpecFingerprint,
		Repositories: []RepositoryReceipt{{
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
