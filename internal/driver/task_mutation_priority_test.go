package driver

import (
	"encoding/json"
	"testing"
)

func TestClaimedTaskSerializesCommittedPriorityZero(t *testing.T) {
	payload, err := json.Marshal(ClaimedTask{ID: "TASK-P0", Priority: 0})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	priority, ok := decoded["priority"]
	if !ok {
		t.Fatalf("claim response omitted committed P0: %s", payload)
	}
	if string(priority) != "0" {
		t.Fatalf("claim response priority = %s, want 0", priority)
	}
}
