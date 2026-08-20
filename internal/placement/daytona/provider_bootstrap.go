package daytona

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	apiclient "github.com/daytonaio/daytona/libs/api-client-go"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/placement"
)

const (
	// bootstrapInstallRetries and bootstrapInstallMaxTimeSeconds bound the
	// in-sandbox curl. Retries cover transient network blips; the max-time is a
	// backstop under the prep budget (the exec timeout still fences the whole
	// command via the caller's context).
	bootstrapInstallRetries        = 3
	bootstrapInstallMaxTimeSeconds = 120
	// bootstrapBinaryMinBytes is a size floor the downloaded file must clear
	// before it is installed. `test -s` only rejects an empty file; the floor
	// additionally rejects a small error page a proxy might return with HTTP
	// 200 (which curl -f cannot catch). A loom binary is tens of MB, so 1 MiB
	// is a safe floor that will not false-reject a real binary.
	bootstrapBinaryMinBytes = 1 << 20
)

// installBootstrapBinary downloads and atomically installs the served binary
// into the sandbox before the lead PTY starts. It FAILS HARD: any exec error is
// returned so provisioning aborts rather than silently booting the baked
// binary. The command is built purely from serve-supplied config
// (URL/dest/mode), never from sandbox-resident input.
func (p *Provider) installBootstrapBinary(ctx context.Context, sandbox *apiclient.Sandbox, spec placement.BootstrapBinarySpec) error {
	cmd := bootstrapInstallCommand(spec)
	if _, err := p.execLeadPrep(ctx, sandbox, cmd); err != nil {
		return fmt.Errorf("install bootstrap binary at %q: %w", strings.TrimSpace(spec.Dest), err)
	}
	return nil
}

// bootstrapInstallCommand builds the atomic download+install shell command:
// curl into a sibling .tmp, verify it is non-empty and above the size floor,
// chmod it, then rename into place. mv within one filesystem is atomic, so a
// download killed mid-transfer never leaves a half-written binary at Dest --
// and because install fails hard, a killed download aborts provisioning
// entirely. Assumes spec has passed validateBootstrapBinarySpec.
func bootstrapInstallCommand(spec placement.BootstrapBinarySpec) string {
	dest := strings.TrimSpace(spec.Dest)
	tmp := dest + ".tmp"
	quotedTmp := shellQuote(tmp)
	return "curl -fSL --retry " + strconv.Itoa(bootstrapInstallRetries) +
		" --max-time " + strconv.Itoa(bootstrapInstallMaxTimeSeconds) +
		" -o " + quotedTmp + " " + shellQuote(strings.TrimSpace(spec.URL)) +
		" && test -s " + quotedTmp +
		" && [ \"$(wc -c < " + quotedTmp + ")\" -ge " + strconv.Itoa(bootstrapBinaryMinBytes) + " ]" +
		" && chmod " + strings.TrimSpace(spec.Mode) + " " + quotedTmp +
		" && mv -f " + quotedTmp + " " + shellQuote(dest)
}

// validateBootstrapBinarySpec fails closed on any malformed field. Dest must be
// absolute, Mode must be octal, and URL must be a non-empty http(s) URL.
func validateBootstrapBinarySpec(spec placement.BootstrapBinarySpec) error {
	dest := strings.TrimSpace(spec.Dest)
	if dest == "" || !strings.HasPrefix(dest, "/") {
		return fmt.Errorf("bootstrap binary dest %q must be absolute: %w", dest, domain.ErrInvalid)
	}
	mode := strings.TrimSpace(spec.Mode)
	if !isOctalMode(mode) {
		return fmt.Errorf("bootstrap binary mode %q must be octal: %w", mode, domain.ErrInvalid)
	}
	return validateBootstrapURL(spec.URL)
}

func validateBootstrapURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("bootstrap binary url required: %w", domain.ErrInvalid)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("bootstrap binary url %q must be an http(s) URL: %w", raw, domain.ErrInvalid)
	}
	return nil
}
