package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestDefaultProviderRegistryUsesRealProvidersByDefault(t *testing.T) {
	t.Setenv(FakeProviderEnvVar, "")
	ResetRecordingProviderCalls()

	provider, err := DefaultProviderRegistry(nil).Get(domain.ConnectorSourceGitHub)
	if err != nil {
		t.Fatalf("Get github provider: %v", err)
	}
	result, err := provider.Call(context.Background(), providers.CallSpec{
		Action: providers.ActionGitHubPullRequestRead,
	})
	if err == nil {
		t.Fatal("real provider call without required args succeeded")
	}
	if result.Decision == domain.ConnectorCallGranted {
		t.Fatalf("result = %+v, want non-granted decision", result)
	}
	if calls := RecordingProviderCalls(); len(calls) != 0 {
		t.Fatalf("recording calls = %+v, want none when %s is unset", calls, FakeProviderEnvVar)
	}
}

func TestDefaultProviderRegistryUsesRecordingProviderWhenEnabled(t *testing.T) {
	t.Setenv(FakeProviderEnvVar, "recording")
	recordPath := filepath.Join(t.TempDir(), "connector-provider-calls.jsonl")
	t.Setenv(FakeProviderRecordPathEnvVar, recordPath)
	ResetRecordingProviderCalls()
	t.Cleanup(ResetRecordingProviderCalls)

	provider, err := DefaultProviderRegistry(nil).Get(domain.ConnectorSourceGitHub)
	if err != nil {
		t.Fatalf("Get github provider: %v", err)
	}
	result, err := provider.Call(context.Background(), providers.CallSpec{
		Action:         providers.ActionGitHubPullRequestRead,
		Resource:       "repo:octocat/hello",
		Args:           map[string]any{"repo": "hello", "number": 7, "owner": "octocat"},
		IdempotencyKey: "run-1#github.pull_request.read#1",
		Credential:     "ghp_should_not_be_recorded",
	})
	if err != nil {
		t.Fatalf("Call recording provider: %v", err)
	}
	if result.Decision != domain.ConnectorCallGranted || result.Status != 200 {
		t.Fatalf("result = %+v, want granted/200", result)
	}
	if result.Body["fakeProvider"] != true || result.Body["credentialPresented"] != true {
		t.Fatalf("body = %+v, want fakeProvider and credentialPresented markers", result.Body)
	}

	calls := RecordingProviderCalls()
	if len(calls) != 1 {
		t.Fatalf("recording calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.SourceKind != "github" || got.Action != providers.ActionGitHubPullRequestRead ||
		got.Resource != "repo:octocat/hello" || !got.CredentialPresented {
		t.Fatalf("recorded call = %+v", got)
	}
	if strings.Join(got.ArgKeys, ",") != "number,owner,repo" {
		t.Fatalf("arg keys = %v, want sorted keys only", got.ArgKeys)
	}

	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read record path: %v", err)
	}
	if !strings.Contains(string(raw), `"credential_presented":true`) ||
		!strings.Contains(string(raw), providers.ActionGitHubPullRequestRead) {
		t.Fatalf("record file = %s, want recorded call JSONL", raw)
	}
	if strings.Contains(string(raw), "ghp_should_not_be_recorded") {
		t.Fatalf("record file leaked credential: %s", raw)
	}
}

func TestRecordingProviderRejectsUnknownActions(t *testing.T) {
	t.Setenv(FakeProviderEnvVar, "1")
	ResetRecordingProviderCalls()
	t.Cleanup(ResetRecordingProviderCalls)

	provider, err := DefaultProviderRegistry(nil).Get(domain.ConnectorSourceGitHub)
	if err != nil {
		t.Fatalf("Get github provider: %v", err)
	}
	result, err := provider.Call(context.Background(), providers.CallSpec{
		Action:     "github.repo.delete",
		Resource:   "repo:octocat/hello",
		Credential: "ghp_secret",
	})
	if !errors.Is(err, providers.ErrUnknownAction) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown action err = %v, want ErrUnknownAction wrapping ErrInvalid", err)
	}
	if result.Decision != domain.ConnectorCallUpstreamError {
		t.Fatalf("result = %+v, want upstream_error decision", result)
	}
	if calls := RecordingProviderCalls(); len(calls) != 0 {
		t.Fatalf("recording calls = %+v, want none for unknown action", calls)
	}
}

func TestRecordingProviderPreservesProviderPreconditionFailures(t *testing.T) {
	t.Setenv(FakeProviderEnvVar, "1")
	ResetRecordingProviderCalls()
	t.Cleanup(ResetRecordingProviderCalls)

	github, err := DefaultProviderRegistry(nil).Get(domain.ConnectorSourceGitHub)
	if err != nil {
		t.Fatalf("Get github provider: %v", err)
	}
	result, err := github.Call(context.Background(), providers.CallSpec{
		Action:     providers.ActionGitHubReviewPost,
		Resource:   "repo:octocat/hello",
		Credential: "ghp_secret",
	})
	var pre *providers.PreconditionRequired
	if !errors.As(err, &pre) || len(pre.Fields) != 1 || pre.Fields[0] != "expectedHeadSha" {
		t.Fatalf("review.post err = %v, want expectedHeadSha precondition", err)
	}
	if result.Decision != domain.ConnectorCallPreconditionRequired {
		t.Fatalf("review.post result = %+v, want precondition_required", result)
	}

	datadog, err := DefaultProviderRegistry(nil).Get(domain.ConnectorSourceDatadog)
	if err != nil {
		t.Fatalf("Get datadog provider: %v", err)
	}
	result, err = datadog.Call(context.Background(), providers.CallSpec{
		Action:     providers.ActionDatadogIncidentsWrite,
		Resource:   "monitor:123",
		Credential: "dd_secret",
	})
	if !errors.As(err, &pre) || len(pre.Fields) != 1 || pre.Fields[0] != "monitorId" {
		t.Fatalf("incidents.write err = %v, want monitorId precondition", err)
	}
	if result.Decision != domain.ConnectorCallPreconditionRequired {
		t.Fatalf("incidents.write result = %+v, want precondition_required", result)
	}
	if calls := RecordingProviderCalls(); len(calls) != 0 {
		t.Fatalf("recording calls = %+v, want none for precondition failures", calls)
	}
}
