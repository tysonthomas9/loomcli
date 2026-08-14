package execution

import "testing"

func TestPublicTaskRunSessionIDPreservesFlueRouteIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		run  *TaskRun
		want string
	}{
		{name: "nil", want: ""},
		{name: "ordinary", run: &TaskRun{TaskRunID: "run-1"}, want: "run-1"},
		{name: "flue kind", run: &TaskRun{TaskRunID: "run-2", RunnerKind: "flue-workflow"}, want: "flue-run-2"},
		{name: "flue runtime", run: &TaskRun{TaskRunID: "run-3", RuntimeMetadata: map[string]string{"runtime": "flue"}}, want: "flue-run-3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := PublicTaskRunSessionID(test.run); got != test.want {
				t.Fatalf("PublicTaskRunSessionID() = %q, want %q", got, test.want)
			}
		})
	}
}
