package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	seedTranscriptWorkspace string
	seedTranscriptSession   string
	seedTranscriptTask      string
	seedTranscriptBackend   string
	seedTranscriptFile      string
)

// daemonSeedTranscriptCmd is a TEST-ONLY helper for the fleet-db distributed smoke.
// It synthesizes exactly the control-plane state the daemon leaf's finalize produces
// — an agent session, a finalized transcript artifact, and metadata.transcript_ref —
// so the smoke can assert that a NON-owning serve node surfaces the transcript via the
// control-plane fallback (controlPlaneSessionTranscript). Hidden: never in help output.
var daemonSeedTranscriptCmd = &cobra.Command{
	Use:    "seed-transcript",
	Short:  "TEST-ONLY: seed an agent session + transcript artifact + transcript_ref in fleet-db",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDaemonSeedTranscript,
}

func init() {
	f := daemonSeedTranscriptCmd.Flags()
	f.StringVar(&seedTranscriptWorkspace, "workspace", "", "Workspace key (default: active)")
	f.StringVar(&seedTranscriptSession, "session", "", "Agent session id (required)")
	f.StringVar(&seedTranscriptTask, "task", "", "Task id the session belongs to (required)")
	f.StringVar(&seedTranscriptBackend, "backend", "codex", "Backend label")
	f.StringVar(&seedTranscriptFile, "content", "", "Canonical NDJSON transcript file (default: stdin)")
	daemonCmd.AddCommand(daemonSeedTranscriptCmd)
}

//nolint:funlen // CLI command wires validation, transcript parsing, store lookup, and session update in one path.
func runDaemonSeedTranscript(_ *cobra.Command, _ []string) error {
	if seedTranscriptSession == "" || seedTranscriptTask == "" {
		return fmt.Errorf("--session and --task are required")
	}
	data, err := readSeedTranscriptContent(seedTranscriptFile)
	if err != nil {
		return fmt.Errorf("read transcript content: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("transcript content is empty")
	}
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws := seedTranscriptWorkspace
		if ws == "" {
			active, aerr := cmdstore.ActiveWorkspace(ctx, h.Store)
			if aerr != nil {
				return aerr
			}
			ws = active
		}

		capability, cerr := appserve.NewInteractionCapabilityWithFleetDB(
			appserve.InteractionConfig{WorkspaceKey: ws},
			h.FleetDBClient(),
		)
		if cerr != nil {
			return fmt.Errorf("compose Interaction transcript seed: %w", cerr)
		}
		request, rerr := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/", nil)
		if rerr != nil {
			return fmt.Errorf("create Interaction authority request: %w", rerr)
		}
		operator, aerr := capability.OperatorAuthorityResolver().ResolveOperatorAuthority(
			request,
			ws,
			interaction.ActionStartSession,
		)
		if aerr != nil {
			return fmt.Errorf("authorize Interaction transcript seed: %w", aerr)
		}
		start, serr := capability.InteractionAPI().StartSession(
			ctx,
			operator,
			interaction.StartSessionCommand{
				WorkspaceKey: ws,
				SessionID:    seedTranscriptSession,
				AgentID:      "distributed-smoke-seed",
				NodeID:       "distributed-smoke-seed-" + uuid.NewString(),
				Kind:         interaction.SessionKindTask,
				TaskID:       seedTranscriptTask,
				Phase:        "seeding_transcript",
				Attempt:      1,
				LeaseID:      "distributed-smoke-lease-" + uuid.NewString(),
				LeaseTTL:     5 * time.Minute,
				Metadata: map[string]string{
					"task_id": seedTranscriptTask,
					"backend": seedTranscriptBackend,
				},
			},
		)
		if serr != nil {
			return fmt.Errorf("start Interaction transcript seed: %w", serr)
		}
		rawToken := start.Token.Bytes()
		start.Token.Close()
		defer clear(rawToken)
		if start.Session == nil || start.Lease == nil || len(rawToken) == 0 {
			return fmt.Errorf(
				"interaction transcript seed omitted session authority material: %w",
				interaction.ErrInvalidPersistedState,
			)
		}

		// The transcript artifact uses the daemon finalize's exact upload path.
		finalized, uerr := store.UploadContentArtifact(ctx, h.Store.Artifacts(), store.ArtifactCreate{
			WorkspaceKey:  ws,
			ArtifactID:    "transcript-" + seedTranscriptSession,
			AgentID:       "distributed-smoke-seed",
			SessionID:     seedTranscriptSession,
			TaskID:        seedTranscriptTask,
			OwnerType:     "session", // fleet-db's valid session-owned artifact owner type
			OwnerID:       seedTranscriptSession,
			Type:          "transcript",
			Summary:       "agent session transcript",
			MIMEType:      "application/x-ndjson",
			DurableStatus: "declared",
			Metadata:      map[string]string{"runtime": "distributed-smoke-seed"},
		}, data)
		if uerr != nil {
			if ferr := finishSeedTranscriptSession(
				ctx, capability, start, rawToken,
				interaction.SessionFailed, "transcript_upload_failed", "",
			); ferr != nil {
				return fmt.Errorf(
					"upload transcript artifact: %w; finish failed seed session: %v",
					uerr,
					ferr,
				)
			}
			return fmt.Errorf("upload transcript artifact: %w", uerr)
		}

		// Finish atomically links the transcript and releases the exact lease
		// generation. The test helper never writes AgentSession/AgentLease
		// stores independently.
		ref := "artifact://" + finalized.ArtifactID
		if ferr := finishSeedTranscriptSession(
			ctx, capability, start, rawToken,
			interaction.SessionCompleted, "", ref,
		); ferr != nil {
			return fmt.Errorf("finish Interaction transcript seed: %w", ferr)
		}
		fmt.Printf("seeded transcript: ws=%s session=%s task=%s ref=%s bytes=%d\n", ws, seedTranscriptSession, seedTranscriptTask, ref, len(data))
		return nil
	})
}

func finishSeedTranscriptSession(
	ctx context.Context,
	capability *appserve.InteractionCapability,
	start interaction.SessionStart,
	rawToken []byte,
	status interaction.SessionStatus,
	errorClass,
	transcriptRef string,
) error {
	if capability == nil || start.Session == nil || start.Lease == nil || len(rawToken) == 0 {
		return interaction.ErrInvalidPersistedState
	}
	token := interaction.NewLeaseToken(rawToken)
	auth, err := capability.SessionAuthorityResolver().ResolveSessionAuthority(
		ctx,
		interaction.ActionFinishSession,
		interaction.SessionAuthorityProof{
			WorkspaceKey: start.Session.WorkspaceKey,
			SessionID:    start.Session.SessionID,
			AgentID:      start.Session.AgentID,
			TerminalID:   start.Session.TerminalID,
			NodeID:       start.Lease.NodeID,
			LeaseID:      start.Lease.LeaseID,
			FencingToken: start.Lease.FencingToken,
			Token:        token,
		},
	)
	token.Close()
	if err != nil {
		return err
	}
	defer auth.SessionOwner().CloseLeaseCredential()
	_, err = capability.InteractionAPI().FinishSession(
		ctx,
		auth,
		interaction.FinishSessionCommand{
			WorkspaceKey:         start.Session.WorkspaceKey,
			SessionID:            start.Session.SessionID,
			Status:               status,
			ErrorClass:           errorClass,
			TranscriptArtifactID: transcriptRef,
		},
	)
	return err
}

func readSeedTranscriptContent(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) //nolint:gosec // test-only CLI flag
}
