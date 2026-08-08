package sessioncoord

import "testing"

func TestValidateSessionHistoryIssueIDDelegatesToInteractionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		issueID string
		wantErr bool
	}{
		{name: "valid", issueID: "loomcli-fghge.1"},
		{name: "empty", issueID: "", wantErr: true},
		{name: "space", issueID: "bad id", wantErr: true},
		{name: "slash", issueID: "bad/id", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateSessionHistoryIssueID(test.issueID); (err != nil) != test.wantErr {
				t.Fatalf("ValidateSessionHistoryIssueID(%q) error = %v, wantErr %v", test.issueID, err, test.wantErr)
			}
		})
	}
}
