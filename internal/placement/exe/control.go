// Package exe implements placement.Provider for exe.dev.
//
// exe.dev splits cleanly in two, and the split drives this package's shape:
//
//   - A control plane at POST https://exe.dev/exec, which takes a command
//     STRING (not argv), answers in NEWLINE-DELIMITED JSON, and can create,
//     list and destroy VMs. It cannot run anything inside one.
//   - Everything in-VM, which is SSH only. There is no exec API, no file API
//     and no PTY API, so process control is tmux over SSH.
//
// It also cannot stop or start a VM, so it declares SupportsParking() false.
//
// Every behavior encoded here was verified against the live service during
// Phase 0 (see .scratch/lead-in-daytona/evidence/); the surprises are called
// out at the code that depends on them, because the vendor docs contradict
// several of them.
package exe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultEndpoint is the exe.dev control-plane URL.
const DefaultEndpoint = "https://exe.dev/exec"

// ErrUnsafeArg is a pre-dispatch validation failure. It is the ONLY error in
// this package that proves nothing was sent, which is what makes it the only
// source of placement.CreateOutcomeNotDispatched.
var ErrUnsafeArg = errors.New("exe: argument rejected by local allowlist")

// ErrDeleteUnconfirmed means the server neither confirmed the delete nor
// proved the VM is gone. It is deliberately NOT an absence signal: only a
// point read may conclude absence.
var ErrDeleteUnconfirmed = errors.New("exe: delete not confirmed")

// controlClient talks to the exe.dev control plane.
type controlClient struct {
	token    string
	endpoint string
	http     *http.Client
}

func newControlClient(token, endpoint string, timeout time.Duration) *controlClient {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &controlClient{
		token:    token,
		endpoint: endpoint,
		http:     &http.Client{Timeout: timeout},
	}
}

type execResult struct {
	Status int
	Body   string
	Err    string
}

// exec runs one control-plane command. The request body is a command string.
func (c *controlClient) exec(ctx context.Context, cmd string) (*execResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(cmd))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		// Transport failure: the request MAY have been dispatched. Never
		// downgrade this to "nothing happened".
		return &execResult{}, fmt.Errorf("exe control request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	out := &execResult{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	out.Err = errorText(out.Body)
	return out, nil
}

// decodeStream parses a control-plane body.
//
// LIVE FINDING (Phase 0 gate 7): /exec answers in NEWLINE-DELIMITED JSON, not
// a single object. `rm <absent>` returns HTTP 200 with TWO objects:
//
//	{"error":"VM \"x\" not found"}
//	{"deleted":[],"failed":["x"]}
//
// A single json.Unmarshal over the whole body fails with "Extra data", which
// makes a server-reported error silently invisible. Every response must be
// read as a stream.
func decodeStream(body string) []json.RawMessage {
	dec := json.NewDecoder(strings.NewReader(body))
	var out []json.RawMessage
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out
		}
		out = append(out, raw)
	}
}

// errorText returns the first error reported anywhere in the stream.
func errorText(body string) string {
	for _, raw := range decodeStream(body) {
		var probe struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Error != "" {
			return probe.Error
		}
	}
	return ""
}

// findObject returns the first object in the stream that both decodes into dst
// and satisfies present. Needed because the result object may be preceded by
// error lines.
func findObject(body string, dst any, present func() bool) bool {
	for _, raw := range decodeStream(body) {
		if json.Unmarshal(raw, dst) == nil && present() {
			return true
		}
	}
	return false
}

