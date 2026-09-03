package store

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestTaskRunClaimAttempt(t *testing.T) {
	tests := []struct {
		name string
		run  *domain.TaskRun
		want int
	}{
		{name: "nil run", want: 1},
		{name: "missing metadata", run: &domain.TaskRun{}, want: 1},
		{name: "first claim", run: &domain.TaskRun{RuntimeMetadata: map[string]string{"scheduler_attempt": "0"}}, want: 1},
		{name: "third claim", run: &domain.TaskRun{RuntimeMetadata: map[string]string{"scheduler_attempt": "2"}}, want: 3},
		{name: "negative fallback", run: &domain.TaskRun{RuntimeMetadata: map[string]string{"scheduler_attempt": "-1"}}, want: 1},
		{name: "invalid fallback", run: &domain.TaskRun{RuntimeMetadata: map[string]string{"scheduler_attempt": "wat"}}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TaskRunClaimAttempt(tt.run); got != tt.want {
				t.Fatalf("TaskRunClaimAttempt() = %d, want %d", got, tt.want)
			}
		})
	}
}
