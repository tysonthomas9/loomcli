package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// sendDaemonControlRequest connects to the daemon control socket, sends a
// request, reads the response, and disconnects. One connection per command.
// Convenience wrapper around sendDaemonControlRequestFull.
func sendDaemonControlRequest(socketPath, op, agentName string) (*DaemonControlResponse, error) {
	return sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: op,
		AgentName: agentName,
	})
}

// sendDaemonControlRequestFull connects to the daemon control socket, sends a
// full DaemonControlRequest (including Force and Args fields), reads the
// response, and disconnects. One connection per command.
func sendDaemonControlRequestFull(socketPath string, req DaemonControlRequest) (*DaemonControlResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("daemon is not running (no control socket at %s)", socketPath)
	}
	defer func() { _ = conn.Close() }()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Read response with 30-second timeout (drain+stop can take time)
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("empty response from daemon")
	}

	var resp DaemonControlResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &resp, nil
}
