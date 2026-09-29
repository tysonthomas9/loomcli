// Package pushproxy accepts attempt-scoped Git smart-HTTP pushes on a Loom host.
package pushproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/capture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/errcode"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
)

type ProxyStore interface {
	Store
	MirrorState(context.Context, string, string) (journal.MirrorRecord, bool, error)
	PutMirrorRecord(context.Context, journal.MirrorRecord) error
}

// Proxy handles receive-pack for a single host-controlled provider remote.
// The caller mounts it at any desired path; token claims pin the local repo.
type Proxy struct {
	Store  ProxyStore
	Remote string
	Push   func(*gitexec.Runner) mirror.RefPusher
}

type update struct{ old, new, ref string }

type clientError string

func (e clientError) Error() string { return string(e) }

func clientReason(err error) string {
	if errors.Is(err, errPushTooLarge) {
		return errPushTooLarge.Error()
	}
	var safe clientError
	if errors.As(err, &safe) {
		return safe.Error()
	}
	var coded *errcode.Error
	if errors.As(err, &coded) && (coded.Kind == errcode.Stale || coded.Kind == errcode.SecretPathRefused) {
		return coded.Error()
	}
	slog.Error("push proxy request failed", "error", err)
	return "push failed; contact the host operator"
}

func internalHTTPError(w http.ResponseWriter, err error) {
	slog.Error("push proxy request failed", "error", err)
	http.Error(w, "push proxy unavailable", http.StatusInternalServerError)
}

