package agentmodules

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

var _ func(Deps) []interface{ Register(*http.ServeMux) } = New

func TestProductionCompositionUsesAutomationAwareAgentsConstructor(t *testing.T) {
	rootSource, err := os.ReadFile("agentmodules.go")
	if err != nil {
		t.Fatalf("read root production composition: %v", err)
	}
	automationSource, err := os.ReadFile("automationroutes/routes.go")
	if err != nil {
		t.Fatalf("read Automation production composition: %v", err)
	}
	workspaceSource, err := os.ReadFile("workspace_routes.go")
	if err != nil {
		t.Fatalf("read workspace route composition: %v", err)
	}
	text := string(rootSource) + string(automationSource) + string(workspaceSource)
	if strings.Contains(text, "agents.NewModule(") {
		t.Fatal("production composition still uses inert legacy agents constructor")
	}
	for _, required := range []string{
		"agents.New(agents.Config{", "Bindings: deps.AutomationBindings",
		"SessionTranscripts: deps.AgentSessionTranscripts",
		"OperatorAuthority: deps.AutomationOperator", "automationModules.BindingGrants",
		"Provisioning: deps.AgentProvisioning",
		"ProvisioningAuthority: deps.AgentProvisioningOperator",
		"interactionmanagement.New(interactionmanagement.Config{",
		"Interaction: deps.Interaction",
		"Authority: deps.InteractionOperator",
		"CreateWorkflow: deps.Capabilities.WorkflowBinding",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("production composition is missing %q", required)
		}
	}
}

func TestNewPreservesRegistrationOrder(t *testing.T) {
	modules := New(Deps{})
	got := make([]string, 0, len(modules))
	for _, module := range modules {
		got = append(got, fmt.Sprintf("%T", module))
	}
	want := []string{
		"*agents.Module",
		"*agentsmanagement.Module",
		"*interactionmanagement.Module",
		"*onboarding.Module",
		"*workflows.Module",
		"*executionmanagement.Module",
		"*webhooks.Module",
		"*roles.Module",
		"*triggerbindings.Module",
		"*connectors.Module",
		"*approvals.Module",
		"*taskrunapi.Module",
		"*driverapi.Module",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("module registration order = %v, want %v", got, want)
	}
}
