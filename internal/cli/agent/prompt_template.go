package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"text/template/parse"
)

// PromptData is the template context for custom prompt files.
//
// A custom role owns its whole prompt: unlike the built-in planning/task
// prompts — which get a workspace table, an epic-scope line, the safety rules
// and a prior-attempt checkpoint spliced in for them — nothing is prepended to
// a custom prompt. These fields are how a custom prompt asks for those same
// pieces, by name and one at a time, without being handed any of them.
//
// The fields come in two tiers:
//
//   - Identity (AgentName, WorktreeName, Role, TaskID, EpicID) is always
//     populated. Each one is a function argument or a single environment read,
//     so there is nothing worth gating.
//   - Context blocks (WorkspaceBlock, EpicScope, SafetyBlock, CheckpointBlock,
//     TaskDetail) are populated ONLY when the template names them — see
//     referencedPromptFields. TaskDetail costs an issue-backend round trip and
//     CheckpointBlock touches the worktree lock directory, so a prompt that
//     never mentions them never pays for them.
//
// Anything the template does not name renders as the empty string, so a prompt
// file written before these fields existed produces byte-identical output.
type PromptData struct {
	// --- Identity: always populated ---

	// AgentName is the agent (worktree) name this run was launched for.
	AgentName string
	// WorktreeName mirrors AgentName. Kept distinct because workspace-mode
	// agents may one day diverge from their worktree name.
	WorktreeName string
	// Role is the real role name the daemon spawned this agent under
	// (LOOM_ROLE), falling back to "custom" outside the daemon.
	Role string
	// TaskID is the task the daemon pre-claimed for this run
	// (LOOM_ASSIGNED_TASK_ID). Empty in one-shot and auto mode, where the
	// agent selects and claims its own task mid-turn.
	TaskID string
	// EpicID is the epic this agent is scoped to (--parent), or "" when it is
	// free to pick from the whole backlog.
	EpicID string

	// --- Context blocks: populated only when the template references them ---

	// WorkspaceBlock is the multi-repo workspace section: the repo/path/branch
	// table plus the "run git here, run loom data there" rules. Empty outside
	// workspace mode.
	WorkspaceBlock string
	// EpicScope is the one-line "you must only select tasks from this epic"
	// instruction the built-in prompts use. Empty when EpicID is empty.
	EpicScope string
	// SafetyBlock is the shared multi-agent safety rules section (do not stash,
	// do not switch branches, do not clean up another agent's files).
	SafetyBlock string
	// CheckpointBlock is the "PREVIOUS ATTEMPT CONTEXT" section describing the
	// last crashed or preempted attempt in this worktree. Empty when there is
	// no checkpoint or when a session resume is armed (the resumed session
	// already carries that context).
	CheckpointBlock string
	// TaskDetail is the full rendered detail of TaskID — title, status,
	// priority, labels, description, design, acceptance criteria, notes and
	// dependencies. Empty when there is no pre-claimed task or the fetch fails.
	// This is the only field that talks to the issue backend.
	TaskDetail string
}

// promptFieldRefs is the set of PromptData field names a parsed template reads.
type promptFieldRefs map[string]bool

// has reports whether the template referenced the named PromptData field.
func (r promptFieldRefs) has(field string) bool { return r[field] }

// referencedPromptFields returns the PromptData field names the template reads.
//
// This walks the parse tree rather than scanning the raw file for field names.
// The tree is the honest answer and it is free: Execute needs the template
// parsed anyway, so the scan is one pass over a structure we already built —
// no second read, no second parse. A raw-source scan would also be tempting
// (it is three lines) but it cannot tell a reference from prose, so a prompt
// whose instructions merely mention the words "TaskDetail" would trigger a
// fleet round trip it never asked for. The tree walk finds references anywhere
// a field can legally appear — inside if/range/with bodies, inside pipelines,
// and inside {{define}} blocks associated with the file.
//
// What it misses: templates that render the context wholesale instead of by
// name, i.e. {{.}} or {{printf "%v" .}}. Those name no field, so the gated
// blocks stay empty and only the identity fields show up. Text/template has no
// dynamic struct-field access (index works on maps and slices, not fields), so
// that whole-struct case is the only gap — everything else must spell the field
// out, and spelling it out is exactly what opting in means here.
func referencedPromptFields(tmpl *template.Template) promptFieldRefs {
	refs := promptFieldRefs{}
	for _, t := range tmpl.Templates() {
		if t.Tree == nil {
			continue
		}
		collectPromptFields(t.Tree.Root, refs)
	}
	return refs
}

// collectPromptFields recursively records every field identifier reachable from
// node. Node types with no children fall through the default case.
func collectPromptFields(node parse.Node, refs promptFieldRefs) {
	switch n := node.(type) {
	case nil:
		return
	case *parse.FieldNode:
		// {{.TaskDetail}} parses to Ident ["TaskDetail"]; {{.A.B}} to
		// ["A","B"]. Only the first hop can name a PromptData field.
		if len(n.Ident) > 0 {
			refs[n.Ident[0]] = true
		}
	case *parse.ChainNode:
		// {{(pipeline).Field}} — the base pipeline may itself reference fields.
		collectPromptFields(n.Node, refs)
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			collectPromptFields(child, refs)
		}
	case *parse.ActionNode:
		collectPromptFields(n.Pipe, refs)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, cmd := range n.Cmds {
			collectPromptFields(cmd, refs)
		}
	case *parse.CommandNode:
		for _, arg := range n.Args {
			collectPromptFields(arg, refs)
		}
	case *parse.IfNode:
		collectBranchPromptFields(n.BranchNode, refs)
	case *parse.RangeNode:
		collectBranchPromptFields(n.BranchNode, refs)
	case *parse.WithNode:
		collectBranchPromptFields(n.BranchNode, refs)
	case *parse.TemplateNode:
		collectPromptFields(n.Pipe, refs)
	}
}

// collectBranchPromptFields walks the shared shape of if/range/with: the
// controlling pipeline plus both bodies.
func collectBranchPromptFields(b parse.BranchNode, refs promptFieldRefs) {
	collectPromptFields(b.Pipe, refs)
	if b.List != nil {
		collectPromptFields(b.List, refs)
	}
	if b.ElseList != nil {
		collectPromptFields(b.ElseList, refs)
	}
}

// LoadPromptTemplate reads a prompt template file and executes it with the given data.
// Returns the rendered prompt string.
func LoadPromptTemplate(path string, data PromptData) (string, error) {
	return loadPromptTemplateWith(path, func(promptFieldRefs) PromptData { return data })
}

// loadPromptTemplateWith reads and parses the prompt template at path, hands
// build the set of PromptData fields the template actually references, and
// executes the template with whatever build returns.
//
// The callback is the whole point: it is the only place where "which fields
// does this file want?" is known before the expensive fields would have to be
// computed. Callers that have nothing to skip use LoadPromptTemplate.
func loadPromptTemplateWith(path string, build func(promptFieldRefs) PromptData) (string, error) {
	content, err := os.ReadFile(path) // #nosec G304 — path from LoadPromptTemplate callers, not user input
	if err != nil {
		return "", fmt.Errorf("reading prompt template %s: %w", path, err)
	}

	tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parsing prompt template %s: %w", path, err)
	}

	data := build(referencedPromptFields(tmpl))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing prompt template %s: %w", path, err)
	}

	return buf.String(), nil
}
