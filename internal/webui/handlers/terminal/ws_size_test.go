package terminal

import (
	"net/http/httptest"
	"testing"
)

func TestInitialTerminalSizeFromRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		target   string
		wantCols uint16
		wantRows uint16
	}{
		{
			name:     "uses explicit client size",
			target:   "/api/workspaces/ws/terminal/ws?session=s1&cols=132&rows=40",
			wantCols: 132,
			wantRows: 40,
		},
		{
			name:     "falls back for missing values",
			target:   "/api/workspaces/ws/terminal/ws?session=s1",
			wantCols: 80,
			wantRows: 24,
		},
		{
			name:     "falls back for invalid values",
			target:   "/api/workspaces/ws/terminal/ws?session=s1&cols=-1&rows=9999",
			wantCols: 80,
			wantRows: 24,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest("GET", tt.target, nil)
			gotCols, gotRows := initialTerminalSizeFromRequest(req)
			if gotCols != tt.wantCols || gotRows != tt.wantRows {
				t.Fatalf(
					"initialTerminalSizeFromRequest() = (%d, %d), want (%d, %d)",
					gotCols,
					gotRows,
					tt.wantCols,
					tt.wantRows,
				)
			}
		})
	}
}