func runner(dir string) (*gitexec.Runner, error) {
	return gitexec.New(dir, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.Store == nil || p.Remote == "" {
		http.Error(w, "proxy unavailable", http.StatusServiceUnavailable)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		http.Error(w, "proxy token required", http.StatusUnauthorized)
		return
	}
	claims, err := Verify(r.Context(), p.Store, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		http.Error(w, clientReason(err), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodGet && (r.URL.Query().Get("service") != "git-receive-pack" || !strings.HasSuffix(r.URL.Path, "/info/refs")) ||
		r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/git-receive-pack") {
		http.NotFound(w, r)
		return
	}
	dir, err := os.MkdirTemp("/tmp", "loom-push-quarantine-*")
	if err != nil {
		internalHTTPError(w, err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	git, err := runner(dir)
	if err == nil {
		_, err = git.Run(r.Context(), "init", "--bare")
	}
	if err != nil {
		internalHTTPError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		advert, err := git.Run(r.Context(), "receive-pack", "--stateless-rpc", "--advertise-refs", dir)
		if err != nil {
			internalHTTPError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
		_, _ = w.Write(packet([]byte("# service=git-receive-pack\n")))
		_, _ = w.Write([]byte("0000"))
		_, _ = w.Write(advert)
		return
	}
	p.receive(w, r, claims, git, dir)
}

const maxPushBytes int64 = 2 << 30

var errPushTooLarge = errors.New("push exceeds 2 GiB")

func spoolBody(r io.Reader) (*os.File, error) {
	body, err := os.CreateTemp("/tmp", "loom-push-body-*")
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(body, io.LimitReader(r, maxPushBytes+1))
	if err == nil && n > maxPushBytes {
		err = errPushTooLarge
	}
	if err == nil {
		_, err = body.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = os.Remove(body.Name())
		_ = body.Close()
		return nil, err
	}
	return body, nil
}

func (p *Proxy) receive(w http.ResponseWriter, r *http.Request, claims Claims, git *gitexec.Runner, dir string) {
	body, err := spoolBody(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errPushTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, clientReason(err), status)
		return
	}
	defer os.Remove(body.Name())
	defer body.Close()
	updates, sideband, packAt, err := readUpdates(body, claims)
	if err != nil {
		http.Error(w, "invalid push request", 400)
		return
	}
	// index-pack writes only inside the disposable quarantine repository.
	if _, err := body.Seek(packAt, io.SeekStart); err != nil {
		internalHTTPError(w, err)
		return
	}
	if _, err := git.RunInput(r.Context(), body, "index-pack", "--stdin", "--fix-thin"); err != nil {
		writeReport(w, updates, sideband, clientReason(err))
		return
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		internalHTTPError(w, err)
		return
	}
	report, err := git.RunInput(r.Context(), body, "receive-pack", "--stateless-rpc", dir)
	if err != nil {
		writeReport(w, updates, sideband, clientReason(err))
		return
	}
	for _, change := range updates {
		installed, err := git.Run(r.Context(), "rev-parse", "--verify", change.ref)
		if err != nil || strings.TrimSpace(string(installed)) != change.new {
			writeReport(w, updates, sideband, "quarantine ref update failed")
			return
		}
	}
	accepted, err := p.validateAndForward(r.Context(), claims, git, updates)
	if err != nil {
		writeReport(w, updates, sideband, clientReason(err), accepted...)
		return
	}
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	_, _ = w.Write(report)
}

func (p *Proxy) validateAndForward(ctx context.Context, claims Claims, git *gitexec.Runner, updates []update) ([]string, error) {
	base, err := refname.AttemptBase(claims.Workspace, claims.Attempt)
	if err != nil {
		return nil, err
	}
	host, err := runner(claims.Repo)
	if err != nil {
		return nil, err
	}
	baseSHA, err := host.Run(ctx, "rev-parse", "--verify", base)
	if err != nil {
		return nil, errors.New("attempt base missing")
	}
	basePaths, err := treePaths(ctx, host, strings.TrimSpace(string(baseSHA)))
	if err != nil {
		return nil, err
	}
	var total int64
	seenBlobs := make(map[string]bool)
	for _, change := range updates {
		commits, err := git.Run(ctx, "rev-list", change.new, "^"+strings.TrimSpace(string(baseSHA)))
		if err != nil {
			return nil, err
		}
		for _, commit := range strings.Fields(string(commits)) {
			if err := validateTree(ctx, git, commit, basePaths, &total, seenBlobs); err != nil {
				return nil, err
			}
		}
	}
	if err := current(ctx, p.Store, claims, nowUTC()); err != nil {
		return nil, err
	}
	push := mirror.NewPusher(git)
	if p.Push != nil {
		push = p.Push(git)
	}
	var accepted []string
	for _, change := range updates {
		if err := current(ctx, p.Store, claims, nowUTC()); err != nil {
			return accepted, err
		}
		if err := p.forwardOne(ctx, push, host, git.Path(), change); err != nil {
			return accepted, err
		}
		accepted = append(accepted, change.ref)
	}
	return accepted, nil
}

func (p *Proxy) forwardOne(ctx context.Context, push mirror.RefPusher, host *gitexec.Runner, quarantine string, change update) error {
	prior, found, err := p.Store.MirrorState(ctx, host.Path(), change.ref)
	if err != nil {
		return err
	}
	actual, err := push.RemoteSHA(ctx, p.Remote, change.ref)
	if err != nil {
		return err
	}
	if actual != change.new && actual != "" && (!found || prior.SHA != actual || prior.Remote != p.Remote || prior.State != "mirrored") {
		return clientError("remote ref moved")
	}
	if actual != change.new {
		if err := push.Push(ctx, p.Remote, change.ref, change.new, actual); err != nil {
			return clientError("provider rejected: " + providerReason(err))
		}
	}
	if err := retainOnHost(ctx, host, quarantine, change, prior, found); err != nil {
		return err
	}
	return p.Store.PutMirrorRecord(ctx, journal.MirrorRecord{Repo: host.Path(), Ref: change.ref, Remote: p.Remote, SHA: change.new, State: "mirrored"})
}

func retainOnHost(ctx context.Context, host *gitexec.Runner, quarantine string, change update, prior journal.MirrorRecord, found bool) error {
	if _, err := host.Run(ctx, "fetch", "--no-tags", quarantine, change.ref); err != nil {
		return err
	}
	fetched, err := host.Run(ctx, "rev-parse", "--verify", "FETCH_HEAD")
	if err != nil || strings.TrimSpace(string(fetched)) != change.new {
		return errors.New("host fetch did not preserve accepted SHA")
	}
	old := strings.Repeat("0", len(change.new))
	if exists, err := gitexec.RefExists(host.Path(), change.ref); err != nil {
		return err
	} else if exists {
		current, err := host.Run(ctx, "rev-parse", "--verify", change.ref)
		if err != nil {
			return err
		}
		old = strings.TrimSpace(string(current))
		if old != change.new && (!found || old != prior.SHA) {
			return clientError("host ref moved")
		}
	}
	if err := host.UpdateRef(ctx, change.ref, change.new, old); err != nil {
		return err
	}
	return nil
}

func providerReason(err error) string {
	var command *gitexec.CommandError
	if errors.As(err, &command) && command.Stderr != "" {
		for _, line := range strings.Split(command.Stderr, "\n") {
			if reason, ok := strings.CutPrefix(strings.TrimSpace(line), "remote: "); ok && reason != "" {
				return reason
			}
		}
	}
	return "push refused"
}

func nowUTC() time.Time { return time.Now().UTC() }

func readUpdates(body *os.File, claims Claims) ([]update, bool, int64, error) {
	reader := bufio.NewReader(body)
	baseRef, err := refname.AttemptBase(claims.Workspace, claims.Attempt)
	if err != nil {
		return nil, false, 0, err
	}
	prefix := strings.TrimSuffix(baseRef, "base")
	var changes []update
	var sideband bool
	for {
		header := make([]byte, 4)
		if _, err := io.ReadFull(reader, header); err != nil {
			return nil, false, 0, err
		}
		n, err := strconv.ParseUint(string(header), 16, 16)
		if err != nil || n != 0 && (n < 4 || n > 65520) {
			return nil, false, 0, errors.New("invalid pkt-line")
		}
		if n == 0 {
			break
		}
		line := make([]byte, int(n)-4)
		if _, err := io.ReadFull(reader, line); err != nil {
			return nil, false, 0, err
		}
		if len(changes) == 0 {
			if before, caps, ok := bytes.Cut(line, []byte{0}); ok {
				line = before
				sideband = bytes.Contains(caps, []byte("side-band-64k")) || bytes.Contains(caps, []byte("side-band"))
			}
		}
		change, err := parseUpdate(line, prefix)
		if err != nil {
			return nil, false, 0, err
		}
		changes = append(changes, change)
		if len(changes) > 32 {
			return nil, false, 0, errors.New("too many ref updates")
		}
	}
	if len(changes) == 0 {
		return nil, false, 0, errors.New("no ref update")
	}
	offset, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, false, 0, err
	}
	return changes, sideband, offset - int64(reader.Buffered()), nil
}

func parseUpdate(line []byte, prefix string) (update, error) {
	fields := strings.Fields(strings.TrimSpace(string(line)))
	if len(fields) != 3 || !validSHA(fields[0]) || !validSHA(fields[1]) || fields[2] != prefix+"capture" ||
		gitexec.CheckRefFormat(fields[2], false) != nil || zeroSHA(fields[1]) {
		return update{}, errors.New("ref update refused")
	}
	return update{old: fields[0], new: fields[1], ref: fields[2]}, nil
}

func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func zeroSHA(s string) bool { return strings.Trim(s, "0") == "" }

func treePaths(ctx context.Context, git *gitexec.Runner, sha string) (map[string]string, error) {
	out, err := git.Run(ctx, "ls-tree", "-r", "-z", sha)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string)
	for _, entry := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		if len(entry) != 0 {
			meta, path, ok := bytes.Cut(entry, []byte{'\t'})
			fields := strings.Fields(string(meta))
			if !ok || len(fields) != 3 {
				return nil, errors.New("invalid base tree entry")
			}
			paths[string(path)] = fields[2]
		}
	}
	return paths, nil
}

func validateTree(ctx context.Context, git *gitexec.Runner, sha string, basePaths map[string]string, total *int64, seenBlobs map[string]bool) error {
	out, err := git.Run(ctx, "ls-tree", "-r", "-l", "-z", sha)
	if err != nil {
		return err
	}
	return validateTreeEntries(out, basePaths, total, seenBlobs)
}

func validateTreeEntries(out []byte, basePaths map[string]string, total *int64, seenBlobs map[string]bool) error {
	for _, entry := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return errors.New("invalid tree entry")
		}
		fields := strings.Fields(string(meta))
		if len(fields) != 4 {
			return errors.New("invalid tree entry")
		}
		name := string(path)
		_, tracked := basePaths[name]
		if capture.SecretPath(name) && !tracked {
			return errcode.New(errcode.SecretPathRefused, name, nil)
		}
		if fields[1] == "blob" {
			if basePaths[name] == fields[2] || seenBlobs[fields[2]] {
				continue
			}
			seenBlobs[fields[2]] = true
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				return err
			}
			if size > capture.MaxFileBytes {
				return clientError("file exceeds 100 MiB: " + name)
			}
			if size > maxPushBytes-*total {
				return clientError("push exceeds 2 GiB")
			}
			*total += size
		}
	}
	return nil
}

func packet(payload []byte) []byte {
	return append([]byte(fmt.Sprintf("%04x", len(payload)+4)), payload...)
}

func writeReport(w http.ResponseWriter, changes []update, sideband bool, reason string, accepted ...string) {
	reason = strings.NewReplacer("\n", " ", "\r", " ", "\x00", " ").Replace(reason)
	if len(reason) > 250 {
		reason = reason[:250]
	}
	var report []byte
	report = append(report, packet([]byte("unpack ok\n"))...)
	okRefs := make(map[string]bool, len(accepted))
	for _, ref := range accepted {
		okRefs[ref] = true
	}
	for _, change := range changes {
		status := "ng " + change.ref + " " + reason + "\n"
		if okRefs[change.ref] {
			status = "ok " + change.ref + "\n"
		}
		report = append(report, packet([]byte(status))...)
	}
	report = append(report, []byte("0000")...)
	if sideband {
		report = append(packet(append([]byte{1}, report...)), []byte("0000")...)
	}
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	_, _ = w.Write(report)
}