// Argument allowlists.
//
// Values are interpolated into a command STRING and the server-side parser is
// undocumented, so quoting cannot be proven safe. Fail closed: every
// interpolated value must match a strict allowlist, checked before any I/O.
var (
	reVMName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)
	reTag    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	reSize   = regexp.MustCompile(`^[0-9]+(gb|mb|GB|MB)?$`)
	reImage  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,127}$`)
	reEnvKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	reEnvVal = regexp.MustCompile(`^[A-Za-z0-9._/:=+-]{0,255}$`)
)

func checkArg(field, val string, re *regexp.Regexp) error {
	if !re.MatchString(val) {
		return fmt.Errorf("%w: %s=%q", ErrUnsafeArg, field, val)
	}
	return nil
}

// vm is the control plane's VM view.
type vm struct {
	Name   string `json:"vm_name"`
	Status string `json:"status"`
	// Tags is ABSENT (not []) on an untagged VM -- live-verified, evidence 61.
	// It decodes to nil, which every label match must tolerate.
	Tags []string `json:"tags"`
	// The API exposes no ssh_user or ssh_port; those are provider constants,
	// not read values. An earlier draft invented both fields.
	SSHHost   string `json:"ssh_host"`
	TermURL   string `json:"terminal_url"`
	Image     string `json:"image"`
	Region    string `json:"region"`
	CreatedAt string `json:"created_at"`
}

// listEnvelope distinguishes "the server returned a vms array" from "the
// server returned something else that happens to decode".
type listEnvelope struct {
	VMs *[]vm `json:"vms"`
}

// list returns VMs; a name makes it a point read.
//
// Absence requires HTTP 200, no error field, AND a present vms key. Without
// all three a 403 body ({"error":...}) unmarshals cleanly to an empty slice,
// making an AUTHORIZATION FAILURE indistinguishable from PROVEN ABSENCE --
// which, under the two-pass absence protocol, turns an expiring token into a
// fleet-wide false release.
func (c *controlClient) list(ctx context.Context, name string) ([]vm, error) {
	cmd := "ls --json"
	if name != "" {
		if err := checkArg("name", name, reVMName); err != nil {
			return nil, err
		}
		cmd = fmt.Sprintf("ls %s --json", name)
	}
	res, err := c.exec(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if res.Status != http.StatusOK {
		return nil, fmt.Errorf("exe ls: http %d: %.200s", res.Status, res.Body)
	}
	if res.Err != "" {
		return nil, fmt.Errorf("exe ls: server error: %s", res.Err)
	}
	var envelope listEnvelope
	if !findObject(res.Body, &envelope, func() bool { return envelope.VMs != nil }) {
		return nil, fmt.Errorf("exe ls: no vms object in response, refusing to infer absence: %.200s", res.Body)
	}
	return *envelope.VMs, nil
}

type createOpts struct {
	Name   string
	CPU    int
	Memory string
	Disk   string
	Image  string
	Tags   []string
	Env    map[string]string
}

// buildCreate validates every argument and serializes the command. It performs
// NO I/O, so a failure here is provably not dispatched.
func (o createOpts) buildCreate() (string, error) {
	if err := checkArg("name", o.Name, reVMName); err != nil {
		return "", err
	}
	var b bytes.Buffer
	b.WriteString("new --json --no-email")
	fmt.Fprintf(&b, " --name=%s", o.Name)
	if o.CPU > 0 {
		if o.CPU > 64 {
			return "", fmt.Errorf("%w: cpu=%d", ErrUnsafeArg, o.CPU)
		}
		fmt.Fprintf(&b, " --cpu=%d", o.CPU)
	}
	for _, f := range []struct {
		flag, val string
		re        *regexp.Regexp
	}{
		{"memory", o.Memory, reSize},
		{"disk", o.Disk, reSize},
		{"image", o.Image, reImage},
	} {
		if f.val == "" {
			continue
		}
		if err := checkArg(f.flag, f.val, f.re); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, " --%s=%s", f.flag, f.val)
	}
	for _, t := range o.Tags {
		if err := checkArg("tag", t, reTag); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, " --tag=%s", t)
	}
	// Sorted so the command is deterministic and testable; map order is not.
	for _, k := range sortedKeys(o.Env) {
		if err := checkArg("env key", k, reEnvKey); err != nil {
			return "", err
		}
		if err := checkArg("env value", o.Env[k], reEnvVal); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, " --env %s=%s", k, o.Env[k])
	}
	return b.String(), nil
}

// createOutcome mirrors placement.CreateOutcome without importing it here.
type createOutcome int

const (
	outcomeUnknown createOutcome = iota
	outcomeNotDispatched
	outcomeCreated
)

// create dispatches a VM creation.
//
// The ONLY not-dispatched path is local validation failing before exec. Every
// post-dispatch failure is Unknown -- including 422, which for a duplicate
// name PROVES a same-name VM exists and so is the single most dangerous status
// to report as "nothing happened".
func (c *controlClient) create(ctx context.Context, o createOpts) (createOutcome, error) {
	cmd, err := o.buildCreate()
	if err != nil {
		return outcomeNotDispatched, err
	}
	res, err := c.exec(ctx, cmd)
	if err != nil {
		return outcomeUnknown, err
	}
	if res.Status == http.StatusOK && res.Err == "" {
		// Require the server to actually name the VM before claiming Created.
		var envelope listEnvelope
		if findObject(res.Body, &envelope, func() bool { return envelope.VMs != nil && len(*envelope.VMs) > 0 }) {
			return outcomeCreated, nil
		}
		var single vm
		if findObject(res.Body, &single, func() bool { return single.Name != "" }) {
			return outcomeCreated, nil
		}
		return outcomeUnknown, fmt.Errorf("exe create: 200 without a VM identity: %.200s", res.Body)
	}
	return outcomeUnknown, fmt.Errorf("exe create: http %d: %.200s", res.Status, res.Body)
}

// remove deletes a VM.
//
// The body MUST be parsed: live-verified that `rm <absent>` returns HTTP 200
// with {"deleted":[],"failed":["<name>"]}. Trusting the status code would mark
// a live, billing VM as released. A `failed` entry is ambiguous (absent OR a
// delete error), so it yields ErrDeleteUnconfirmed rather than a not-found.
func (c *controlClient) remove(ctx context.Context, name string) error {
	if err := checkArg("name", name, reVMName); err != nil {
		return err
	}
	res, err := c.exec(ctx, "rm "+name)
	if err != nil {
		return err
	}
	if res.Status != http.StatusOK {
		return fmt.Errorf("exe rm %q: http %d: %.200s", name, res.Status, res.Body)
	}
	var out struct {
		Deleted *[]string `json:"deleted"`
		Failed  []string  `json:"failed"`
	}
	if !findObject(res.Body, &out, func() bool { return out.Deleted != nil }) {
		return fmt.Errorf("exe rm %q: no deleted/failed object in response: %.200s", name, res.Body)
	}
	for _, d := range *out.Deleted {
		if d == name {
			return nil
		}
	}
	return fmt.Errorf("exe rm %q (failed=%v): %w", name, out.Failed, ErrDeleteUnconfirmed)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
