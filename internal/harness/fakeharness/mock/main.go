// Mock CLI harness for harness-wrapper tests. It behaves like an
// interactive agent CLI: prints a banner, performs a configurable
// behavior selected by --mode, and exits with a predictable code.
//
// Modes:
//
//	completed     prints progress lines and DONE, exits 0
//	failed        prints an error to stderr, exits with --exit-code
//	stuck         prints one line then blocks forever (until SIGTERM)
//	needs-input   prints a prompt, reads a line from stdin, exits 0 if it matches --expected-input
//	cost-limited  prints a quota-exhausted message, exits with --exit-code
//	api-error     prints --api-error-msg (optionally --api-error-repeat times) then either heartbeats until signal or, if --api-error-recover, continues to completed-style progress and exits 0
//
// This binary has no external dependencies on a particular consumer.
// It's a standalone fake harness invoked as a subprocess by tests
// under pkg/wrapper.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	mode := flag.String("mode", "completed", "completed|failed|stuck|needs-input|cost-limited|api-error")
	delay := flag.Duration("delay", 50*time.Millisecond, "delay between progress lines")
	exitCode := flag.Int("exit-code", 1, "exit code for failed and cost-limited modes")
	steps := flag.Int("steps", 3, "progress steps for completed mode")
	prompt := flag.String("prompt", "Continue? [y/N] ", "prompt text for needs-input mode")
	expected := flag.String("expected-input", "y", "accepted input for needs-input mode")
	apiErrorMsg := flag.String("api-error-msg", "API Error: 529 Overloaded.", "message to print for api-error mode")
	apiErrorRepeat := flag.Int("api-error-repeat", 1, "print the api-error message this many times")
	apiErrorRepeatGap := flag.Duration("api-error-repeat-gap", 100*time.Millisecond, "delay between repeated api-error prints")
	apiErrorRecover := flag.Bool("api-error-recover", false, "after printing, resume normal completed-style progress and exit 0 (else heartbeat until signal)")
	apiErrorHeartbeat := flag.Duration("api-error-heartbeat", 200*time.Millisecond, "heartbeat interval for api-error mode")
	flag.Parse()

	installSignalCleanup()

	fmt.Println("Mock Agent CLI")

	switch *mode {
	case "completed":
		runCompleted(*steps, *delay)
	case "failed":
		fmt.Fprintln(os.Stderr, "Fatal: workspace is not writable.")
		os.Exit(*exitCode)
	case "stuck":
		fmt.Println("Thinking...")
		select {}
	case "needs-input":
		runNeedsInput(*prompt, *expected)
	case "cost-limited":
		fmt.Fprintln(os.Stderr, "ERROR: quota exceeded. Please try again after your usage limit resets.")
		os.Exit(*exitCode)
	case "api-error":
		runAPIError(*apiErrorMsg, *apiErrorRepeat, *apiErrorRepeatGap, *apiErrorRecover, *apiErrorHeartbeat, *steps, *delay)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
}

func runCompleted(steps int, delay time.Duration) {
	for i := 1; i <= steps; i++ {
		fmt.Printf("Step %d/%d\n", i, steps)
		time.Sleep(delay)
	}
	fmt.Println("DONE")
}

func runNeedsInput(prompt, expected string) {
	fmt.Println("Need approval to continue.")
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if line == expected {
		fmt.Println("Approved. DONE")
		return
	}
	fmt.Fprintln(os.Stderr, "Rejected.")
	os.Exit(2)
}

func runAPIError(msg string, repeat int, repeatGap time.Duration, recover bool, heartbeat time.Duration, steps int, delay time.Duration) {
	if repeat < 1 {
		repeat = 1
	}
	for i := 0; i < repeat; i++ {
		if i > 0 {
			time.Sleep(repeatGap)
		}
		fmt.Println(msg)
	}
	if recover {
		// Brief pause, then resume normal output so callers can verify
		// the wrapper's StatusAPIError did not contaminate the
		// terminal Result when output continues.
		time.Sleep(500 * time.Millisecond)
		runCompleted(steps, delay)
		return
	}
	// Heartbeat: keep PTY active without producing recognizable
	// content. A bare dot per tick is enough to refresh lastOutput.
	for {
		time.Sleep(heartbeat)
		fmt.Print(".")
	}
}

func installSignalCleanup() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-ch
		fmt.Println("Mock interrupted.")
		os.Exit(130)
	}()
}
