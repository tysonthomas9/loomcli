package taskrunapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/loomgit/remotecapture"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type captureParams struct {
	RepoURL    string `json:"repoUrl"`
	BaseSHA    string `json:"baseSha"`
	CaptureSHA string `json:"captureSha"`
	TreeHash   string `json:"treeHash"`
	Complete   bool   `json:"complete"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	SandboxID  string `json:"sandboxId"`
	RepoDir    string `json:"repoDir"`
}

func captureAttempt(runID string, metadata map[string]string) string {
	if prior := metadata["remote_capture_attempt"]; prior != "" {
		prefix := strings.TrimSuffix(driverpkg.TaskCopyAttemptID(runID, 0), "-a1") + "-a"
		if strings.HasPrefix(prior, prefix) {
			if number, err := strconv.Atoi(strings.TrimPrefix(prior, prefix)); err == nil && number > 0 {
				return prior
			}
		}
	}
	number, _ := strconv.Atoi(metadata["scheduler_attempt"])
	if number < 0 {
		number = 0
	}
	return driverpkg.TaskCopyAttemptID(runID, number)
}

func (m *Module) captureState(ctx context.Context, ws string, id leaseIdentity, _ []byte) (any, error) {
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"status":    run.RuntimeMetadata["remote_capture_status"],
		"sandboxId": run.RuntimeMetadata["daytona_sandbox_id"],
		"attempt":   run.RuntimeMetadata["remote_capture_attempt"],
		"baseSha":   run.RuntimeMetadata["remote_capture_base_sha"],
		"repoUrl":   run.RuntimeMetadata["remote_capture_repo_url"],
		"repoDir":   run.RuntimeMetadata["daytona_repo_dir"],
	}, nil
}

func (m *Module) captureToken(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[captureParams](body)
	if err != nil {
		return nil, err
	}
	owner := fmt.Sprintf("%s:%d", id.TaskRunID, id.FencingToken)
	attempt := captureAttempt(id.TaskRunID, run.RuntimeMetadata)
	token, ref, err := remotecapture.Prepare(ctx, m.captureJournalPath, ws, attempt, params.RepoURL, params.BaseSHA, owner)
	if err != nil {
		return nil, err
	}
	return map[string]string{"token": token, "attempt": attempt, "ref": ref}, nil
}

func (m *Module) captureRegister(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[captureParams](body)
	if err != nil {
		return nil, err
	}
	if params.SandboxID == "" || params.RepoDir == "" {
		return nil, fmt.Errorf("sandbox ID and repository directory are required")
	}
	attempt := captureAttempt(id.TaskRunID, run.RuntimeMetadata)
	owner := fmt.Sprintf("%s:%d", id.TaskRunID, id.FencingToken)
	if _, _, err := remotecapture.Prepare(ctx, m.captureJournalPath, ws, attempt, params.RepoURL, params.BaseSHA, owner); err != nil {
		return nil, err
	}
	metadata := map[string]string{
		"remote_capture_status": "pending", "remote_capture_attempt": attempt,
		"remote_capture_base_sha": params.BaseSHA, "remote_capture_repo_url": params.RepoURL,
		"daytona_sandbox_id": params.SandboxID, "daytona_repo_dir": params.RepoDir,
	}
	if _, err := m.store.TaskRuns().Heartbeat(ctx, ws, id.TaskRunID, store.TaskRunHeartbeat{
		NodeID: id.NodeID, LeaseID: id.LeaseID, LeaseToken: id.LeaseToken,
		FencingToken: id.FencingToken, RuntimeMetadata: metadata, HeartbeatAt: m.now(),
	}); err != nil {
		return nil, err
	}
	return map[string]string{"attempt": attempt}, nil
}

func (m *Module) captureFinalize(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[captureParams](body)
	if err != nil {
		return nil, err
	}
	owner := fmt.Sprintf("%s:%d", id.TaskRunID, id.FencingToken)
	revision, err := remotecapture.Finalize(ctx, m.captureJournalPath, remotecapture.FinalizeInput{
		Workspace: ws, Attempt: captureAttempt(id.TaskRunID, run.RuntimeMetadata), Task: run.TaskID,
		RepoURL: params.RepoURL, BaseSHA: params.BaseSHA, CaptureSHA: params.CaptureSHA,
		TreeHash: params.TreeHash, Outcome: params.Outcome, Complete: params.Complete, Owner: owner,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"changeId": revision.Change, "revision": revision.Number, "headSha": revision.HeadSHA}, nil
}

func (m *Module) capturePending(ctx context.Context, ws string, id leaseIdentity, body []byte) (any, error) {
	run, err := m.verifyLease(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	params, err := decodeParams[captureParams](body)
	if err != nil {
		return nil, err
	}
	err = remotecapture.Remember(ctx, m.captureJournalPath, remotecapture.FinalizeInput{
		Workspace: ws, Attempt: captureAttempt(id.TaskRunID, run.RuntimeMetadata), Task: run.TaskID,
		RepoURL: params.RepoURL, BaseSHA: params.BaseSHA, CaptureSHA: params.CaptureSHA,
		TreeHash: params.TreeHash, Outcome: params.Outcome, Complete: params.Complete,
		Owner: fmt.Sprintf("%s:%d", id.TaskRunID, id.FencingToken),
	}, params.Reason)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"retained": true}, nil
}

func (m *Module) capturePush(w http.ResponseWriter, r *http.Request) {
	remotecapture.ServeHTTP(w, r, m.captureJournalPath, r.PathValue("ws"))
}
