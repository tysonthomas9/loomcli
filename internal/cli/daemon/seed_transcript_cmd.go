package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cliagent "github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	seedLogWorkspace string
	seedLogAgent     string
	seedLogFile      string
)

// daemonSeedLogCmd is part of the TEST-ONLY seeding seam (docs/adr/0001). It
// appends content through the same archive-log writer used by the supervisor.
var daemonSeedLogCmd = &cobra.Command{
	Use:    "seed-log",
	Short:  "TEST-ONLY: append content to an agent's archive log via the product's own writer",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDaemonSeedLog,
}

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
	logFlags := daemonSeedLogCmd.Flags()
	logFlags.StringVar(&seedLogWorkspace, "workspace", "", "Workspace key (required)")
	logFlags.StringVar(&seedLogAgent, "agent", "", "Agent name (required)")
	logFlags.StringVar(&seedLogFile, "content", "", "Log content file (default: stdin)")
	daemonCmd.AddCommand(daemonSeedLogCmd)

	f := daemonSeedTranscriptCmd.Flags()
	f.StringVar(&seedTranscriptWorkspace, "workspace", "", "Workspace key (default: active)")
	f.StringVar(&seedTranscriptSession, "session", "", "Agent session id (required)")
	f.StringVar(&seedTranscriptTask, "task", "", "Task id the session belongs to (required)")
	f.StringVar(&seedTranscriptBackend, "backend", "codex", "Backend label")
	f.StringVar(&seedTranscriptFile, "content", "", "Canonical NDJSON transcript file (default: stdin)")
	daemonCmd.AddCommand(daemonSeedTranscriptCmd)
}

func runDaemonSeedLog(_ *cobra.Command, _ []string) error {
	if err := requireTestSupport(); err != nil {
		return err
	}
	if seedLogWorkspace == "" || seedLogAgent == "" {
		return fmt.Errorf("--workspace and --agent are required")
	}
	data, err := readSeedContent(seedLogFile)
	if err != nil {
		return fmt.Errorf("read log content: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("log content is empty")
	}
	f, err := cliagent.OpenAgentArchiveLog(seedLogWorkspace, seedLogAgent)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // best-effort close after explicit write check
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("append agent archive log: %w", err)
	}
	fmt.Printf("seeded log: ws=%s agent=%s bytes=%d path=%s\n", seedLogWorkspace, seedLogAgent, len(data), f.Name())
	return nil
}

//nolint:funlen // CLI command wires validation, transcript parsing, store lookup, and session update in one path.
func runDaemonSeedTranscript(_ *cobra.Command, _ []string) error {
	if err := requireTestSupport(); err != nil {
		return err
	}
	if seedTranscriptSession == "" || seedTranscriptTask == "" {
		return fmt.Errorf("--session and --task are required")
	}
	data, err := readSeedContent(seedTranscriptFile)
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

// requireTestSupport gates the hidden seed-* commands that ship in the
// production binary but are reserved for product-owned test setup.
func requireTestSupport() error {
	if os.Getenv("LOOM_TESTSUPPORT") != "1" {
		return fmt.Errorf("seed commands are test support: set LOOM_TESTSUPPORT=1 to enable")
	}
	return nil
}

func readSeedContent(path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path) //nolint:gosec // G304: test-only CLI flag
}
