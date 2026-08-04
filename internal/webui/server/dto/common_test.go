package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestErrorResponse_JSON(t *testing.T) {
	resp := NewErrorResponse("not found", "not_found")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["success"] != false {
		t.Errorf("success = %v, want false", m["success"])
	}
	if m["error"] != "not found" {
		t.Errorf("error = %v, want %q", m["error"], "not found")
	}
	if m["code"] != "not_found" {
		t.Errorf("code = %v, want %q", m["code"], "not_found")
	}
}

func TestErrorResponse_NoCode(t *testing.T) {
	resp := NewErrorResponse("fail", "")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["code"]; ok {
		t.Errorf("code field should be omitted when empty, got %v", m["code"])
	}
}

func TestErrorResponse_WithDetails(t *testing.T) {
	resp := ErrorResponse{
		Success: false,
		Error:   "validation failed",
		Code:    "validation_error",
		Details: map[string]any{"title": "required"},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	details, ok := m["details"].(map[string]any)
	if !ok {
		t.Fatalf("details missing or wrong type")
	}
	if details["title"] != "required" {
		t.Errorf("details.title = %v, want %q", details["title"], "required")
	}
}

func TestErrorResponse_NilDetails(t *testing.T) {
	resp := NewErrorResponse("fail", "error")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["details"]; ok {
		t.Errorf("details field should be omitted when nil")
	}
}

func TestErrorResponse_Constructor_SuccessFalse(t *testing.T) {
	resp := NewErrorResponse("x", "y")
	if resp.Success != false {
		t.Errorf("Success = %v, want false", resp.Success)
	}
}

func TestListResponse_JSON(t *testing.T) {
	resp := NewListResponse([]string{"a", "b"}, 2)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["success"] != true {
		t.Errorf("success = %v, want true", m["success"])
	}
	items, ok := m["data"].([]any)
	if !ok {
		t.Fatalf("data missing or wrong type")
	}
	if len(items) != 2 {
		t.Errorf("len(data) = %d, want 2", len(items))
	}
	if items[0] != "a" || items[1] != "b" {
		t.Errorf("data = %v, want [a, b]", items)
	}
	if m["total"] != float64(2) {
		t.Errorf("total = %v, want 2", m["total"])
	}
}

func TestListResponse_EmptySlice(t *testing.T) {
	resp := NewListResponse([]string{}, 0)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"data":[]`) {
		t.Errorf("expected data:[], got %s", raw)
	}
	if !strings.Contains(raw, `"total":0`) {
		t.Errorf("expected total:0, got %s", raw)
	}
}

func TestListResponse_NilItems(t *testing.T) {
	resp := NewListResponse[string](nil, 0)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"data":[]`) {
		t.Errorf("expected data:[] (not null), got %s", raw)
	}
}

func TestListResponse_PaginationTotal(t *testing.T) {
	resp := NewListResponse([]string{"a", "b"}, 100)

	var m map[string]any
	data, _ := json.Marshal(resp)
	json.Unmarshal(data, &m)

	items := m["data"].([]any)
	if len(items) != 2 {
		t.Errorf("len(data) = %d, want 2", len(items))
	}
	if m["total"] != float64(100) {
		t.Errorf("total = %v, want 100", m["total"])
	}
}

func TestListResponse_IntType(t *testing.T) {
	resp := NewListResponse([]int{1, 2, 3}, 3)
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	items := m["data"].([]any)
	if len(items) != 3 {
		t.Errorf("len(data) = %d, want 3", len(items))
	}
	if items[0] != float64(1) {
		t.Errorf("data[0] = %v, want 1", items[0])
	}
}

func TestListResponse_Constructor_SuccessTrue(t *testing.T) {
	resp := NewListResponse[string](nil, 0)
	if resp.Success != true {
		t.Errorf("Success = %v, want true", resp.Success)
	}
}

func TestMessageResponse_JSON(t *testing.T) {
	resp := NewMessageResponse("issue closed")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["success"] != true {
		t.Errorf("success = %v, want true", m["success"])
	}
	if m["message"] != "issue closed" {
		t.Errorf("message = %v, want %q", m["message"], "issue closed")
	}
}

func TestMessageResponse_EmptyString(t *testing.T) {
	resp := NewMessageResponse("")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(data)
	if !strings.Contains(raw, `"message":""`) {
		t.Errorf("expected message:\"\" (not omitted), got %s", raw)
	}
}

func TestMessageResponse_Constructor_SuccessTrue(t *testing.T) {
	resp := NewMessageResponse("x")
	if resp.Success != true {
		t.Errorf("Success = %v, want true", resp.Success)
	}
}
