package rpc

import (
	"encoding/json"
	"testing"
)

func TestReadyArgs_SourceRepos_RoundTrip(t *testing.T) {
	args := ReadyArgs{
		Assignee:    "alice",
		SourceRepos: []string{"repo-a", "repo-b"},
	}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ReadyArgs
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.SourceRepos) != 2 || got.SourceRepos[0] != "repo-a" || got.SourceRepos[1] != "repo-b" {
		t.Errorf("SourceRepos mismatch: got %v", got.SourceRepos)
	}
	if got.Assignee != "alice" {
		t.Errorf("Assignee mismatch: got %s", got.Assignee)
	}
}

func TestListArgs_SourceRepos_RoundTrip(t *testing.T) {
	args := ListArgs{
		Status:      "open",
		SourceRepos: []string{"repo-x"},
	}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ListArgs
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.SourceRepos) != 1 || got.SourceRepos[0] != "repo-x" {
		t.Errorf("SourceRepos mismatch: got %v", got.SourceRepos)
	}
	if got.Status != "open" {
		t.Errorf("Status mismatch: got %s", got.Status)
	}
}

func TestCountArgs_SourceRepos_RoundTrip(t *testing.T) {
	args := CountArgs{
		Status:      "open",
		SourceRepos: []string{"repo-1", "repo-2", "repo-3"},
	}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got CountArgs
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.SourceRepos) != 3 || got.SourceRepos[0] != "repo-1" || got.SourceRepos[1] != "repo-2" || got.SourceRepos[2] != "repo-3" {
		t.Errorf("SourceRepos mismatch: got %v", got.SourceRepos)
	}
}

func TestSourceRepos_Omitempty(t *testing.T) {
	args := ReadyArgs{Assignee: "bob"}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["source_repos"]; ok {
		t.Error("source_repos should be omitted when empty")
	}
}

func TestSourceRepos_BackwardCompat(t *testing.T) {
	// JSON without source_repos field should unmarshal fine
	tests := []struct {
		name string
		json string
	}{
		{"ReadyArgs", `{"assignee":"alice","limit":10}`},
		{"ListArgs", `{"status":"open","limit":5}`},
		{"CountArgs", `{"status":"closed","group_by":"status"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch tt.name {
			case "ReadyArgs":
				var args ReadyArgs
				if err := json.Unmarshal([]byte(tt.json), &args); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if args.SourceRepos != nil {
					t.Errorf("expected nil SourceRepos, got %v", args.SourceRepos)
				}
			case "ListArgs":
				var args ListArgs
				if err := json.Unmarshal([]byte(tt.json), &args); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if args.SourceRepos != nil {
					t.Errorf("expected nil SourceRepos, got %v", args.SourceRepos)
				}
			case "CountArgs":
				var args CountArgs
				if err := json.Unmarshal([]byte(tt.json), &args); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if args.SourceRepos != nil {
					t.Errorf("expected nil SourceRepos, got %v", args.SourceRepos)
				}
			}
		})
	}
}
