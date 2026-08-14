package interaction

import "testing"

func TestValidateTerminalSessionNameEnforcesInteractionPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sessionName string
		wantErr     bool
	}{
		{name: "valid", sessionName: "loom-work-123"},
		{name: "underscore", sessionName: "loom_work_123"},
		{name: "empty", sessionName: "", wantErr: true},
		{name: "space", sessionName: "bad session", wantErr: true},
		{name: "dot", sessionName: "bad.session", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateTerminalSessionName(test.sessionName); (err != nil) != test.wantErr {
				t.Fatalf("ValidateSessionName(%q) error = %v, wantErr %v", test.sessionName, err, test.wantErr)
			}
		})
	}
}
