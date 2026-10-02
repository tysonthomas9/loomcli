// Command loom-harness-emu is Loom's test-support OpenCode emulator
// (internal/harnessemu). It answers `--version` and `serve --service` the
// way the opencode adapter starts OpenCode: it listens on a loopback port
// and registers in <XDG_STATE_HOME or HOME/.local/state>/opencode/
// service.json. Its state is emu-state.json beside that file, and
// LOOM_HARNESS_EMU_SCENARIOS names the test-owned scenario file.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/harnessemu"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(harnessemu.Version)
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: loom-harness-emu --version | serve --service")
		os.Exit(2)
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "loom-harness-emu:", err)
		os.Exit(1)
	}
}

func serve() error {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	dir = filepath.Join(dir, "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pw := make([]byte, 16)
	_, _ = rand.Read(pw)
	s, err := harnessemu.New(filepath.Join(dir, "emu-state.json"), os.Getenv("LOOM_HARNESS_EMU_SCENARIOS"), hex.EncodeToString(pw))
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	reg, _ := json.Marshal(map[string]any{"id": "loom-harness-emu", "version": harnessemu.Version,
		"url": "http://" + ln.Addr().String(), "pid": os.Getpid(), "password": s.Password})
	if err := os.WriteFile(filepath.Join(dir, "service.json"), reg, 0o600); err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sig; s.Close(); _ = ln.Close() }()
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
