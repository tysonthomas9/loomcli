package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// findWebUIBinary resolves the beads-web-ui binary location.
// Package-level var for test injection.
var findWebUIBinary = defaultFindWebUIBinary

func defaultFindWebUIBinary() (string, error) {
	if p := os.Getenv("BEADS_WEBUI_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("BEADS_WEBUI_BIN set but not found: %w", err)
		}
		return p, nil
	}
	p, err := exec.LookPath("beads-web-ui")
	if err != nil {
		return "", fmt.Errorf("beads-web-ui binary not found: set $BEADS_WEBUI_BIN or add to $PATH")
	}
	return p, nil
}

// WebUIProcess manages the beads-web-ui subprocess.
type WebUIProcess struct {
	port       int
	corsOrigin string
	binaryPath string

	mu       sync.Mutex
	cmd      *exec.Cmd
	waitCh   chan error  // receives cmd.Wait() result; one per process instance
	shutdown chan struct{}
	done     chan struct{} // closed when monitor goroutine exits
	once     sync.Once    // ensures shutdown channel closed only once
}

// NewWebUIProcess creates a WebUIProcess with the given port and CORS config.
// Returns error if the binary cannot be found.
func NewWebUIProcess(port int, corsOrigin string) (*WebUIProcess, error) {
	binPath, err := findWebUIBinary()
	if err != nil {
		return nil, err
	}
	return &WebUIProcess{
		port:       port,
		corsOrigin: corsOrigin,
		binaryPath: binPath,
		shutdown:   make(chan struct{}),
		done:       make(chan struct{}),
	}, nil
}

// Start launches the subprocess and starts the monitor goroutine.
func (w *WebUIProcess) Start() error {
	if err := w.startProcess(); err != nil {
		close(w.done)
		return fmt.Errorf("failed to start beads-web-ui: %w", err)
	}
	go w.monitor()
	return nil
}

// startProcess creates and starts the beads-web-ui command.
// It also spawns a goroutine to call cmd.Wait() exactly once and deliver the
// result on w.waitCh, avoiding the double-Wait bug.
func (w *WebUIProcess) startProcess() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	args := []string{
		"-port", fmt.Sprintf("%d", w.port),
	}
	if w.corsOrigin != "" {
		args = append(args, "-cors", w.corsOrigin)
	}

	cmd := exec.Command(w.binaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Use process group so we can kill child processes on shutdown
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	w.cmd = cmd
	w.waitCh = make(chan error, 1)
	go func(ch chan<- error) {
		ch <- cmd.Wait()
	}(w.waitCh)

	return nil
}

// monitor watches the subprocess and restarts it on crash with exponential backoff.
// It is the sole consumer of waitCh, ensuring cmd.Wait() is never called twice.
func (w *WebUIProcess) monitor() {
	defer close(w.done)

	backoff := 1 * time.Second
	const maxBackoff = 30 * time.Second
	const stableThreshold = 10 * time.Second

	for {
		startTime := time.Now()

		// Wait for process to exit (via the dedicated wait goroutine)
		w.mu.Lock()
		ch := w.waitCh
		w.mu.Unlock()

		var waitErr error
		select {
		case waitErr = <-ch:
			// process exited
		case <-w.shutdown:
			return
		}

		// Check if we were told to shut down
		select {
		case <-w.shutdown:
			return
		default:
		}

		uptime := time.Since(startTime)
		if uptime >= stableThreshold {
			backoff = 1 * time.Second // reset on stable run
		}

		log.Printf("[webui] Process exited (err=%v, uptime=%s), restarting in %s...", waitErr, uptime.Round(time.Millisecond), backoff)

		// Sleep before restart attempt
		if interruptibleSleep(backoff, w.shutdown) {
			return
		}

		// Increase backoff for next potential crash
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		// Attempt restart, retrying with backoff on failure
		for {
			select {
			case <-w.shutdown:
				return
			default:
			}
			if err := w.startProcess(); err != nil {
				log.Printf("[webui] Failed to restart beads-web-ui: %v", err)
				if interruptibleSleep(backoff, w.shutdown) {
					return
				}
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			break
		}
	}
}

// Stop performs graceful shutdown: SIGTERM, wait 2s, then SIGKILL.
// The monitor goroutine is the sole caller of cmd.Wait(); Stop only signals
// the process and waits for the monitor to finish.
func (w *WebUIProcess) Stop() {
	w.once.Do(func() {
		close(w.shutdown)
	})

	w.mu.Lock()
	cmd := w.cmd
	w.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		// Send SIGTERM to the process group
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

		// Wait up to 2 seconds for monitor to observe the exit
		select {
		case <-w.done:
			return
		case <-time.After(2 * time.Second):
			log.Printf("[webui] Process did not exit after SIGTERM, sending SIGKILL")
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}

	// Wait for monitor goroutine to finish
	<-w.done
}
