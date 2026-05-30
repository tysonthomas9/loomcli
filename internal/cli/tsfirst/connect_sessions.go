package tsfirst

import "strings"

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
