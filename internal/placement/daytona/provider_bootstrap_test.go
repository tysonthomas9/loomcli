package daytona

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

func validBootstrapSpec() *placement.BootstrapBinarySpec {
	return &placement.BootstrapBinarySpec{
		URL:  "https://serve.example.com/api/lead/bootstrap/loom",
		Dest: "/usr/local/bin/loom",
		Mode: "0755",
	}
}

func TestPrepareLeadBootInstallsBootstrapBinaryAtomically(t *testing.T) {
	var command string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		command = req.Command
		return executeBody(0, "", "")
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		BootstrapBinary: validBootstrapSpec(),
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	for _, want := range []string{
		"curl -fSL --retry 3 --max-time 120 -o '/usr/local/bin/loom.tmp' 'https://serve.example.com/api/lead/bootstrap/loom'",
		"test -s '/usr/local/bin/loom.tmp'",
		"wc -c < '/usr/local/bin/loom.tmp'",
		"chmod 0755 '/usr/local/bin/loom.tmp'",
		// Atomicity: stage into .tmp then rename into place, so a download killed
		// mid-transfer never leaves a half-written binary at the install path.
		"mv -f '/usr/local/bin/loom.tmp' '/usr/local/bin/loom'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("bootstrap command %q missing %q", command, want)
		}
	}
}

// Fail-hard: an install exec error must abort PrepareLeadBoot rather than fall
// back to the baked binary.
func TestPrepareLeadBootBootstrapFailsHard(t *testing.T) {
	provider := newPrepProvider(t, func(_ toolboxExecuteRequest) string {
		return executeBody(6, "", "curl: (6) could not resolve host")
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		BootstrapBinary: validBootstrapSpec(),
	})
	if err == nil {
		t.Fatal("PrepareLeadBoot = nil, want fail-hard install error")
	}
	if !strings.Contains(err.Error(), "install bootstrap binary") {
		t.Fatalf("error = %v, want install-bootstrap failure", err)
	}
}

// The bootstrap install must run before the checkout so a failed download bails
// before the more expensive clone.
func TestPrepareLeadBootInstallsBootstrapBeforeCheckout(t *testing.T) {
	var commands []string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		commands = append(commands, req.Command)
		if strings.Contains(req.Command, "rev-parse") {
			return executeBody(0, "git", "") // checkout already present -> skip clone
		}
		return executeBody(0, "", "")
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		BootstrapBinary: validBootstrapSpec(),
		Repo: &placement.RepoClone{
			Name:      "repo",
			RemoteURL: "https://github.com/o/r",
			Checkout:  "/root/workspace/repo",
		},
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	if len(commands) < 2 {
		t.Fatalf("commands = %v, want bootstrap then checkout probe", commands)
	}
	if !strings.Contains(commands[0], "curl -fSL") {
		t.Fatalf("first command = %q, want bootstrap install", commands[0])
	}
	if !strings.Contains(commands[1], "rev-parse") {
		t.Fatalf("second command = %q, want checkout probe", commands[1])
	}
}

// Invalid specs must be rejected before any sandbox call (fail closed).
func TestPrepareLeadBootRejectsInvalidBootstrapSpec(t *testing.T) {
	for name, spec := range map[string]placement.BootstrapBinarySpec{
		"relative dest":   {URL: "https://s/x", Dest: "usr/local/bin/loom", Mode: "0755"},
		"empty dest":      {URL: "https://s/x", Dest: "", Mode: "0755"},
		"non-octal mode":  {URL: "https://s/x", Dest: "/usr/local/bin/loom", Mode: "rwx"},
		"empty mode":      {URL: "https://s/x", Dest: "/usr/local/bin/loom", Mode: ""},
		"empty url":       {URL: "", Dest: "/usr/local/bin/loom", Mode: "0755"},
		"non-http url":    {URL: "ftp://s/x", Dest: "/usr/local/bin/loom", Mode: "0755"},
		"scheme-less url": {URL: "serve.example.com/x", Dest: "/usr/local/bin/loom", Mode: "0755"},
	} {
		t.Run(name, func(t *testing.T) {
			// The prep provider t.Fatalf's on any exec/sandbox request, proving
			// validation short-circuits before the sandbox is touched.
			provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
				t.Fatalf("unexpected exec for invalid spec: %s", req.Command)
				return ""
			})
			specCopy := spec
			err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
				BootstrapBinary: &specCopy,
			})
			if err == nil {
				t.Fatalf("PrepareLeadBoot(%+v) = nil, want validation error", spec)
			}
		})
	}
}
