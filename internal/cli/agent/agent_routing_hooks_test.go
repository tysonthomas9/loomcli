package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
)

func TestRunAgentDispatchesModesWithHooks(t *testing.T) {
	promptPath := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(promptPath, []byte("hello {{.AgentName}}"), 0600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	worktree := t.TempDir()

	oldValidate := validatePromptFileFn
	oldMap := mapTaskFilterFn
	oldResolve := resolveAgentTargetFn
	oldDaemon := runAgentDaemonFn
	oldAuto := runAgentAutoModeFn
	oldSingle := runAgentSingleTaskFn
	oldPrompt, oldFilter, oldAutoMode, oldDaemonMode, oldParent := agentPromptFile, agentTaskFilter, agentAutoMode, agentDaemonMode, agentParentID
	t.Cleanup(func() {
		validatePromptFileFn = oldValidate
		mapTaskFilterFn = oldMap
		resolveAgentTargetFn = oldResolve
		runAgentDaemonFn = oldDaemon
		runAgentAutoModeFn = oldAuto
		runAgentSingleTaskFn = oldSingle
		agentPromptFile, agentTaskFilter, agentAutoMode, agentDaemonMode, agentParentID = oldPrompt, oldFilter, oldAutoMode, oldDaemonMode, oldParent
	})

	agentPromptFile = promptPath
	agentTaskFilter = "any"
	agentParentID = "EPIC-1"
	validatePromptFileFn = func(path string) {
		if path != promptPath {
			t.Fatalf("validatePromptFile path = %q, want %q", path, promptPath)
		}
	}
	mapTaskFilterFn = func(filter, parentID string) (func() (bool, error), error) {
		if filter != "any" || parentID != "EPIC-1" {
			t.Fatalf("mapTaskFilter args = %q %q", filter, parentID)
		}
		return func() (bool, error) { return true, nil }, nil
	}
	resolveAgentTargetFn = func(name, repo string) (workspace.ResolvedTarget, error) {
		if name != "falcon" || repo != "" {
			t.Fatalf("resolveAgentTarget args = %q %q", name, repo)
		}
		return workspace.ResolvedTarget{WorkDir: worktree, AgentName: "falcon"}, nil
	}

	var calls []string
	runAgentSingleTaskFn = func(path, name string, promptGen func(string, *cfgpkg.WorkspaceConfig) string, check func() (bool, error)) {
		calls = append(calls, "single:"+path+":"+name+":"+promptGen(name, nil))
		ok, err := check()
		if !ok || err != nil {
			t.Fatalf("check = %v %v", ok, err)
		}
	}
	runAgentAutoModeFn = func(path, name string, promptGen func(string, *cfgpkg.WorkspaceConfig) string, check func() (bool, error)) {
		calls = append(calls, "auto:"+path+":"+name+":"+promptGen(name, nil))
	}
	runAgentDaemonFn = func(path, name string, promptGen func(string, *cfgpkg.WorkspaceConfig) string) {
		calls = append(calls, "daemon:"+path+":"+name+":"+promptGen(name, nil))
	}

	agentAutoMode, agentDaemonMode = false, false
	runAgent(&cobra.Command{}, []string{"falcon"})
	agentAutoMode, agentDaemonMode = true, false
	runAgent(&cobra.Command{}, []string{"falcon"})
	agentAutoMode, agentDaemonMode = false, true
	runAgent(&cobra.Command{}, []string{"falcon"})

	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want three dispatches", calls)
	}
	for _, want := range []string{"single:", "auto:", "daemon:"} {
		found := false
		for _, call := range calls {
			if len(call) >= len(want) && call[:len(want)] == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("calls = %#v, missing prefix %q", calls, want)
		}
	}
}

