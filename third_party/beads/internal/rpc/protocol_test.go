package rpc

import (
	"encoding/json"
	"testing"
)

func TestRequestSerialization(t *testing.T) {
	createArgs := CreateArgs{
		Title:       "Test Issue",
		Description: "Test description",
		IssueType:   "task",
		Priority:    2,
	}

	argsJSON, err := json.Marshal(createArgs)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	req := Request{
		Operation: OpCreate,
		Args:      argsJSON,
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	var decodedReq Request
	if err := json.Unmarshal(reqJSON, &decodedReq); err != nil {
		t.Fatalf("Failed to unmarshal request: %v", err)
	}

	if decodedReq.Operation != OpCreate {
		t.Errorf("Expected operation %s, got %s", OpCreate, decodedReq.Operation)
	}

	var decodedArgs CreateArgs
	if err := json.Unmarshal(decodedReq.Args, &decodedArgs); err != nil {
		t.Fatalf("Failed to unmarshal args: %v", err)
	}

	if decodedArgs.Title != createArgs.Title {
		t.Errorf("Expected title %s, got %s", createArgs.Title, decodedArgs.Title)
	}
	if decodedArgs.Priority != createArgs.Priority {
		t.Errorf("Expected priority %d, got %d", createArgs.Priority, decodedArgs.Priority)
	}
}

func TestResponseSerialization(t *testing.T) {
	resp := Response{
		Success: true,
		Data:    json.RawMessage(`{"id":"bd-1","title":"Test"}`),
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	var decodedResp Response
	if err := json.Unmarshal(respJSON, &decodedResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if decodedResp.Success != resp.Success {
		t.Errorf("Expected success %v, got %v", resp.Success, decodedResp.Success)
	}

	if string(decodedResp.Data) != string(resp.Data) {
		t.Errorf("Expected data %s, got %s", string(resp.Data), string(decodedResp.Data))
	}
}

func TestErrorResponse(t *testing.T) {
	resp := Response{
		Success: false,
		Error:   "something went wrong",
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	var decodedResp Response
	if err := json.Unmarshal(respJSON, &decodedResp); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if decodedResp.Success {
		t.Errorf("Expected success false, got true")
	}

	if decodedResp.Error != resp.Error {
		t.Errorf("Expected error %s, got %s", resp.Error, decodedResp.Error)
	}
}

func TestAllOperations(t *testing.T) {
	operations := []string{
		OpPing,
		OpCreate,
		OpUpdate,
		OpClose,
		OpList,
		OpShow,
		OpReady,
		OpStats,
		OpDepAdd,
		OpDepRemove,
		OpDepTree,
		OpLabelAdd,
		OpLabelRemove,
		OpCommentList,
		OpCommentAdd,
	}

	for _, op := range operations {
		req := Request{
			Operation: op,
			Args:      json.RawMessage(`{}`),
		}

		reqJSON, err := json.Marshal(req)
		if err != nil {
			t.Errorf("Failed to marshal request for op %s: %v", op, err)
			continue
		}

		var decodedReq Request
		if err := json.Unmarshal(reqJSON, &decodedReq); err != nil {
			t.Errorf("Failed to unmarshal request for op %s: %v", op, err)
			continue
		}

		if decodedReq.Operation != op {
			t.Errorf("Expected operation %s, got %s", op, decodedReq.Operation)
		}
	}
}

func TestUpdateArgsWithNilValues(t *testing.T) {
	title := "New Title"
	args := UpdateArgs{
		ID:    "bd-1",
		Title: &title,
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	var decodedArgs UpdateArgs
	if err := json.Unmarshal(argsJSON, &decodedArgs); err != nil {
		t.Fatalf("Failed to unmarshal args: %v", err)
	}

	if decodedArgs.Title == nil {
		t.Errorf("Expected title to be non-nil")
	} else if *decodedArgs.Title != title {
		t.Errorf("Expected title %s, got %s", title, *decodedArgs.Title)
	}

	if decodedArgs.Status != nil {
		t.Errorf("Expected status to be nil, got %v", *decodedArgs.Status)
	}
}

func TestCreateArgsSourceRepoSerialization(t *testing.T) {
	args := CreateArgs{
		Title:      "Test Issue",
		IssueType:  "task",
		Priority:   2,
		SourceRepo: "github.com/org/repo",
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal CreateArgs: %v", err)
	}

	var decoded CreateArgs
	if err := json.Unmarshal(argsJSON, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal CreateArgs: %v", err)
	}

	if decoded.SourceRepo != "github.com/org/repo" {
		t.Errorf("Expected SourceRepo 'github.com/org/repo', got %q", decoded.SourceRepo)
	}
	if decoded.Title != "Test Issue" {
		t.Errorf("Expected Title 'Test Issue', got %q", decoded.Title)
	}
}

func TestCreateArgsSourceRepoOmittedWhenEmpty(t *testing.T) {
	args := CreateArgs{
		Title:     "Test Issue",
		IssueType: "task",
		Priority:  2,
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal CreateArgs: %v", err)
	}

	// source_repo should be omitted from JSON when empty
	var raw map[string]interface{}
	if err := json.Unmarshal(argsJSON, &raw); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}
	if _, exists := raw["source_repo"]; exists {
		t.Errorf("Expected source_repo to be omitted from JSON when empty")
	}
}

func TestMutationEventSourceRepoSerialization(t *testing.T) {
	event := MutationEvent{
		Type:       MutationCreate,
		IssueID:    "bd-42",
		Title:      "Test Issue",
		SourceRepo: "github.com/org/repo",
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal MutationEvent: %v", err)
	}

	var decoded MutationEvent
	if err := json.Unmarshal(eventJSON, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal MutationEvent: %v", err)
	}

	if decoded.SourceRepo != "github.com/org/repo" {
		t.Errorf("Expected SourceRepo 'github.com/org/repo', got %q", decoded.SourceRepo)
	}
	if decoded.Type != MutationCreate {
		t.Errorf("Expected Type %q, got %q", MutationCreate, decoded.Type)
	}
	if decoded.IssueID != "bd-42" {
		t.Errorf("Expected IssueID 'bd-42', got %q", decoded.IssueID)
	}
}

func TestMutationEventSourceRepoOmittedWhenEmpty(t *testing.T) {
	event := MutationEvent{
		Type:    MutationCreate,
		IssueID: "bd-42",
		Title:   "Test Issue",
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal MutationEvent: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(eventJSON, &raw); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}
	if _, exists := raw["source_repo"]; exists {
		t.Errorf("Expected source_repo to be omitted from JSON when empty")
	}
}
