package onboarding

import (
	"net/http"
	"testing"
)

func TestModuleRegisterBranches(t *testing.T) {
	emptyMux := http.NewServeMux()
	NewModule(nil, nil).Register(emptyMux)

	mux := http.NewServeMux()
	NewModule(&stubIssueService{}, &stubAgentService{}).Register(mux)
}
