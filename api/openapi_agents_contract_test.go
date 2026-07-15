package api_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	agentCollectionPath = "/api/workspaces/{ws}/agents"
	agentItemPath       = "/api/workspaces/{ws}/agents/{name}"
)

type openAPIDocument struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

func TestAgentModuleRoutesAreDocumented(t *testing.T) {
	doc := readOpenAPI(t)
	source, err := os.ReadFile("../internal/webui/handlers/agents/module.go")
	if err != nil {
		t.Fatalf("read agents module routes: %v", err)
	}

	routePattern := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) ([^"]+)"`)
	matches := routePattern.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("agents module contains no statically registered routes")
	}
	for _, match := range matches {
		method, path := strings.ToLower(match[1]), match[2]
		pathItem, ok := doc.Paths[path]
		if !ok {
			t.Errorf("agents module route %s %s is missing from api/openapi.yaml", match[1], path)
			continue
		}
		if _, ok := pathItem[method]; !ok {
			t.Errorf("agents module route %s %s has no matching OpenAPI operation", match[1], path)
		}
	}
}

func TestUnifiedAgentDiscriminatorContract(t *testing.T) {
	doc := readOpenAPI(t)

	wantKinds := []string{"binding", "prompt", "scripted", "supervised"}
	gotKinds := discriminatorKinds(t, doc.Components.Schemas["UnifiedAgent"])
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("UnifiedAgent discriminator kinds = %v, want %v", gotKinds, wantKinds)
	}
	listSchema := operationSchema(t, doc, agentCollectionPath, "get", "responses", "200", "content", "application/json", "schema")
	assertRef(t, listSchema, "#/components/schemas/UnifiedAgentListResponse")
	listProperties := childMap(t, doc.Components.Schemas["UnifiedAgentListResponse"], "properties")
	listData := childMap(t, listProperties, "data")
	assertRef(t, childMap(t, listData, "items"), "#/components/schemas/UnifiedAgent")
	createSchema := operationSchema(t, doc, agentCollectionPath, "post", "requestBody", "content", "application/json", "schema")
	assertRef(t, createSchema, "#/components/schemas/CreateUnifiedAgentRequest")
	createdSchema := operationSchema(t, doc, agentCollectionPath, "post", "responses", "201", "content", "application/json", "schema")
	assertRef(t, createdSchema, "#/components/schemas/CreatedUnifiedAgent")

	item := doc.Paths[agentItemPath]
	for _, method := range []string{"get", "patch", "delete"} {
		if _, ok := item[method]; !ok {
			t.Errorf("unified agent item path is missing %s", strings.ToUpper(method))
		}
	}
}

func TestCreateUnifiedAgentRequestPreservesLegacyOmittedKind(t *testing.T) {
	doc := readOpenAPI(t)
	request := doc.Components.Schemas["CreateUnifiedAgentRequest"]
	if _, ok := request["discriminator"]; ok {
		t.Fatal("CreateUnifiedAgentRequest must not require a discriminator because legacy supervised requests omit kind")
	}
	refs := schemaRefs(t, request, "oneOf")
	wantRefs := []string{
		"#/components/schemas/CreatePromptAgentRequest",
		"#/components/schemas/CreateSupervisedAgentRequest",
	}
	slices.Sort(refs)
	slices.Sort(wantRefs)
	if !slices.Equal(refs, wantRefs) {
		t.Fatalf("CreateUnifiedAgentRequest oneOf refs = %v, want %v", refs, wantRefs)
	}

	supervised := doc.Components.Schemas["CreateSupervisedAgentRequest"]
	if slices.Contains(stringSlice(t, supervised, "required"), "kind") {
		t.Fatal("CreateSupervisedAgentRequest.kind is required, want legacy-compatible optional field")
	}
	properties := childMap(t, supervised, "properties")
	kind := childMap(t, properties, "kind")
	gotKinds := stringSlice(t, kind, "enum")
	slices.Sort(gotKinds)
	wantKinds := []string{"interactive", "supervised", "worker"}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("CreateSupervisedAgentRequest.kind enum = %v, want %v", gotKinds, wantKinds)
	}
}

func TestAgentOperationsDocumentActualErrorEnvelopeAndStatuses(t *testing.T) {
	doc := readOpenAPI(t)
	agentError := doc.Components.Schemas["AgentErrorResponse"]
	if got := stringSlice(t, agentError, "required"); !slices.Equal(got, []string{"error"}) {
		t.Fatalf("AgentErrorResponse required = %v, want [error]", got)
	}
	properties := childMap(t, agentError, "properties")
	if _, ok := properties["success"]; ok {
		t.Fatal("AgentErrorResponse documents success, but unified agent handlers do not emit it")
	}
	if _, ok := properties["kind"]; !ok {
		t.Fatal("AgentErrorResponse is missing service-layer kind")
	}

	tests := []struct {
		path     string
		method   string
		statuses []string
	}{
		{agentCollectionPath, "get", []string{"409", "500", "503"}},
		{agentCollectionPath, "post", []string{"400", "409", "413", "500", "503"}},
		{agentItemPath, "get", []string{"404", "409", "500", "503"}},
		{agentItemPath, "patch", []string{"400", "404", "409", "413", "500", "503"}},
		{agentItemPath, "delete", []string{"404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{name}/queue", "get", []string{"501"}},
		{"/api/workspaces/{ws}/agents/{id}/enable", "post", []string{"400", "404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{id}/disable", "post", []string{"400", "404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{id}/runs", "get", []string{"400", "404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{name}/stop", "post", []string{"400", "404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{name}/start", "post", []string{"400", "404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{name}/restart", "post", []string{"400", "404", "409", "500", "503"}},
		{"/api/workspaces/{ws}/agents/{name}/yield", "post", []string{"400", "404", "409", "500", "503"}},
	}
	for _, tt := range tests {
		for _, status := range tt.statuses {
			schema := operationSchema(t, doc, tt.path, tt.method, "responses", status, "content", "application/json", "schema")
			assertRef(t, schema, "#/components/schemas/AgentErrorResponse")
		}
	}
}

func readOpenAPI(t *testing.T) openAPIDocument {
	t.Helper()
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	var doc openAPIDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	return doc
}

func discriminatorKinds(t *testing.T, schema map[string]any) []string {
	t.Helper()
	discriminator := childMap(t, schema, "discriminator")
	mapping := childMap(t, discriminator, "mapping")
	kinds := make([]string, 0, len(mapping))
	for kind := range mapping {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

func operationSchema(t *testing.T, doc openAPIDocument, path, method string, keys ...string) map[string]any {
	t.Helper()
	current, ok := doc.Paths[path][method].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI operation %s %s is missing or invalid", strings.ToUpper(method), path)
	}
	for _, key := range keys {
		current = childMap(t, current, key)
	}
	return current
}

func childMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	child, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI key %q is missing or is not an object", key)
	}
	return child
}

func stringSlice(t *testing.T, parent map[string]any, key string) []string {
	t.Helper()
	raw, ok := parent[key].([]any)
	if !ok {
		t.Fatalf("OpenAPI key %q is missing or is not an array", key)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			t.Fatalf("OpenAPI key %q contains non-string value %#v", key, item)
		}
		out = append(out, value)
	}
	return out
}

func schemaRefs(t *testing.T, schema map[string]any, key string) []string {
	t.Helper()
	raw, ok := schema[key].([]any)
	if !ok {
		t.Fatalf("OpenAPI key %q is missing or is not an array", key)
	}
	refs := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("OpenAPI key %q contains non-object value %#v", key, item)
		}
		ref, ok := entry["$ref"].(string)
		if !ok {
			t.Fatalf("OpenAPI key %q entry has no $ref: %#v", key, entry)
		}
		refs = append(refs, ref)
	}
	return refs
}

func assertRef(t *testing.T, schema map[string]any, want string) {
	t.Helper()
	if got, _ := schema["$ref"].(string); got != want {
		t.Fatalf("schema ref = %q, want %q", got, want)
	}
}
