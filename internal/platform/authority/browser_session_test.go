package authority

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const browserSessionTestAction Action = "workflowcatalog.approve-version"

func TestLocalBrowserSessionDelegatesExactActionWithoutPersistingOperatorToken(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer, durableToken := newLocalOperatorTestIssuer(t, filepath.Join(t.TempDir(), "operator"), "workspace-a", &now, time.Minute)
	randomBytes := append(
		bytes.Repeat([]byte{0x42}, localOperatorTokenBytes),
		bytes.Repeat([]byte{0x43}, localOperatorTokenBytes)...,
	)
	random := bytes.NewReader(randomBytes)
	broker, err := newLocalBrowserSessionBroker(issuer, random, func() time.Time { return now }, 30*time.Second, 15*time.Minute, browserSessionTestAction)
	if err != nil {
		t.Fatalf("newLocalBrowserSessionBroker: %v", err)
	}
	admission, err := issuer.NewAdmission(OperatorOnly(browserSessionTestAction))
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	launch, err := broker.MintLaunchCode("Bearer "+durableToken, "workspace-a")
	if err != nil {
		t.Fatalf("MintLaunchCode: %v", err)
	}
	if launch.Code == durableToken || launch.Workspace != "workspace-a" || len(launch.Code) != localOperatorTokenBytes*2 {
		t.Fatalf("launch = %+v", launch)
	}
	session, err := broker.ExchangeLaunchCode(launch.Code, "workspace-a")
	if err != nil {
		t.Fatalf("ExchangeLaunchCode: %v", err)
	}
	if session.Bearer == launch.Code || session.Bearer == durableToken {
		t.Fatal("browser session reused a durable or launch credential")
	}
	auth, err := broker.IssueOperator("Bearer "+session.Bearer, "workspace-a", browserSessionTestAction)
	if err != nil {
		t.Fatalf("IssueOperator: %v", err)
	}
	if auth.Subject() != localBrowserSubject || auth.Workspace() != "workspace-a" || auth.Action() != browserSessionTestAction {
		t.Fatalf("authority = subject %q workspace %q action %q", auth.Subject(), auth.Workspace(), auth.Action())
	}
	if err := admission.RequireOperator(browserSessionTestAction, "workspace-a", auth); err != nil {
		t.Fatalf("browser authority did not share admission seal: %v", err)
	}
	if _, err := broker.ExchangeLaunchCode(launch.Code, "workspace-a"); !errors.Is(err, ErrInvalidOperatorToken) {
		t.Fatalf("replayed launch error = %v", err)
	}
}

func TestLocalBrowserSessionFailsClosedOnScopeActionAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer, durableToken := newLocalOperatorTestIssuer(t, filepath.Join(t.TempDir(), "operator"), "workspace-a", &now, time.Minute)
	random := bytes.NewReader(bytes.Repeat([]byte{0x24}, localOperatorTokenBytes*4))
	broker, err := newLocalBrowserSessionBroker(issuer, random, func() time.Time { return now }, time.Second, 2*time.Second, browserSessionTestAction)
	if err != nil {
		t.Fatalf("newLocalBrowserSessionBroker: %v", err)
	}

	if _, err := broker.MintLaunchCode(durableToken, "workspace-b"); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("wrong launch workspace error = %v", err)
	}
	launch, err := broker.MintLaunchCode(durableToken, "workspace-a")
	if err != nil {
		t.Fatalf("MintLaunchCode: %v", err)
	}
	if _, err := broker.ExchangeLaunchCode(launch.Code, "workspace-b"); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("wrong exchange workspace error = %v", err)
	}
	if _, err := broker.ExchangeLaunchCode(launch.Code, "workspace-a"); !errors.Is(err, ErrInvalidOperatorToken) {
		t.Fatalf("wrong-workspace exchange did not consume launch: %v", err)
	}

	launch, err = broker.MintLaunchCode(durableToken, "workspace-a")
	if err != nil {
		t.Fatalf("second MintLaunchCode: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := broker.ExchangeLaunchCode(launch.Code, "workspace-a"); !errors.Is(err, ErrInvalidOperatorToken) {
		t.Fatalf("expired launch error = %v", err)
	}

	launch, err = broker.MintLaunchCode(durableToken, "workspace-a")
	if err != nil {
		t.Fatalf("third MintLaunchCode: %v", err)
	}
	session, err := broker.ExchangeLaunchCode(launch.Code, "workspace-a")
	if err != nil {
		t.Fatalf("ExchangeLaunchCode: %v", err)
	}
	if _, err := broker.IssueOperator(session.Bearer, "workspace-a", "workflowcatalog.activate-version"); !errors.Is(err, ErrActionNotAllowed) {
		t.Fatalf("wrong action error = %v", err)
	}
	if _, err := broker.IssueOperator(session.Bearer, "workspace-b", browserSessionTestAction); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("wrong session workspace error = %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := broker.IssueOperator(session.Bearer, "workspace-a", browserSessionTestAction); !errors.Is(err, ErrInvalidOperatorToken) {
		t.Fatalf("expired session error = %v", err)
	}
}

