package domain

import "testing"

func TestPublicTaskRunSessionID(t *testing.T) {
	tests := []struct {
		name string
		run  *TaskRun
		want string
	}{
		{name: "nil", run: nil, want: ""},
		{name: "empty", run: &TaskRun{}, want: ""},
		{
			name: "ordinary task run",
			run:  &TaskRun{TaskRunID: " task-run-1 "},
			want: "task-run-1",
		},
		{
			name: "flue runner kind",
			run: &TaskRun{
				TaskRunID:  "task-run-2",
				RunnerKind: " flue-workflow ",
			},
			want: "flue-task-run-2",
		},
		{
			name: "flue runtime metadata",
			run: &TaskRun{
				TaskRunID:       "task-run-3",
				RuntimeMetadata: map[string]string{"runtime": " flue "},
			},
			want: "flue-task-run-3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PublicTaskRunSessionID(tt.run); got != tt.want {
				t.Fatalf("PublicTaskRunSessionID() = %q, want %q", got, tt.want)
			}
		})
	}
}
