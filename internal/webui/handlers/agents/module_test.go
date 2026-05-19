package agents

import (
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestModuleRegisterBranches(t *testing.T) {
	emptyMux := http.NewServeMux()
	NewModule(nil, nil).Register(emptyMux)

	mux := http.NewServeMux()
	NewModule(&fakeAgentService{}, realtime.NewHub()).Register(mux)
}
