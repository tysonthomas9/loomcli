// Command agent-attach-check is the e2e harness's "real product code"
// probe of the persistent-agent terminal stack. It builds a real
// agentd.AgentdClient (the same one webui's WS terminal handler uses),
// drives ResolveAgent / EnsureAlive against a running control-plane,
// dials the agentd over mTLS through vm-host's per-VM bridge, opens a
// session via Terminal/Attach, echoes a token, and verifies the token
// round-trips through the pty + bridge + control-plane plumbing.
//
// Unlike the harness-side agent_attach_check that builds a hand-rolled
// gRPC client, this command exercises the same code path that ships in
// loomcli — cache lookups, control-plane fallback, mTLS dial helpers,
// the AgentdClient.AttachSession state machine, and the agentdAttachment
// recv loop. A regression in any of those layers will surface here.
//
// Flags:
//   -cp-addr        host:port of loom-control-plane gRPC service
//   -cp-ca          path to CA PEM that signs the control-plane's server cert.
//                   Today the harness runs control-plane plaintext, so this
//                   path is reused as -agentd-ca; passing it does NOT enable
//                   mTLS to the control-plane (we leave ControlPlaneTLS=nil
//                   per the harness spec).
//   -agentd-ca      path to PEM CA cert that signs agentd's server cert.
//                   In this MVP harness it is the same self-signed CA.
//   -workspace      SessionKey.Workspace passed to AttachSession.
//   -agent          SessionKey.Name passed to AttachSession (also used as
//                   the agent identity for ResolveAgent / EnsureAlive).
//   -expect         token to echo and look for in the AttachOutput stream.
//   -deadline-secs  upper bound on the round-trip wait (default 15 s).
//
// Exit 0 prints "ok" to stdout. Any failure logs the cause to stderr and
// exits 1.
//
// Build:
//   go build -o /tmp/agent-attach-check ./cmd/agent-attach-check
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal/agentd"
)

func main() {
	cpAddr := flag.String("cp-addr", "", "host:port of loom-control-plane gRPC service")
	cpCA := flag.String("cp-ca", "", "path to control-plane CA PEM (currently informational; ControlPlaneTLS is nil per harness spec)")
	agentdCA := flag.String("agentd-ca", "", "path to PEM CA cert that signs agentd's server cert")
	workspace := flag.String("workspace", "", "SessionKey.Workspace passed to AttachSession")
	agent := flag.String("agent", "", "SessionKey.Name passed to AttachSession (also used as agent identity for ResolveAgent)")
	expect := flag.String("expect", "", "token to echo and look for in the AttachOutput stream")
	deadlineSecs := flag.Int("deadline-secs", 15, "upper bound on the round-trip wait")
	flag.Parse()

	// Required flags. -cp-ca is accepted but currently unused: the harness
	// runs the control-plane plaintext, and AgentdClient.New rejects the
	// (ControlPlaneTLS=nil + AgentdRootCAPEM=nil) combination only when
	// ControlPlaneTLS is non-nil — so we deliberately leave it nil here.
	_ = *cpCA

	for name, v := range map[string]string{
		"-cp-addr":   *cpAddr,
		"-agentd-ca": *agentdCA,
		"-workspace": *workspace,
		"-agent":     *agent,
		"-expect":    *expect,
	} {
		if v == "" {
			log.Fatalf("agent-attach-check: %s is required", name)
		}
	}

	caBytes, err := os.ReadFile(*agentdCA)
	if err != nil {
		log.Fatalf("agent-attach-check: read -agentd-ca: %v", err)
	}

	// Build the AgentdClient with the harness wiring: plaintext to the
	// control-plane (ControlPlaneTLS = nil) and mTLS to agentd (the CA
	// here is what verifies the agentd's server cert; the per-attach
	// client cert + key arrive via EnsureAlive's mtls_cert_pem +
	// mtls_key_pem and are slotted in by AgentdClient.routingTLS).
	client, err := agentd.New(agentd.Options{
		ControlPlaneEndpoint: *cpAddr,
		ControlPlaneTLS:      nil,
		AgentdRootCAPEM:      caBytes,
	})
	if err != nil {
		log.Fatalf("agent-attach-check: agentd.New: %v", err)
	}
	defer func() { _ = client.Close() }()

	key := terminal.SessionKey{Workspace: *workspace, Name: *agent}

	// AttachSession can race the agentd's mTLS bring-up: control-plane's
	// readinessDial only verifies the host-side bridge port is listening,
	// not that agentd inside the guest has bound vsock and finished
	// mTLS init. A freshly-spawned VM can return "connection reset by
	// peer" mid-handshake for a few seconds. Retry with a small backoff
	// before giving up — production webui hits this same race and the
	// AgentdClient's reconnect loop covers it once a stream exists, but
	// the initial dial has no retry.
	var att terminal.Attachment
	const maxAttempts = 5
	for i := 0; i < maxAttempts; i++ {
		att, _, err = client.AttachSession(key, 80, 24, nil)
		if err == nil {
			break
		}
		if i == maxAttempts-1 {
			log.Fatalf("agent-attach-check: AttachSession (after %d attempts): %v", maxAttempts, err)
		}
		log.Printf("agent-attach-check: AttachSession attempt %d failed: %v; retrying in 2s", i+1, err)
		time.Sleep(2 * time.Second)
	}
	defer client.Detach(key, att.ConnID())

	// outputBuf accumulates everything the agentd writes to the stream's
	// stdout channel. The reader goroutine is the only writer; main()
	// reads under the same mutex when polling for the expect token.
	var (
		mu        sync.Mutex
		outputBuf strings.Builder
		readDone  = make(chan struct{})
	)
	go func() {
		defer close(readDone)
		for chunk := range att.Output() {
			mu.Lock()
			outputBuf.Write(chunk)
			mu.Unlock()
		}
	}()

	// Drive the round-trip: write `echo <expect>\n` and then poll the
	// accumulated buffer. Done before the deadline → success; deadline
	// elapses → log and exit non-zero.
	cmd := []byte(fmt.Sprintf("echo %s\n", *expect))
	if _, err := att.WriteInput(cmd); err != nil {
		log.Fatalf("agent-attach-check: WriteInput: %v", err)
	}

	deadline := time.Now().Add(time.Duration(*deadlineSecs) * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := outputBuf.String()
		mu.Unlock()
		if strings.Contains(seen, *expect) {
			fmt.Println("ok")
			return
		}
		// Polling cadence: 50 ms is plenty fast (bash's echo round-trip is
		// well under that on a healthy bridge) and cheap (no measurable CPU
		// overhead vs. the gRPC stream itself).
		select {
		case <-readDone:
			// Output channel closed before we saw the token — capture what
			// we have, append the agentd's exitReason if any, and bail.
			mu.Lock()
			tail := outputBuf.String()
			mu.Unlock()
			reason := att.ExitReason()
			log.Fatalf("agent-attach-check: output closed before %q seen; reason=%q got=%q", *expect, reason, tail)
		case <-time.After(50 * time.Millisecond):
		}
	}

	mu.Lock()
	tail := outputBuf.String()
	mu.Unlock()
	log.Fatalf("agent-attach-check: timeout %ds waiting for %q; got=%q", *deadlineSecs, *expect, tail)
}
