package supervisor

import (
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

var skillYieldGrace = 90 * time.Second

func (s *Supervisor) buildClaimConstraints(ap *AgentProcess) cli.RoleConstraints {
	ae := ap.Entry
	if sourceRepos, err := config.ResolveAgentRepos(ap.Entry, s.Repos); err == nil {
		ae.SourceRepos = sourceRepos
	} else {
		slog.Warn("failed to resolve agent repos for task claim", "worktree", ap.Entry.Worktree, "err", err)
	}
	return cli.MergeRoleConstraints(ap.RoleConfig, ae)
}

func (s *Supervisor) fallbackClaimYieldPeer(ap *AgentProcess, match cli.TaskMatch) *AgentProcess {
	peer := s.betterFitIdlePeer(ap, match.Issue, match.Score)
	if peer == nil {
		clearSkillYield(ap, match.Issue.ID)
		return nil
	}
	if !withinSkillYieldGrace(ap, match.Issue.ID, time.Now()) {
		return nil
	}
	return peer
}

func (s *Supervisor) betterFitIdlePeer(ap *AgentProcess, issue backend.IssueData, currentScore int) *AgentProcess {
	s.AgentsMu.RLock()
	peers := append([]*AgentProcess(nil), s.Agents...)
	stopped := make(map[string]struct{}, len(s.StoppedAgents))
	for name := range s.StoppedAgents {
		stopped[name] = struct{}{}
	}
	s.AgentsMu.RUnlock()

	for _, peer := range peers {
		if peer == nil || peer == ap || peer.Entry.Mode == domain.AgentModeEphemeral {
			continue
		}
		if _, ok := stopped[peer.Entry.Worktree]; ok || !shouldClaimTaskForRole(peer) {
			continue
		}
		peer.Mu.Lock()
		idle := peer.Pid == 0 && peer.AssignedTaskID == "" && peer.RequestedTaskID == ""
		peer.Mu.Unlock()
		if !idle {
			continue
		}
		peerMatch := cli.MatchTask(issue, s.buildClaimConstraints(peer))
		if peerMatch.SkillMatches > 0 && !peerMatch.IsSkillFallback() && peerMatch.Score > currentScore {
			return peer
		}
	}
	return nil
}

func withinSkillYieldGrace(ap *AgentProcess, issueID string, now time.Time) bool {
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.SkillYields == nil {
		ap.SkillYields = make(map[string]time.Time)
	}
	first, ok := ap.SkillYields[issueID]
	if !ok {
		first = now
		ap.SkillYields[issueID] = first
	}
	if now.Sub(first) >= skillYieldGrace {
		delete(ap.SkillYields, issueID)
		return false
	}
	return true
}

func clearSkillYield(ap *AgentProcess, issueID string) {
	ap.Mu.Lock()
	delete(ap.SkillYields, issueID)
	ap.Mu.Unlock()
}

func pruneSkillYields(ap *AgentProcess, issues []backend.IssueData) {
	ready := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		ready[issue.ID] = struct{}{}
	}
	ap.Mu.Lock()
	for issueID := range ap.SkillYields {
		if _, ok := ready[issueID]; !ok {
			delete(ap.SkillYields, issueID)
		}
	}
	ap.Mu.Unlock()
}
