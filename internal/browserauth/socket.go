//go:build !windows

package browserauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// OperatorSocketServer serves issue/refresh/revoke on a mode-0600 Unix socket
// inside a mode-0700 directory owned by the current user. Every connection's
// peer UID must equal the server's UID. Different OS users are refused both by
// file permissions and by the peer check; same-UID processes are inside the
// documented POC trust boundary.
// peerUIDLookup is replaced in tests to simulate a foreign peer.
var peerUIDLookup = peerUID

type OperatorSocketServer struct {
	path      string
	created   os.FileInfo // the socket file this server bound; Close removes only it
	listener  net.Listener
	registry  *OperatorSessionRegistry
	validate  WorkspaceValidator
	logger    *slog.Logger
	uid       uint32
	peerUID   func(net.Conn) (uint32, error)
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// ListenOperatorSocket creates the socket at path. It fails closed: if the
// directory cannot be made private, the path is too long, or peer credentials
// are unsupported on this platform, no socket is created.
func ListenOperatorSocket(path string, registry *OperatorSessionRegistry, validate WorkspaceValidator, logger *slog.Logger) (*OperatorSocketServer, error) {
	if registry == nil {
		return nil, errors.New("operator socket: registry is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if len(path) > maxUnixSocketPath {
		return nil, fmt.Errorf("operator socket: path %q exceeds %d bytes", path, maxUnixSocketPath)
	}
	if !peerCredentialsSupported() {
		return nil, errors.New("operator socket: peer credentials are not supported on this platform")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("operator socket: mkdir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs the execute bit; 0700 is owner-only
		return nil, fmt.Errorf("operator socket: chmod dir: %w", err)
	}
	if err := checkPrivatePath(dir, true); err != nil {
		return nil, fmt.Errorf("operator socket: %w", err)
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("operator socket: listen: %w", err)
	}
	// Close decides whether to unlink (only our own file); the listener's
	// default unlink-on-close would remove a socket another runtime rebound.
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("operator socket: chmod: %w", err)
	}
	created, err := os.Lstat(path)
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("operator socket: stat: %w", err)
	}
	s := &OperatorSocketServer{path: path, created: created, listener: ln, registry: registry, validate: validate,
		logger: logger, uid: uint32(os.Getuid()), peerUID: peerUIDLookup} //nolint:gosec // uid fits uint32 on unix
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Path returns the socket path.
func (s *OperatorSocketServer) Path() string { return s.path }

// Close stops accepting, waits for in-flight requests, removes the socket
// file and revokes every local operator session.
func (s *OperatorSocketServer) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.listener.Close()
		s.wg.Wait()
		// Remove the path only if it is still the socket this server bound;
		// never delete a socket another runtime has since bound there.
		if current, statErr := os.Lstat(s.path); statErr == nil && os.SameFile(current, s.created) {
			_ = os.Remove(s.path)
		}
		s.registry.RevokeAll()
	})
	return err
}

func (s *OperatorSocketServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.logger.Warn("operator socket accept failed", "err", err)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *OperatorSocketServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(socketIOTimeout))
	uid, err := s.peerUID(conn)
	if err != nil || uid != s.uid {
		// Never reveal anything to a foreign or unverifiable peer.
		s.logger.Warn("operator socket rejected peer", "peer_uid_ok", err == nil, "err", err)
		writeSocketResponse(conn, SocketResponse{Error: "peer credentials rejected", Code: "peer_rejected"})
		return
	}
	line, err := bufio.NewReader(io.LimitReader(conn, maxSocketRequest)).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		writeSocketResponse(conn, SocketResponse{Error: "malformed request", Code: "bad_request"})
		return
	}
	var req SocketRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeSocketResponse(conn, SocketResponse{Error: "malformed request", Code: "bad_request"})
		return
	}
	writeSocketResponse(conn, s.dispatch(req, uid))
}

