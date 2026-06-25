package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
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

		// 1) The agent session the non-owning serve node will resolve. task_id is set
		//    on both the column and the metadata so the transcript route's ownership
		//    check matches either way.
		if _, cerr := h.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{
			WorkspaceKey: ws,
			SessionID:    seedTranscriptSession,
			AgentID:      "distributed-smoke-seed",
			TaskID:       seedTranscriptTask,
			Status:       domain.AgentSessionCompleted,
			Metadata:     map[string]string{"task_id": seedTranscriptTask, "backend": seedTranscriptBackend},
		}); cerr != nil && !errors.Is(cerr, domain.ErrAlreadyExists) {
			return fmt.Errorf("create agent session: %w", cerr)
		}

		// 2) The transcript artifact — the daemon finalize's exact upload path.
		finalized, uerr := store.UploadContentArtifact(ctx, h.Store.Artifacts(), store.ArtifactCreate{
			WorkspaceKey:  ws,
			ArtifactID:    "transcript-" + seedTranscriptSession,
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
			return fmt.Errorf("upload transcript artifact: %w", uerr)
		}

		// 3) Point the session at the artifact — the cross-node read key.
		ref := "artifact://" + finalized.ArtifactID
		meta := map[string]string{"task_id": seedTranscriptTask, "backend": seedTranscriptBackend, "transcript_ref": ref}
		if _, perr := h.Store.AgentSessions().Update(ctx, ws, seedTranscriptSession, store.AgentSessionUpdate{Metadata: &meta}); perr != nil {
			return fmt.Errorf("set transcript_ref: %w", perr)
		}
		fmt.Printf("seeded transcript: ws=%s session=%s task=%s ref=%s bytes=%d\n", ws, seedTranscriptSession, seedTranscriptTask, ref, len(data))
		return nil
	})
}

func readSeedTranscriptContent(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) //nolint:gosec // test-only CLI flag
}
