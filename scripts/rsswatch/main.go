package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const rssSampleInterval = 250 * time.Millisecond

type processSample struct {
	parent int
	rssKB  int64
}

type watchOptions struct {
	limitMiB       int64
	timeout        time.Duration
	command        string
	commandArgs    []string
	sampleInterval time.Duration
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	options, err := parseWatchOptions(args)
	if err != nil {
		writef(stderr, "%v\n", err)
		return 2
	}

	// rsswatch is intentionally a generic argv runner. It never invokes a shell,
	// and its caller supplies both the executable and arguments.
	command := exec.Command(options.command, options.commandArgs...) //nolint:gosec
	command.Stdout = stdout
	command.Stderr = stderr
	command.Stdin = os.Stdin
	if err := configureProcessGroup(command); err != nil {
		writef(stderr, "rsswatch configure: %v\n", err)
		return 2
	}
	if err := command.Start(); err != nil {
		writef(stderr, "rsswatch start: %v\n", err)
		return 2
	}
	return watchCommand(command, options, stderr)
}

func watchCommand(command *exec.Cmd, options watchOptions, stderr io.Writer) int {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(options.sampleInterval)
	defer ticker.Stop()
	timer := time.NewTimer(options.timeout)
	defer timer.Stop()

	limitKB := options.limitMiB * 1024
	var peakKB int64
	for {
		select {
		case waitErr := <-done:
			writef(stderr, "rsswatch peak_tree_rss_mib=%.1f limit_mib=%d\n", float64(peakKB)/1024, options.limitMiB)
			if waitErr == nil {
				return 0
			}
			var exitError *exec.ExitError
			if errors.As(waitErr, &exitError) {
				return exitError.ExitCode()
			}
			writef(stderr, "rsswatch wait: %v\n", waitErr)
			return 2
		case <-ticker.C:
			processes, sampleErr := readProcessTable()
			if sampleErr != nil {
				writef(stderr, "rsswatch sample: %v\n", sampleErr)
				_ = killProcessGroup(command.Process.Pid)
				<-done
				return 2
			}
			rssKB := treeRSS(command.Process.Pid, processes)
			if rssKB > peakKB {
				peakKB = rssKB
			}
			if rssKB > limitKB {
				writef(stderr, "rsswatch abort tree_rss_mib=%.1f limit_mib=%d\n", float64(rssKB)/1024, options.limitMiB)
				_ = killProcessGroup(command.Process.Pid)
				<-done
				return 99
			}
		case <-timer.C:
			writef(stderr, "rsswatch timeout after %s peak_tree_rss_mib=%.1f\n", options.timeout, float64(peakKB)/1024)
			_ = killProcessGroup(command.Process.Pid)
			<-done
			return 124
		}
	}
}

// writef keeps best-effort diagnostics from obscuring the command's exit code.
func writef(writer io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(writer, format, args...)
}

func parseWatchOptions(args []string) (watchOptions, error) {
	if len(args) < 3 {
		return watchOptions{}, errors.New("usage: rsswatch <limit-mib> <timeout-seconds> <command> [args...]")
	}
	limitMiB, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || limitMiB <= 0 {
		return watchOptions{}, fmt.Errorf("invalid RSS limit %q", args[0])
	}
	timeoutSeconds, err := strconv.Atoi(args[1])
	if err != nil || timeoutSeconds <= 0 {
		return watchOptions{}, fmt.Errorf("invalid timeout %q", args[1])
	}
	return watchOptions{
		limitMiB:       limitMiB,
		timeout:        time.Duration(timeoutSeconds) * time.Second,
		command:        args[2],
		commandArgs:    append([]string(nil), args[3:]...),
		sampleInterval: rssSampleInterval,
	}, nil
}

func parseProcessTable(output []byte) (map[int]processSample, error) {
	processes := map[int]processSample{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		rssKB, rssErr := strconv.ParseInt(fields[2], 10, 64)
		if pidErr == nil && parentErr == nil && rssErr == nil && pid > 0 && parent >= 0 && rssKB >= 0 {
			processes[pid] = processSample{parent: parent, rssKB: rssKB}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return processes, nil
}

func treeRSS(root int, processes map[int]processSample) int64 {
	descendants := map[int]bool{root: true}
	for changed := true; changed; {
		changed = false
		for pid, process := range processes {
			if !descendants[pid] && descendants[process.parent] {
				descendants[pid] = true
				changed = true
			}
		}
	}
	var totalKB int64
	for pid := range descendants {
		// RSS can include shared pages in more than one process. Summing it is a
		// deliberately conservative ceiling for the complete command tree.
		totalKB += processes[pid].rssKB
	}
	return totalKB
}
