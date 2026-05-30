package tsfirst

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

var localConnectStdoutMu sync.Mutex

func invokeNonStreamingLocalAgent(backend cli.Backend, backendName, workDir, prompt, agentName string, stream io.Writer) (localInvocationResult, error) {
	collector := usage.NewCollector(backendName, agentName)
	shutdown := make(chan struct{})
	output, err := captureLocalBackendStdout(stream, func() error {
		return backend.InvokeNonInteractive(workDir, prompt, agentName, shutdown, collector)
	})
	if err != nil {
		return localInvocationResult{}, err
	}
	response := strings.TrimSpace(output)
	if response == "" {
		response = "backend completed; no stdout response was captured"
	}
	result := localInvocationResult{
		Response: response,
		Usage:    connectUsageFromCollector(collector),
	}
	if stream != nil && strings.TrimSpace(output) == "" {
		if _, err := io.WriteString(stream, response); err != nil {
			return localInvocationResult{}, err
		}
	}
	return result, nil
}

func captureLocalBackendStdout(stream io.Writer, fn func() error) (string, error) {
	localConnectStdoutMu.Lock()
	defer localConnectStdoutMu.Unlock()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	var captured bytes.Buffer
	target := io.Writer(&captured)
	if stream != nil {
		target = io.MultiWriter(stream, &captured)
	}
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(target, reader)
		copyDone <- copyErr
	}()
	var runErr error
	os.Stdout = writer
	func() {
		defer func() {
			os.Stdout = oldStdout
			_ = writer.Close()
		}()
		runErr = fn()
	}()
	copyErr := <-copyDone
	_ = reader.Close()
	if runErr != nil {
		return "", runErr
	}
	if copyErr != nil {
		return "", copyErr
	}
	return captured.String(), nil
}

func connectUsageFromCollector(collector *usage.Collector) *connectUsage {
	if collector == nil {
		return nil
	}
	input, output, cacheRead, cacheWrite := collector.Totals()
	if input == 0 && output == 0 && cacheRead == 0 && cacheWrite == 0 {
		return nil
	}
	return &connectUsage{
		InputTokens:              input,
		OutputTokens:             output,
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: cacheWrite,
		TotalTokens:              input + output + cacheRead + cacheWrite,
	}
}