func (s *OperatorSocketServer) dispatch(req SocketRequest, uid uint32) SocketResponse {
	switch req.Op {
	case OpIssue:
		if s.validate != nil {
			ctx, cancel := context.WithTimeout(context.Background(), socketIOTimeout)
			err := s.validate(ctx, req.Workspace)
			cancel()
			if err != nil {
				return SocketResponse{Error: "workspace not available", Code: "workspace_invalid"}
			}
		}
		token, sess, err := s.registry.Issue(req.Workspace, uid)
		if err != nil {
			return SocketResponse{Error: err.Error(), Code: "issue_failed"}
		}
		s.logger.Info("local operator session issued", "session_id", sess.ID, "workspace", sess.Workspace)
		return sessionResponse(sess, token)
	case OpRefresh:
		sess, err := s.registry.Refresh(req.Token)
		if err != nil {
			return SocketResponse{Error: "session not active", Code: "session_inactive"}
		}
		return sessionResponse(sess, "")
	case OpRevoke:
		revoked := s.registry.Revoke(req.Token)
		s.logger.Info("local operator session revoke requested", "revoked", revoked)
		return SocketResponse{OK: true}
	default:
		return SocketResponse{Error: "unknown operation", Code: "bad_request"}
	}
}

func sessionResponse(sess OperatorSession, token string) SocketResponse {
	return SocketResponse{OK: true, Token: token, SessionID: sess.ID, Workspace: sess.Workspace,
		ExpiresAt: sess.ExpiresAt(), AbsoluteExpiresAt: sess.AbsoluteExpiresAt(),
		IdleTimeoutSecs: int(OperatorIdleTTL / time.Second)}
}

func writeSocketResponse(w io.Writer, resp SocketResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

// staleSocketDialTimeout bounds the liveness probe before a leftover socket
// is removed.
const staleSocketDialTimeout = 500 * time.Millisecond

// removeStaleSocket deletes a leftover socket from a previous run. Anything
// at path that is not a socket owned by this user is left alone and reported,
// and so is a socket that still accepts connections (another live runtime).
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("operator socket: stat: %w", err)
	}
	if info.Mode()&fs.ModeSocket == 0 || !ownedByCurrentUser(info) {
		return fmt.Errorf("operator socket: %s exists and is not a socket owned by this user", path)
	}
	if conn, dialErr := net.DialTimeout("unix", path, staleSocketDialTimeout); dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("operator socket: %s is in use by another runtime", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("operator socket: remove stale socket: %w", err)
	}
	return nil
}

// CallOperatorSocket sends one request and returns the response. It refuses
// to connect unless the socket and its directory are private to this user,
// so a planted socket cannot harvest a bearer.
func CallOperatorSocket(ctx context.Context, path string, req SocketRequest) (SocketResponse, error) {
	if err := checkPrivatePath(filepath.Dir(path), true); err != nil {
		return SocketResponse{}, fmt.Errorf("operator socket: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return SocketResponse{}, fmt.Errorf("operator socket unavailable: %w", err)
	}
	if info.Mode()&fs.ModeSocket == 0 || !ownedByCurrentUser(info) || info.Mode().Perm()&0o077 != 0 {
		return SocketResponse{}, fmt.Errorf("operator socket %s is not a private socket owned by this user", path)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return SocketResponse{}, fmt.Errorf("operator socket unavailable: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(socketIOTimeout))
	data, err := json.Marshal(req)
	if err != nil {
		return SocketResponse{}, err
	}
	// The server may answer (e.g. peer_rejected) and close before reading the
	// request, so a failed write can race an already-sent reply (EPIPE on
	// Linux). Prefer that reply; report the write error only if none arrived.
	_, writeErr := conn.Write(append(data, '\n'))
	line, err := bufio.NewReader(io.LimitReader(conn, maxSocketRequest)).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		if writeErr != nil {
			return SocketResponse{}, fmt.Errorf("operator socket write: %w", writeErr)
		}
		return SocketResponse{}, fmt.Errorf("operator socket read: %w", err)
	}
	var resp SocketResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return SocketResponse{}, fmt.Errorf("operator socket response: %w", err)
	}
	return resp, nil
}
