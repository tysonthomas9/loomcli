package automode

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
)

const (
	// workScanMaxAttempts includes the initial ready/work read. A scan failure
	// gets two in-process retries before the process exits with its marker.
	workScanMaxAttempts  = 3
	workScanRetryInitial = time.Second
)

var workScanRetryAfterRe = regexp.MustCompile(`(?i)retry.?after[:\s]+(\d+)`)

// nextWorkScanRetry decides whether an unavailable ready/work scan should be
// retried in-process. A non-retryable error is surfaced immediately; retryable
// errors are bounded so a daemon sees a categorical failure rather than a
// process that silently looks idle forever.
func nextWorkScanRetry(failures *int, err error) (retry bool, delay time.Duration) {
	if !isRetryableWorkScanError(err) {
		return false, 0
	}
	*failures++
	if *failures >= workScanMaxAttempts {
		return false, 0
	}
	return true, workScanRetryDelay(err, *failures)
}

func isRetryableWorkScanError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var backendErr *backend.BackendError
	if !errors.As(err, &backendErr) {
		return false
	}
	switch backendErr.Kind {
	case backend.KindTimeout, backend.KindInternal:
		return true
	case backend.KindUnavailable:
		message := strings.ToLower(backendErr.Error())
		return !strings.Contains(message, "authentication") &&
			!strings.Contains(message, "unauthorized") &&
			!strings.Contains(message, "forbidden")
	default:
		return false
	}
}

func workScanRetryDelay(err error, failures int) time.Duration {
	if hint := workScanRetryAfter(err); hint > 0 {
		return hint
	}
	return workScanRetryInitial << (failures - 1)
}

func workScanRetryAfter(err error) time.Duration {
	match := workScanRetryAfterRe.FindStringSubmatch(err.Error())
	if len(match) < 2 {
		return 0
	}
	seconds, parseErr := strconv.Atoi(match[1])
	if parseErr != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func workScanFailureReason(err error) string {
	cause := "unknown ready/work scan error"
	if err != nil {
		cause = strings.Join(strings.Fields(err.Error()), " ")
	}
	return agenterr.WorkScanFailureMarker + ": " + cause
}
