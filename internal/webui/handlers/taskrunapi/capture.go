package taskrunapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/loomgit/remotecapture"
)

type captureParams struct {
	RepoURL    string `json:"repoUrl"`
	BaseSHA    string `json:"baseSha"`
	CaptureSHA string `json:"captureSha"`
	TreeHash   string `json:"treeHash"`
	Complete   bool   `json:"complete"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
}

func captureAttempt(runID string, metadata map[string]string) string {
	number, _ := strconv.Atoi(metadata["scheduler_attempt"])
	if number < 0 {
		number = 0
	}
	return driverpkg.TaskCopyAttemptID(runID, number)
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
