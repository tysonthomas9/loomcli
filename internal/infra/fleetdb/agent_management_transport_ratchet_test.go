package fleetdb

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestAgentManagementCASMutationsHaveNoLegacyFallbackCallsites(t *testing.T) {
	const sourcePath = "agent_management_transport.go"
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, sourcePath, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		switch function.Name.Name {
		case "UpdateAgentRole", "DeleteAgentRole", "UpdateAgentServiceIdentity":
			start := files.Position(function.Body.Pos()).Offset
			end := files.Position(function.Body.End()).Offset
			bodies[function.Name.Name] = string(source[start:end])
		}
	}
	for _, name := range []string{
		"UpdateAgentRole",
		"DeleteAgentRole",
		"UpdateAgentServiceIdentity",
	} {
		if bodies[name] == "" {
			t.Fatalf("%s body not found", name)
		}
	}
	for _, name := range []string{"UpdateAgentRole", "DeleteAgentRole"} {
		for _, forbidden := range []string{
			"transport.client.roles.Get",
			"transport.client.roles.Update",
			"transport.client.roles.Delete",
			"transport.client.GetRole",
		} {
			if strings.Contains(bodies[name], forbidden) {
				t.Fatalf("%s contains legacy callsite %q", name, forbidden)
			}
		}
		if count := strings.Count(bodies[name], "DoWithHeaders"); count != 1 {
			t.Fatalf("%s Fleet command callsites = %d, want exactly 1", name, count)
		}
	}
	if !strings.Contains(bodies["UpdateAgentRole"], `"/definition"`) {
		t.Fatal("UpdateAgentRole no longer names the exact revision command route")
	}
	if !strings.Contains(bodies["DeleteAgentRole"], `"/delete"`) {
		t.Fatal("DeleteAgentRole no longer names the exact revision delete route")
	}

	identity := bodies["UpdateAgentServiceIdentity"]
	for _, forbidden := range []string{
		"transport.client.services.Get",
		"transport.client.services.Update",
		"transport.client.GetAgentService",
	} {
		if strings.Contains(identity, forbidden) {
			t.Fatalf("UpdateAgentServiceIdentity contains legacy callsite %q", forbidden)
		}
	}
	if !strings.Contains(identity, "ErrAgentServiceUnsupportedIdentityPatch") {
		t.Fatal("unsupported AgentService identity fields no longer fail closed")
	}
}
