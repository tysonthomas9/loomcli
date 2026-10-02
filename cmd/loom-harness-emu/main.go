// Command loom-harness-emu is Loom's test-support OpenCode emulator
// (internal/harnessemu). It answers `--version` and `serve --service` the
// way the opencode adapter starts OpenCode: it listens on a loopback port
// and registers in $XDG_STATE_HOME/opencode/service.json. Its state is
// emu-state.json beside that file, LOOM_HARNESS_EMU_SCENARIOS names the
// test-owned scenario file and LOOM_HARNESS_EMU_MODEL the fake model.
//
// It runs only with LOOM_HARNESS_EMU=1 and a test-owned XDG_STATE_HOME, so
// a product run that reaches it fails closed and never replaces the user's
// own OpenCode registration.
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
	if os.Getenv("LOOM_HARNESS_EMU") != "1" || os.Getenv("XDG_STATE_HOME") == "" {
		fmt.Fprintln(os.Stderr, "loom-harness-emu: test only; needs LOOM_HARNESS_EMU=1 and a test-owned XDG_STATE_HOME")
		os.Exit(2)
	}
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
	dir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pw := make([]byte, 16)
	_, _ = rand.Read(pw)
	s, err := harnessemu.New(filepath.Join(dir, "emu-state.json"), os.Getenv("LOOM_HARNESS_EMU_SCENARIOS"), hex.EncodeToString(pw))
	if err != nil {
		return err
	}
	s.Model = os.Getenv("LOOM_HARNESS_EMU_MODEL")
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
