package lead

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
)

// resolveLeadResume turns the --continue / --resume flags into a concrete
// target, or refuses.
//
// Refusing is the contract. `loom lead` itself is best-effort about the store
// -- it launches fine with fleet-db down -- but a resume CANNOT be: without the
// store there is no id to resume, and quietly launching a fresh conversation
// is exactly the data loss the flags exist to prevent. So every error here is
// fatal to the launch, and the caller exits non-zero.
//
// Returns (nil, nil) when neither flag was given.
func resolveLeadResume(ctx context.Context, workDir, backendName string, args []string) (*leadcontrol.ResumeTarget, error) {
	req, err := leadResumeRequest(workDir, backendName, args)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, nil
	}

	openCtx, cancel := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancel()
	handle, ws, ok := openLeadSessionStore(openCtx)
	if !ok {
		return nil, fmt.Errorf(
			"cannot resume: loom's session store is unavailable, so there is no session id to resume. " +
				"Start fleet-db (or run plain 'loom lead' for a fresh session)")
	}
	defer func() { _ = handle.Close() }()

	listCtx, cancelList := context.WithTimeout(ctx, leadStoreOpTimeout)
	defer cancelList()
	records, err := leadcontrol.ListLeadSessions(listCtx, handle.Store, ws, req.AgentID, leadcontrol.LeadSessionListOptions{})
	if err != nil {
		return nil, fmt.Errorf("cannot resume: %w", err)
	}
	target, err := leadcontrol.ResolveResumeTarget(records, *req)
	if err != nil {
		return nil, err
	}
	announceLeadResume(target)
	return target, nil
}

// leadResumeRequest validates the flag combination and builds the request.
// Returns (nil, nil) when no resume was asked for. The usage errors here are
// raised BEFORE any store access, as they are decidable from the flags alone.
func leadResumeRequest(workDir, backendName string, args []string) (*leadcontrol.ResumeRequest, error) {
	ref := strings.TrimSpace(leadResume)
	if leadContinue && ref != "" {
		return nil, fmt.Errorf("--continue and --resume are mutually exclusive; " +
			"use --continue (or a bare --resume) for the latest session, or --resume <id> for a specific one")
	}
	ref, err := applyResumePositional(ref, args)
	if err != nil {
		return nil, err
	}
	req := leadcontrol.ResumeRequest{
		Continue: leadContinue,
		Ref:      ref,
		Backend:  backendName,
		WorkDir:  workDir,
		AgentID:  resolveLeadAgentID(),
	}
	if !req.Requested() {
		return nil, nil
	}
	// The uncontrolled path is a plain interactive launch with no session
	// plumbing at all, so it has nowhere to put a resume id. Name the env var:
	// it is usually set in a shell profile and forgotten.
	if backends.LeadControlDisabled() {
		return nil, fmt.Errorf("cannot resume while the controlled lead runtime is disabled by %s; "+
			"unset it (or set it to 1) and retry", "LOOM_LEAD_CONTROLLED")
	}
	return &req, nil
}

// leadArgs accepts the single trailing token that `loom lead --resume <id>`
// produces.
//
// --resume carries a NoOptDefVal so a bare --resume means "the latest", and
// pflag only ever takes an optional-value flag's value in the `--resume=<id>`
// form -- a space-separated id is left as a positional argument instead. The
// documented form is `--resume <id>`, so it is absorbed here rather than
// rejected as an unknown command. Anything else stays an error.
func leadArgs(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if strings.TrimSpace(leadResume) == "" {
		return fmt.Errorf("unknown argument %q; 'loom lead' takes no positional arguments "+
			"(to resume, use 'loom lead --resume %s')", args[0], args[0])
	}
	if len(args) > 1 {
		return fmt.Errorf("accepts at most 1 argument (the --resume session id), received %d", len(args))
	}
	return nil
}

// applyResumePositional folds `--resume <id>`'s trailing token into the ref.
func applyResumePositional(ref string, args []string) (string, error) {
	if len(args) == 0 {
		return ref, nil
	}
	positional := strings.TrimSpace(args[0])
	if ref != leadcontrol.ResumeLatestSentinel {
		return "", fmt.Errorf("--resume was given both a value (%q) and an argument (%q); pass exactly one session id",
			ref, positional)
	}
	return positional, nil
}

// announceLeadResume tells the operator which session was chosen and how it
// was matched, so an ambiguous-looking id is never ambiguous in hindsight.
func announceLeadResume(target *leadcontrol.ResumeTarget) {
	if target == nil {
		return
	}
	handle := target.HarnessSessionID
	if handle == "" {
		handle = target.CodexThreadID
	}
	if handle == "" && target.UseCodexLast {
		handle = "codex's most recent thread"
	}
	fmt.Printf("Resuming lead session %s (matched by %s) -> %s\n", target.Record.SessionID, target.MatchedBy, handle)
	if target.SkippedNoHandle > 0 {
		fmt.Printf("  skipped %d more recent session(s) with no recorded resume id\n", target.SkippedNoHandle)
	}
	for _, w := range target.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}
	fmt.Println()
}

// seedResumeMetadata records the ancestry and the provider handle on the NEW
// orchestration row.
//
// Seeding the handle (rather than waiting for the runtime watcher to scrape
// it) is what makes resume chainable: if this process dies before the watcher
// persists anything, the row still resolves for the next --continue. The two
// ancestry keys are kept separate from the live handle so a session-id rotation
// reads as a chain instead of overwriting the evidence.
func seedResumeMetadata(metadata map[string]string, resume *leadcontrol.ResumeTarget) {
	if metadata == nil || resume == nil {
		return
	}
	metadata[leadcontrol.MetadataLeadResumedFrom] = resume.Record.SessionID
	switch {
	case resume.HarnessSessionID != "":
		metadata[leadcontrol.MetadataHarnessSessionID] = resume.HarnessSessionID
		metadata[leadcontrol.MetadataLeadResumedHarnessID] = resume.HarnessSessionID
	case resume.CodexThreadID != "":
		metadata[leadcontrol.MetadataCodexThreadID] = resume.CodexThreadID
		metadata[leadcontrol.MetadataLeadResumedHarnessID] = resume.CodexThreadID
	}
}

// leadRuntimeOptions assembles the controlled-launch options, carrying the
// resolved resume handle through to the backend.
func leadRuntimeOptions(
	registration leadSessionRegistration,
	workDir, prompt, backendName string,
	resume *leadcontrol.ResumeTarget,
) backends.ControlledLeadOptions {
	opts := backends.ControlledLeadOptions{
		Store:     registration.Store(),
		Workspace: registration.Workspace,
		LeadName:  registration.AgentID,
		SessionID: registration.SessionID,
		WorkDir:   workDir,
		Prompt:    prompt,
		Backend:   backendName,
	}
	if resume != nil {
		opts.ResumeHarnessSessionID = resume.HarnessSessionID
		opts.ResumeCodexThreadID = resume.CodexThreadID
		opts.ResumeLast = resume.UseCodexLast
	}
	return opts
}
