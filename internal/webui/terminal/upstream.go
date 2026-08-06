package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// PTYUpstream is the transport a session hub reads from and writes to. The host
// implementation wraps a local pty file + child process; a future implementation will
// wrap a remote provider PTY.
type PTYUpstream interface {
	// Output returns the byte stream. Exactly one consumer (the hub pump). The channel
	// closing signals that the upstream ended; the owner decides what that means.
	Output() <-chan []byte
	Write(p []byte) (int, error)
	Resize(ctx context.Context, cols, rows uint16) error
	// Close releases the transport. For the host this kills the child and closes the fd.
	Close() error
}

type hostUpstream struct {
	file *os.File
	cmd  *exec.Cmd

	outputOnce sync.Once
	output     chan []byte
	stop       chan struct{}
	readerWG   sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
}

func newHostUpstream(file *os.File, cmd *exec.Cmd) *hostUpstream {
	return &hostUpstream{file: file, cmd: cmd, stop: make(chan struct{})}
}

func (u *hostUpstream) Output() <-chan []byte {
	u.startReader()
	return u.output
}

func (u *hostUpstream) startReader() {
	u.outputOnce.Do(func() {
		u.output = make(chan []byte)
		u.readerWG.Add(1)
		go func() {
			defer u.readerWG.Done()
			u.readOutput()
		}()
	})
}

func (u *hostUpstream) readOutput() {
	defer close(u.output)
	buf := make([]byte, realtime.TerminalReadBufSize)
	for {
		select {
		case <-u.stop:
			return
		default:
		}
		if u.file == nil {
			return
		}
		n, err := u.file.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case u.output <- chunk:
			case <-u.stop:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (u *hostUpstream) Write(p []byte) (int, error) {
	// Multiple attachments share one PTY fd through this upstream. POSIX
	// guarantees concurrent write(2) calls up to PIPE_BUF (4096 bytes) are
	// atomic, and terminal keystrokes are far below that, so no mutex is
	// needed on this path.
	return u.file.Write(p)
}

func (u *hostUpstream) Resize(_ context.Context, cols, rows uint16) error {
	return pty.Setsize(u.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (u *hostUpstream) Close() error {
	u.closeOnce.Do(func() {
		close(u.stop)
		if u.file != nil {
			if err := u.file.Close(); err != nil {
				u.closeErr = err
			}
		}
		if u.cmd != nil && u.cmd.Process != nil {
			_ = u.cmd.Process.Kill()
			_ = u.cmd.Wait()
		}
		u.startReader()
		u.readerWG.Wait()
	})
	return u.closeErr
}

var _ PTYUpstream = (*hostUpstream)(nil)
