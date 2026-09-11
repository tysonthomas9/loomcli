package backend

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIssueRecoverySelectionDocumentEcho(t *testing.T) {
	for _, tc := range []struct {
		name, document, selected, metadata string
		valid                              bool
	}{
		{"selected", `{"history":{"issue_id":"A"}}`, "A", "A", true},
		{"unselected", `{"history":null}`, "", "", true},
		{"forged echo", `{"history":{"issue_id":"B"}}`, "A", "A", false},
		{"missing echo", `{"history":null}`, "A", "A", false},
		{"unexpected selection", `{"history":{"issue_id":"A"}}`, "", "", false},
		{"duplicate history", `{"history":{"issue_id":"B"},"history":{"issue_id":"A"}}`, "A", "A", false},
		{"duplicate echo", `{"history":{"issue_id":"B","issue_id":"A"}}`, "A", "A", false},
		{"missing metadata", `{"history":{"issue_id":"A"}}`, "A", "", false},
		{"missing history", `{}`, "", "", false},
		{"nested limit", strings.Repeat("[", 514) + "null" + strings.Repeat("]", 514), "", "", false},
		{"invalid UTF8", "{\"history\":null,\"other\":\"\xff\"}", "", "", false},
		{"oversized", strings.Repeat(" ", 16<<20) + `{"history":null}`, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIssueRecoverySelection(IssueRecoverySnapshot{SelectedIssueID: tc.metadata, Document: []byte(tc.document)}, tc.selected)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
