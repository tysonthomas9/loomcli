package fleet

import (
	"encoding/json"
	"os"
	"testing"
)

// The browser preparer consumes this exact corpus too. These are native wire
// conformance fixtures, not a claim of actual storage or browser execution.
func TestIssueRecoverySharedCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/issue_recovery_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string `json:"name"`
		Valid    bool   `json:"valid"`
		Document string `json:"document"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := validateRecoveryDocument([]byte(tc.Document), "WS")
			if tc.Valid {
				if err != nil {
					t.Fatal(err)
				}
				if string(got.Document) != tc.Document {
					t.Fatal("native document changed")
				}
			} else if err == nil || len(got.Document) != 0 {
				t.Fatal("invalid native document accepted")
			}
		})
	}
}
