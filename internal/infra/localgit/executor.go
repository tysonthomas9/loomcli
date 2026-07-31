// Package localgit adapts the hardened machine-local Git implementation to
// Source Control and Connectors-owned ports.
package localgit

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/gitauth"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// Executor keeps the credential source private to Connectors. Source Control
// receives neither this object nor the ephemeral gitauth.Credential it
// resolves. A nil source preserves anonymous/SSH/local Git behavior.
type Executor struct {
	source gitauth.Source
}

var _ connectors.GitReadExecutor = (*Executor)(nil)

func New(source gitauth.Source) *Executor {
	return &Executor{source: source}
}

func (e *Executor) ValidateGitRead(
	ctx context.Context,
	command connectors.GitReadCommand,
) (string, error) {
	if e == nil {
		return "", connectors.ErrUnavailable
	}
	if err := validateGitReadCommand(command); err != nil {
		return "", fmt.Errorf("%w: invalid bounded Git read: %v", connectors.ErrInvalid, err)
	}
	canonicalTarget, err := canonicalCheckoutTarget(
		ctx,
		command.WorkspacePath,
		command.TargetPath,
	)
	if err != nil {
		return "", fmt.Errorf("%w: Git read containment validation failed: %w", connectors.ErrInvalid, err)
	}
	if command.Operation == connectors.GitReadFetchRef {
		if err := validateExactRemote(ctx, command); err != nil {
			return "", err
		}
	}
	return canonicalTarget, nil
}

func (e *Executor) ExecuteGitRead(ctx context.Context, command connectors.GitReadCommand) error {
	if e == nil {
		return connectors.ErrUnavailable
	}
	switch command.Operation {
	case connectors.GitReadClone:
		if _, err := e.ValidateGitRead(ctx, command); err != nil {
			return err
		}
		return e.executeClone(ctx, command)
	case connectors.GitReadFetchRef:
		if _, err := e.ValidateGitRead(ctx, command); err != nil {
			return err
		}
		if err := localworkspace.FetchGitRefAnonymous(
			ctx,
			command.TargetPath,
			command.RemoteName,
			command.SourceRef,
			command.DestinationRef,
		); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.source == nil {
			if err := fetchRefAmbient(ctx, command); err != nil {
				return fmt.Errorf("bounded Git fetch failed: %w", err)
			}
			return nil
		}
		credential, err := e.source.Resolve(ctx, command.RemoteURL)
		if err != nil {
			return fmt.Errorf("resolve bounded Git fetch credential: %w", err)
		}
		if credential == nil {
			if err := fetchRefAmbient(ctx, command); err != nil {
				return fmt.Errorf("bounded Git fetch failed: %w", err)
			}
			return nil
		}
		defer credential.Close()
		if err := fetchRefWithCredential(ctx, command, credential); err != nil {
			return fmt.Errorf("bounded Git fetch failed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: Git read %q", connectors.ErrUnsupportedOperation, command.Operation)
	}
}

func (e *Executor) executeClone(ctx context.Context, command connectors.GitReadCommand) error {
	if strings.TrimSpace(command.RemoteName) == "" {
		command.RemoteName = "origin"
	}
	if err := localworkspace.CloneRepoToAnonymous(
		ctx,
		command.RemoteURL,
		command.RemoteName,
		command.TargetPath,
	); err == nil {
		return nil
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	if e.source == nil {
		if err := cloneAmbient(ctx, command); err != nil {
			return fmt.Errorf("bounded Git clone failed: %w", err)
		}
		return nil
	}
	credential, err := e.source.Resolve(ctx, command.RemoteURL)
	if err != nil {
		return fmt.Errorf("resolve bounded Git clone credential: %w", err)
	}
	if credential == nil {
		if err := cloneAmbient(ctx, command); err != nil {
			return fmt.Errorf("bounded Git clone failed: %w", err)
		}
		return nil
	}
	defer credential.Close()
	if err := cloneWithCredential(ctx, command, credential); err != nil {
		return fmt.Errorf("bounded Git clone failed: %w", err)
	}
	return nil
}

func validateExactRemote(ctx context.Context, command connectors.GitReadCommand) error {
	if strings.TrimSpace(command.RemoteName) == "" {
		return fmt.Errorf("%w: checkout remote name is empty", connectors.ErrInvalid)
	}
	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = maxGitRemoteOutput
	stderr.limit = maxGitRemoteOutput
	// command.TargetPath has passed canonicalCheckoutTarget and RemoteName has
	// passed validateGitReadCommand plus the defensive non-empty check above.
	//nolint:gosec // Fixed Git subcommand with validated bounded coordinates.
	process := exec.CommandContext(
		ctx,
		"git",
		"-C",
		command.TargetPath,
		"remote",
		"get-url",
		command.RemoteName,
	)
	process.Env = localGitEnv()
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	clearCommandEnv(process)
	if err != nil {
		stdout.zero()
		stderr.zero()
		return fmt.Errorf("%w: checkout remote is unavailable", connectors.ErrInvalid)
	}
	observed := bytes.TrimSpace(stdout.bytes())
	matched := bytes.Equal(observed, []byte(command.RemoteURL))
	stdout.zero()
	stderr.zero()
	if !matched {
		return fmt.Errorf("%w: checkout remote does not match the admitted repository", connectors.ErrInvalid)
	}
	return nil
}
