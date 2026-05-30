package tsfirst

import (
	"fmt"
	"io"
	"strings"
)

type trackingWriter struct {
	w     io.Writer
	count int
	last  byte
}

func (tw *trackingWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		tw.count += len(p)
		tw.last = p[len(p)-1]
	}
	return tw.w.Write(p)
}

func ensureStreamLineBreak(tw *trackingWriter) error {
	if tw == nil {
		return nil
	}
	if tw.count > 0 && tw.last == '\n' {
		return nil
	}
	_, err := fmt.Fprintln(tw.w)
	return err
}

func printInteractiveConnectHeader(out io.Writer, result connectResult) error {
	if _, err := fmt.Fprintf(out, "Connected to %s instance %s session %s (backend=%s model=%s)\n", result.Agent, result.Instance, result.Session, fallback(result.Backend, "default"), fallback(result.Model, "default")); err != nil {
		return err
	}
	if len(result.Env) > 0 {
		if _, err := fmt.Fprintf(out, "Env allowlist: %s\n", strings.Join(result.Env, ", ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "Enter one prompt per line. Ctrl-D, /exit, or /quit ends the session.")
	return err
}
