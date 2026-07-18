package execution

import (
	"strings"
	"testing"
)

func TestRequestedTaskRunIdentitiesAreStableAndEnvelopeBound(t *testing.T) {
	taskRunID := RequestedTaskRunID("run-1", "TASK-1")
	if taskRunID != RequestedTaskRunID("run-1", "TASK-1") {
		t.Fatal("TaskRun identity changed across replay")
	}
	if taskRunID == RequestedTaskRunID("run-2", "TASK-1") || taskRunID == RequestedTaskRunID("run-1", "TASK-2") {
		t.Fatal("TaskRun identity escaped its parent/work-item envelope")
	}
	stepID := RequestedDriverStepID("run-1", taskRunID)
	if stepID != RequestedDriverStepID("run-1", taskRunID) {
		t.Fatal("DriverStep identity changed across replay")
	}
	if stepID == RequestedDriverStepID("run-2", taskRunID) || stepID == RequestedDriverStepID("run-1", "other-task-run") {
		t.Fatal("DriverStep identity escaped its parent/TaskRun envelope")
	}
	if got := RequestTaskRunRequestID("run-1", taskRunID); !strings.HasPrefix(got, "exec-task:sha256:") || len(got) > requestTaskRunRequestIDMaxLength {
		t.Fatalf("request identity = %q", got)
	}
}

func TestRequestTaskRunRequestIDBoundsLongProductionIdentities(t *testing.T) {
	parentRunID := "automation-run-7a7c614654dcac0b48429c21c36bd3d1"
	taskRunID := "promptagent-" + parentRunID + "-LOCALMODE-2"
	legacyReadable := "exec-task:" + parentRunID + ":" + taskRunID
	if got := len(legacyReadable); got != requestTaskRunRequestIDMaxLength+1 {
		t.Fatalf("production regression fixture length = %d, want %d", got, requestTaskRunRequestIDMaxLength+1)
	}

	requestID := RequestTaskRunRequestID(parentRunID, taskRunID)
	if len(requestID) > requestTaskRunRequestIDMaxLength {
		t.Fatalf("request identity length = %d, want <= %d: %q", len(requestID), requestTaskRunRequestIDMaxLength, requestID)
	}
	if !strings.HasPrefix(requestID, "exec-task:sha256:") || requestID == legacyReadable {
		t.Fatalf("long request identity was not deterministically compacted: %q", requestID)
	}
	if replay := RequestTaskRunRequestID(parentRunID, taskRunID); replay != requestID {
		t.Fatalf("request identity changed across replay: first=%q replay=%q", requestID, replay)
	}
	if other := RequestTaskRunRequestID(parentRunID, taskRunID+"-other"); other == requestID {
		t.Fatalf("request identity collision for a different child: %q", other)
	}
	if other := RequestTaskRunRequestID(parentRunID+"-other", taskRunID); other == requestID {
		t.Fatalf("request identity collision for a different parent: %q", other)
	}
}

func TestRequestTaskRunRequestIDIsUnambiguousAtTheFleetBoundary(t *testing.T) {
	parentRunID := strings.Repeat("p", 50)
	taskRunID := strings.Repeat("t", 67)
	legacyReadable := "exec-task:" + parentRunID + ":" + taskRunID
	if len(legacyReadable) != requestTaskRunRequestIDMaxLength {
		t.Fatalf("boundary fixture length = %d, want %d", len(legacyReadable), requestTaskRunRequestIDMaxLength)
	}
	if got := RequestTaskRunRequestID(parentRunID, taskRunID); !strings.HasPrefix(got, "exec-task:sha256:") || len(got) > requestTaskRunRequestIDMaxLength {
		t.Fatalf("boundary request identity = %q", got)
	}

	overflow := RequestTaskRunRequestID(parentRunID, taskRunID+"t")
	if !strings.HasPrefix(overflow, "exec-task:sha256:") || len(overflow) > requestTaskRunRequestIDMaxLength {
		t.Fatalf("overflow request identity = %q (%d bytes)", overflow, len(overflow))
	}
	if left, right := RequestTaskRunRequestID("a:b", "c"), RequestTaskRunRequestID("a", "b:c"); left == right {
		t.Fatalf("delimiter collision: %q", left)
	}
}