func TestLocalBrowserLaunchExchangeIsSingleUseUnderConcurrency(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	issuer, durableToken := newLocalOperatorTestIssuer(t, filepath.Join(t.TempDir(), "operator"), "workspace-a", &now, time.Minute)
	random := bytes.NewReader(bytes.Repeat([]byte{0x66}, localOperatorTokenBytes*16))
	broker, err := newLocalBrowserSessionBroker(issuer, random, func() time.Time { return now }, time.Minute, time.Minute, browserSessionTestAction)
	if err != nil {
		t.Fatalf("newLocalBrowserSessionBroker: %v", err)
	}
	launch, err := broker.MintLaunchCode(durableToken, "workspace-a")
	if err != nil {
		t.Fatalf("MintLaunchCode: %v", err)
	}

	const callers = 8
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, exchangeErr := broker.ExchangeLaunchCode(launch.Code, "workspace-a"); exchangeErr == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful exchanges = %d, want 1", successes)
	}
}

func TestRuntimeWideIssuerBrowserSessionRetainsLaunchWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	runtimeDir := filepath.Join(t.TempDir(), "operator")
	issuer, err := loadOrCreateLocalOperatorCredentialScope(runtimeDir, "", true, localOperatorDependencies{
		random: bytes.NewReader(bytes.Repeat([]byte{0x11}, localOperatorTokenBytes)),
		now:    func() time.Time { return now },
		ttl:    time.Minute,
	})
	if err != nil {
		t.Fatalf("runtime issuer: %v", err)
	}
	durable, err := ReadLocalOperatorToken(runtimeDir)
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	randomBytes := bytes.Join([][]byte{
		bytes.Repeat([]byte{0x22}, localOperatorTokenBytes),
		bytes.Repeat([]byte{0x33}, localOperatorTokenBytes),
		bytes.Repeat([]byte{0x44}, localOperatorTokenBytes),
		bytes.Repeat([]byte{0x55}, localOperatorTokenBytes),
	}, nil)
	broker, err := newLocalBrowserSessionBroker(issuer, bytes.NewReader(randomBytes), func() time.Time { return now }, time.Minute, time.Minute, browserSessionTestAction)
	if err != nil {
		t.Fatalf("newLocalBrowserSessionBroker: %v", err)
	}
	launch, err := broker.MintLaunchCode(durable, "workspace-a")
	if err != nil {
		t.Fatalf("MintLaunchCode: %v", err)
	}
	session, err := broker.ExchangeLaunchCode(launch.Code, "workspace-a")
	if err != nil {
		t.Fatalf("ExchangeLaunchCode: %v", err)
	}
	auth, err := broker.IssueOperator(session.Bearer, "workspace-a", browserSessionTestAction)
	if err != nil {
		t.Fatalf("IssueOperator(workspace-a): %v", err)
	}
	if auth.Workspace() != "workspace-a" {
		t.Fatalf("authority workspace = %q, want workspace-a", auth.Workspace())
	}
	if _, err := broker.IssueOperator(session.Bearer, "workspace-b", browserSessionTestAction); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("cross-workspace session authority error = %v", err)
	}

	otherLaunch, err := broker.MintLaunchCode(durable, "workspace-b")
	if err != nil {
		t.Fatalf("MintLaunchCode(workspace-b): %v", err)
	}
	otherSession, err := broker.ExchangeLaunchCode(otherLaunch.Code, "workspace-b")
	if err != nil {
		t.Fatalf("ExchangeLaunchCode(workspace-b): %v", err)
	}
	otherAuth, err := broker.IssueOperator(otherSession.Bearer, "workspace-b", browserSessionTestAction)
	if err != nil {
		t.Fatalf("IssueOperator(workspace-b): %v", err)
	}
	if otherAuth.Workspace() != "workspace-b" {
		t.Fatalf("authority workspace = %q, want workspace-b", otherAuth.Workspace())
	}
}