func TestRunPlanAndTaskDispatchAndFallbackHooks(t *testing.T) {
	worktree := t.TempDir()

	oldResolve := resolveAgentTargetFn
	oldTmux := automodeTmuxFn
	oldSignal := setupSignalHandlerFn
	oldTmuxRun := runAutoModeTmuxFn
	oldLoop := runAutoModeLoopFn
	oldPlanDaemon := runPlanDaemonFn
	oldPlanAuto := runPlanAutoFallbackFn
	oldPlanSingle := runPlanSingleTaskFn
	oldTaskDaemon := runTaskDaemonFn
	oldTaskAuto := runTaskAutoFallbackFn
	oldTaskSingle := runTaskSingleTaskFn
	oldPlanFlags := []any{planAutoMode, planDaemonMode, planInterval, planMaxTasks, planIdleTimeout, planParentID}
	oldTaskFlags := []any{taskAutoMode, taskDaemonMode, taskInterval, taskMaxTasks, taskIdleTimeout, taskParentID}
	t.Cleanup(func() {
		resolveAgentTargetFn = oldResolve
		automodeTmuxFn = oldTmux
		setupSignalHandlerFn = oldSignal
		runAutoModeTmuxFn = oldTmuxRun
		runAutoModeLoopFn = oldLoop
		runPlanDaemonFn = oldPlanDaemon
		runPlanAutoFallbackFn = oldPlanAuto
		runPlanSingleTaskFn = oldPlanSingle
		runTaskDaemonFn = oldTaskDaemon
		runTaskAutoFallbackFn = oldTaskAuto
		runTaskSingleTaskFn = oldTaskSingle
		planAutoMode = oldPlanFlags[0].(bool)
		planDaemonMode = oldPlanFlags[1].(bool)
		planInterval = oldPlanFlags[2].(int)
		planMaxTasks = oldPlanFlags[3].(int)
		planIdleTimeout = oldPlanFlags[4].(int)
		planParentID = oldPlanFlags[5].(string)
		taskAutoMode = oldTaskFlags[0].(bool)
		taskDaemonMode = oldTaskFlags[1].(bool)
		taskInterval = oldTaskFlags[2].(int)
		taskMaxTasks = oldTaskFlags[3].(int)
		taskIdleTimeout = oldTaskFlags[4].(int)
		taskParentID = oldTaskFlags[5].(string)
	})

	resolveAgentTargetFn = func(name, repo string) (workspace.ResolvedTarget, error) {
		if repo != "" {
			t.Fatalf("repo = %q, want empty", repo)
		}
		if name == "" {
			name = "cwd"
		}
		return workspace.ResolvedTarget{WorkDir: worktree, AgentName: name}, nil
	}
	setupSignalHandlerFn = func() chan struct{} { return make(chan struct{}) }

	var calls []string
	runPlanDaemonFn = func(*cli.Deps, string, string) { calls = append(calls, "plan-daemon") }
	runPlanAutoFallbackFn = func(*cli.Deps, string, string, func() (bool, error)) { calls = append(calls, "plan-auto-fallback") }
	runPlanSingleTaskFn = func(*cli.Deps, string, string, func() (bool, error)) { calls = append(calls, "plan-single") }
	runTaskDaemonFn = func(*cli.Deps, string, string) { calls = append(calls, "task-daemon") }
	runTaskAutoFallbackFn = func(*cli.Deps, string, string, func() (bool, error)) { calls = append(calls, "task-auto-fallback") }
	runTaskSingleTaskFn = func(*cli.Deps, string, string, func() (bool, error)) { calls = append(calls, "task-single") }
	runAutoModeTmuxFn = func(opts automode.AutoModeOptions, _ chan struct{}) {
		calls = append(calls, opts.AgentType+"-tmux:"+opts.AgentName)
	}
	runAutoModeLoopFn = func(opts automode.AutoModeOptions, _ chan struct{}) {
		if opts.CustomPromptGen != nil && opts.CustomPromptGen(opts.AgentName, nil) == "" {
			t.Fatalf("%s prompt generator returned empty prompt", opts.AgentType)
		}
		calls = append(calls, opts.AgentType+"-loop:"+opts.AgentName)
	}

	automodeTmuxFn = func() bool { return false }
	planAutoMode, planDaemonMode, planParentID = false, false, "EPIC-P"
	runPlan(&cobra.Command{}, []string{"planner"})
	planAutoMode, planDaemonMode = true, false
	runPlan(&cobra.Command{}, []string{"planner"})
	planAutoMode, planDaemonMode = false, true
	runPlan(&cobra.Command{}, []string{"planner"})
	planAutoMode, planDaemonMode = true, false
	automodeTmuxFn = func() bool { return true }
	runPlan(&cobra.Command{}, []string{"planner"})
	runPlanAutoFallback(cli.GetDeps(&cobra.Command{}), worktree, "planner", func() (bool, error) { return true, nil })

	automodeTmuxFn = func() bool { return false }
	taskAutoMode, taskDaemonMode, taskParentID = false, false, "EPIC-T"
	runTask(&cobra.Command{}, []string{"worker"})
	taskAutoMode, taskDaemonMode = true, false
	runTask(&cobra.Command{}, []string{"worker"})
	taskAutoMode, taskDaemonMode = false, true
	runTask(&cobra.Command{}, []string{"worker"})
	taskAutoMode, taskDaemonMode = true, false
	automodeTmuxFn = func() bool { return true }
	runTask(&cobra.Command{}, []string{"worker"})
	runTaskAutoFallback(cli.GetDeps(&cobra.Command{}), worktree, "worker", func() (bool, error) { return true, nil })

	want := []string{
		"plan-single", "plan-auto-fallback", "plan-daemon", "plan-tmux:planner", "plan-loop:planner",
		"task-single", "task-auto-fallback", "task-daemon", "task-tmux:worker", "task-loop:worker",
	}
	for _, expected := range want {
		found := false
		for _, call := range calls {
			if call == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("calls = %#v, missing %q", calls, expected)
		}
	}
}

func TestAgentAutoModeUsesTmuxAndLoopHooks(t *testing.T) {
	oldTmux := automodeTmuxFn
	oldSignal := setupSignalHandlerFn
	oldTmuxRun := runAutoModeTmuxFn
	oldLoop := runAutoModeLoopFn
	t.Cleanup(func() {
		automodeTmuxFn = oldTmux
		setupSignalHandlerFn = oldSignal
		runAutoModeTmuxFn = oldTmuxRun
		runAutoModeLoopFn = oldLoop
	})

	setupSignalHandlerFn = func() chan struct{} { return make(chan struct{}) }
	var calls []string
	runAutoModeTmuxFn = func(opts automode.AutoModeOptions, _ chan struct{}) {
		calls = append(calls, "tmux:"+opts.AgentType+":"+opts.AgentName)
	}
	runAutoModeLoopFn = func(opts automode.AutoModeOptions, _ chan struct{}) {
		calls = append(calls, "loop:"+opts.AgentType+":"+opts.AgentName)
	}
	promptGen := func(string, *cfgpkg.WorkspaceConfig) string { return "prompt" }
	check := func() (bool, error) { return true, nil }

	automodeTmuxFn = func() bool { return true }
	runAgentAutoMode(t.TempDir(), "agent-a", promptGen, check)

	automodeTmuxFn = func() bool { return false }
	runAgentAutoMode(t.TempDir(), "agent-b", promptGen, check)

	want := []string{"tmux:agent:agent-a", "loop:agent:agent-b"}
	for _, expected := range want {
		found := false
		for _, call := range calls {
			if call == expected {
				found = true
			}
		}
		if !found {
			t.Fatalf("calls = %#v, missing %q", calls, expected)
		}
	}

}
