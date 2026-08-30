package exe

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

// sshRunner is the one operation lead-boot prep needs, extracted so the
// command construction is testable without a live VM.
type sshRunner interface {
	Run(cmd string) (string, error)
	// RunStdin feeds data on stdin instead of the command line. Secrets go
	// this way: the command string is visible in the VM's process list.
	RunStdin(cmd string, stdin []byte) (string, error)
}

type clientRunner struct{ client *ssh.Client }

func (r clientRunner) Run(cmd string) (string, error) { return run(r.client, cmd) }

func (r clientRunner) RunStdin(cmd string, stdin []byte) (string, error) {
	return runStdin(r.client, cmd, stdin)
}

func (p *Provider) installBootstrapBinary(client *ssh.Client, sandboxID string, spec *placement.BootstrapBinarySpec) error {
	return installBootstrapBinary(clientRunner{client}, sandboxID, spec)
}

// installBootstrapBinary downloads serve's own loom binary and installs it
// atomically, before anything else, so the lead boots the freshly served
// binary rather than the one baked into the image.
func installBootstrapBinary(client sshRunner, sandboxID string, spec *placement.BootstrapBinarySpec) error {
	mode := strings.TrimSpace(spec.Mode)
	if mode == "" {
		mode = "0755"
	}
	tmp := spec.Dest + ".loom-tmp"
	cmd := fmt.Sprintf(
		"curl -fsSL --retry 3 --max-time 120 -o %s %s && chmod %s %s && mv -f %s %s",
		shellQuote(tmp), shellQuote(spec.URL),
		shellQuote(mode), shellQuote(tmp),
		shellQuote(tmp), shellQuote(spec.Dest),
	)
	if out, err := client.Run(cmd); err != nil {
		return fmt.Errorf("exe install bootstrap binary in sandbox %q: %w (%s)", sandboxID, err, firstLine(out))
	}
	return nil
}

func (p *Provider) cloneRepo(client *ssh.Client, sandboxID string, prep placement.LeadBootPrep) error {
	return cloneRepo(clientRunner{client}, sandboxID, prep)
}

// cloneRepo checks the lead's repo out.
//
// Two things here are load-bearing and neither is obvious:
//
//	The token never appears in a command string. sshd runs a remote command as
//	`sh -c '<string>'`, so a token interpolated there is readable from the VM's
//	process list by every process in the VM -- including the lead, which is the
//	one principal that must never hold it. It is fed on stdin instead. (A
//	URL-embedded token would be worse still: it also persists in .git/config.)
//
//	The credential helper is removed WHETHER OR NOT the clone succeeds. Chaining
//	the cleanup with && leaves the token on disk on exactly the paths where
//	something already went wrong.
func cloneRepo(client sshRunner, sandboxID string, prep placement.LeadBootPrep) error {
	repo := prep.Repo
	checkout := strings.TrimSpace(repo.Checkout)
	if checkout == "" {
		return fmt.Errorf("exe clone in sandbox %q: checkout path required", sandboxID)
	}
	var token string
	if prep.GitToken != nil {
		resolved, err := prep.GitToken()
		if err != nil {
			return fmt.Errorf("exe resolve git token for sandbox %q: %w", sandboxID, err)
		}
		token = resolved
	}

	clone := fmt.Sprintf("git clone --depth 1 %s %s", shellQuote(repo.RemoteURL), shellQuote(checkout))
	if ref := strings.TrimSpace(repo.Ref); ref != "" {
		clone = fmt.Sprintf("git clone --depth 1 --branch %s %s %s",
			shellQuote(ref), shellQuote(repo.RemoteURL), shellQuote(checkout))
	}

	if token == "" {
		cmd := fmt.Sprintf("mkdir -p %s && %s", shellQuote(dirOf(checkout)), clone)
		if _, err := client.Run(cmd); err != nil {
			// Never surface the output: it can echo credential material.
			return fmt.Errorf("exe clone %q into sandbox %q failed: %w", repo.Name, sandboxID, err)
		}
		return nil
	}

	askpass := checkout + ".loom-askpass"
	// The helper reads the token from stdin (cat), writes it with 0700, runs
	// the clone, then removes the helper unconditionally and re-raises the
	// clone's exit status so a failure is still a failure.
	cmd := fmt.Sprintf(
		"set -e; mkdir -p %s; "+
			"umask 077; printf '#!/bin/sh\ncat %s\n' > %s; chmod 700 %s; "+
			"cat > %s; chmod 600 %s; "+
			"set +e; GIT_ASKPASS=%s GIT_TERMINAL_PROMPT=0 %s; rc=$?; "+
			"rm -f %s %s; exit $rc",
		shellQuote(dirOf(checkout)),
		shellQuote(askpass+".tok"), shellQuote(askpass), shellQuote(askpass),
		shellQuote(askpass+".tok"), shellQuote(askpass+".tok"),
		shellQuote(askpass), clone,
		shellQuote(askpass), shellQuote(askpass+".tok"),
	)
	if _, err := client.RunStdin(cmd, []byte(token)); err != nil {
		return fmt.Errorf("exe clone %q into sandbox %q failed: %w", repo.Name, sandboxID, err)
	}
	return nil
}
