package harnessemu

import (
	"regexp"
	"strings"
)

// subagent plays OpenCode b30c4d0's subagent tool (tool/plugin/subagent.ts):
// it asserts the "subagent" action on the agent it starts against the
// session's installed rules, last match wins, and a deny fails the call
// with OpenCode's error and starts nothing; otherwise it starts a child
// session. No matching rule plays as allowed (no ask), as on a Loom lead
// before SA1. "" means the child started.
func (s *Server) subagent(sid string, input map[string]any) string {
	agent, _ := input["agent"].(string)
	rules, _ := s.st.Sessions[sid].Info["permissions"].([]any)
	for i := len(rules) - 1; i >= 0; i-- {
		r, _ := rules[i].(map[string]any)
		action, _ := r["action"].(string)
		resource, _ := r["resource"].(string)
		if wildcard(action, "subagent") && wildcard(resource, agent) {
			if r["effect"] == "deny" {
				return "Subagent denied: " + agent
			}
			break
		}
	}
	child := "ses_" + s.newID()
	s.st.Sessions[child] = &session{Info: map[string]any{"id": child, "parentID": sid, "metadata": map[string]any{}}}
	s.emit(child, "session.created", map[string]any{"parentID": sid})
	return ""
}

// wildcard matches OpenCode's permission patterns, where * is any run.
func wildcard(pattern, s string) bool {
	ok, _ := regexp.MatchString("^"+strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*")+"$", s)
	return ok
}
