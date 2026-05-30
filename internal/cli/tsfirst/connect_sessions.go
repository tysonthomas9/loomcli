package tsfirst

import "strings"

const (
	connectResumeApplied     = "applied"
	connectResumeUnsupported = "unsupported"

	connectResumeMethodSetter                = "setter"
	connectResumeMethodStreamingResumed      = "streaming_resumed"
	connectResumeMethodNonInteractiveResumed = "noninteractive_resumed"
	connectResumeMethodNone                  = "none"
)

func setBackendResumeSessionID(backend any, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	resumer, ok := backend.(interface{ SetResumeSessionID(string) })
	if !ok {
		return false
	}
	resumer.SetResumeSessionID(sessionID)
	return true
}

func newConnectResume(sessionID string) *connectResume {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	return &connectResume{
		Requested:              true,
		PriorProviderSessionID: sessionID,
	}
}

func markResumeApplied(resume *connectResume, method string) {
	if resume == nil {
		return
	}
	resume.Status = connectResumeApplied
	resume.Method = strings.TrimSpace(method)
	resume.Message = ""
}

func markResumeUnsupported(resume *connectResume, backendName string) {
	if resume == nil || resume.Status == connectResumeApplied {
		return
	}
	backendName = strings.TrimSpace(backendName)
	if backendName == "" {
		backendName = "selected backend"
	}
	resume.Status = connectResumeUnsupported
	resume.Method = connectResumeMethodNone
	resume.Message = "backend " + backendName + " does not expose provider-native resume for local connect; local transcript history was included in the prompt"
}

func backendLastSessionID(backend any, workDir string) string {
	provider, ok := backend.(interface{ LastSessionID(string) string })
	if !ok {
		return ""
	}
	return strings.TrimSpace(provider.LastSessionID(workDir))
}

func fillBackendSessionID(result localInvocationResult, backend any, workDir string) localInvocationResult {
	if strings.TrimSpace(result.ProviderSessionID) != "" {
		return result
	}
	result.ProviderSessionID = backendLastSessionID(backend, workDir)
	return result
}
