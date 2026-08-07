package daytona

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	sdkerrors "github.com/daytonaio/daytona/libs/sdk-go/pkg/errors"

	"github.com/tysonthomas9/loomcli/internal/placement"
)

func TestCreatePayloadPassesLabelsEnvResourceAndAllowlist(t *testing.T) {
	provider := &Provider{
		snapshotName:           DefaultSnapshotName,
		snapshotID:             DefaultSnapshotID,
		createAutoStopInterval: defaultCreateAutoStop,
	}
	labels := map[string]string{
		placement.PlacementLabelKey:   "lead-placement-1",
		placement.EnvironmentLabelKey: "prod",
		"loom-workspace":              "WS",
		"loom-agent":                  "nova",
	}
	payload, err := provider.createPayload(placement.CreateRequest{
		SnapshotRef: DefaultSnapshotID,
		Labels:      labels,
		Env: map[string]string{
			"KEEP":    "yes",
			APIKeyEnv: "must-not-enter-sandbox",
		},
		NetworkDomainAllowlist: []string{"github.com", " *.example.com ", ""},
	})
	if err != nil {
		t.Fatalf("createPayload: %v", err)
	}

	if got := payload.GetSnapshot(); got != DefaultSnapshotName {
		t.Fatalf("snapshot = %q, want %q", got, DefaultSnapshotName)
	}
	if got := payload.GetLabels(); !reflect.DeepEqual(got, labels) {
		t.Fatalf("labels = %#v, want verbatim %#v", got, labels)
	}
	payload.GetLabels()["extra"] = "mutated"
	if _, ok := labels["extra"]; ok {
		t.Fatal("labels map was aliased into create payload")
	}
	env := payload.GetEnv()
	if env["KEEP"] != "yes" {
		t.Fatalf("env KEEP = %q, want yes", env["KEEP"])
	}
	if _, ok := env[APIKeyEnv]; ok {
		t.Fatalf("%s was copied into sandbox env", APIKeyEnv)
	}
	if _, ok := payload.GetCpuOk(); ok {
		t.Fatal("cpu was sent alongside a snapshot; Daytona rejects the create")
	}
	if _, ok := payload.GetMemoryOk(); ok {
		t.Fatal("memory was sent alongside a snapshot; Daytona rejects the create")
	}
	if got := payload.GetDomainAllowList(); got != "github.com,*.example.com" {
		t.Fatalf("domain allowlist = %q", got)
	}
	if got := payload.GetAutoStopInterval(); got != 15 {
		t.Fatalf("autoStopInterval = %d, want 15", got)
	}
}

func TestCreatePayloadStripsLeadHookEnv(t *testing.T) {
	provider := &Provider{snapshotName: DefaultSnapshotName, snapshotID: DefaultSnapshotID, createAutoStopInterval: -1}
	payload, err := provider.createPayload(placement.CreateRequest{
		SnapshotRef: DefaultSnapshotName,
		Env: map[string]string{
			"KEEP":            "yes",
			leadBootEnv:       "1",
			leadWorkdirEnv:    "/wrong",
			leadPromptFileEnv: "/wrong.md",
		},
	})
	if err != nil {
		t.Fatalf("createPayload: %v", err)
	}
	env := payload.GetEnv()
	if env["KEEP"] != "yes" {
		t.Fatalf("env KEEP = %q, want yes", env["KEEP"])
	}
	assertNoLeadHookEnv(t, env)
}

func TestCreatePayloadHonorsExplicitResource(t *testing.T) {
	provider := &Provider{snapshotName: DefaultSnapshotName, snapshotID: DefaultSnapshotID, createAutoStopInterval: -1}
	payload, err := provider.createPayload(placement.CreateRequest{
		SnapshotRef: DefaultSnapshotName,
		Resource:    placement.ResourceSize{VCPU: 2, MemGiB: 4},
	})
	if err != nil {
		t.Fatalf("createPayload: %v", err)
	}
	// Daytona rejects a create carrying both a snapshot and explicit sizing
	// ("Cannot specify Sandbox resources when using a snapshot"), and every
	// placement uses a snapshot -- so sending these fails every create. Size
	// comes from the snapshot instead.
	if _, ok := payload.GetCpuOk(); ok {
		t.Fatal("cpu was sent alongside a snapshot; Daytona rejects the create")
	}
	if _, ok := payload.GetMemoryOk(); ok {
		t.Fatal("memory was sent alongside a snapshot; Daytona rejects the create")
	}
	if _, ok := payload.GetAutoStopIntervalOk(); ok {
		t.Fatal("autoStopInterval was set when config disabled it")
	}
}

func TestCreatePayloadRejectsOverLimitDomainAllowlist(t *testing.T) {
	provider := &Provider{snapshotName: DefaultSnapshotName, snapshotID: DefaultSnapshotID, createAutoStopInterval: -1}
	domains := make([]string, 21)
	for i := range domains {
		domains[i] = "h.example.test"
	}
	_, err := provider.createPayload(placement.CreateRequest{
		SnapshotRef:            DefaultSnapshotName,
		NetworkDomainAllowlist: domains,
		Resource:               placement.ResourceSize{VCPU: 1, MemGiB: 2},
	})
	if err == nil || !strings.Contains(err.Error(), "max 20") {
		t.Fatalf("createPayload over limit = %v, want max 20 error", err)
	}
}

func TestCreateSendsCreateTimePayload(t *testing.T) {
	var captured map[string]any
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.Path != "/api/sandbox" {
			t.Fatalf("request = %s %s, want POST /api/sandbox", req.Method, req.URL.Path)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://proxy.test/toolbox", map[string]string{
			placement.PlacementLabelKey: "lead-placement-1",
			"loom-workspace":            "WS",
		})), nil
	})

	result, err := provider.Create(context.Background(), placement.CreateRequest{
		SnapshotRef: DefaultSnapshotID,
		Labels: map[string]string{
			placement.PlacementLabelKey: "lead-placement-1",
			"loom-workspace":            "WS",
		},
		Env: map[string]string{
			"LOOM_WORKSPACE": "WS",
			APIKeyEnv:        "must-not-enter-sandbox",
		},
		Resource:               placement.ResourceSize{VCPU: 1, MemGiB: 2},
		NetworkDomainAllowlist: []string{"github.com", "app.daytona.io"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.SandboxID != "sandbox-1" {
		t.Fatalf("sandbox id = %q, want sandbox-1", result.SandboxID)
	}
	assertRequestString(t, captured, "snapshot", DefaultSnapshotName)
	assertRequestString(t, captured, "domainAllowList", "github.com,app.daytona.io")
	if _, ok := captured["cpu"]; ok {
		t.Fatalf("cpu was sent with a snapshot; Daytona rejects the create: %v", captured)
	}
	if _, ok := captured["memory"]; ok {
		t.Fatalf("memory was sent with a snapshot; Daytona rejects the create: %v", captured)
	}
	env := captured["env"].(map[string]any)
	if _, ok := env[APIKeyEnv]; ok {
		t.Fatalf("%s was sent in create env", APIKeyEnv)
	}
	labels := captured["labels"].(map[string]any)
	if labels[placement.PlacementLabelKey] != "lead-placement-1" || labels["loom-workspace"] != "WS" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestCreateReturnsDecodedValidationBody(t *testing.T) {
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"message":"Provide only one of them"}`), nil
	})

	_, err := provider.Create(context.Background(), placement.CreateRequest{
		SnapshotRef: DefaultSnapshotName,
		Resource:    placement.ResourceSize{VCPU: 1, MemGiB: 2},
	})
	if err == nil || !strings.Contains(err.Error(), "Provide only one of them") {
		t.Fatalf("Create error = %v, want decoded API body", err)
	}
	if errors.Is(err, placement.ErrSandboxNotFound) {
		t.Fatalf("400 was mapped to ErrSandboxNotFound: %v", err)
	}
}

func TestGetMapsNotFoundButNotTransportOrServer(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, `{"message":"Sandbox not found"}`), nil
		})
		_, err := provider.Get(context.Background(), "missing")
		if !errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatalf("Get error = %v, want ErrSandboxNotFound", err)
		}
		var notFound *sdkerrors.DaytonaNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("Get error = %T %[1]v, want DaytonaNotFoundError in chain", err)
		}
	})

	t.Run("server error", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"provider unavailable"}`), nil
		})
		_, err := provider.Get(context.Background(), "sandbox-1")
		if err == nil || errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatalf("Get 500 = %v, want transport/server error distinct from not found", err)
		}
		var serverErr *sdkerrors.DaytonaServerError
		if !errors.As(err, &serverErr) {
			t.Fatalf("Get 500 = %T %[1]v, want DaytonaServerError", err)
		}
	})

	t.Run("transport", func(t *testing.T) {
		provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		})
		_, err := provider.Get(context.Background(), "sandbox-1")
		if err == nil || errors.Is(err, placement.ErrSandboxNotFound) {
			t.Fatalf("Get transport = %v, want non-not-found", err)
		}
	})
}

func TestDeleteMapsAlreadyGoneToNotFound(t *testing.T) {
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", req.Method)
		}
		return jsonResponse(http.StatusNotFound, `{"message":"already gone"}`), nil
	})

	err := provider.Delete(context.Background(), "sandbox-1")
	if !errors.Is(err, placement.ErrSandboxNotFound) {
		t.Fatalf("Delete error = %v, want ErrSandboxNotFound", err)
	}
}

func TestListManagedReturnsAbsentListedSandbox(t *testing.T) {
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/sandbox" {
			t.Fatalf("request = %s %s, want GET /api/sandbox", req.Method, req.URL.Path)
		}
		if got := req.URL.Query().Get("includeErroredDeleted"); got != "true" {
			t.Fatalf("includeErroredDeleted = %q, want true", got)
		}
		var labels map[string]string
		if err := json.Unmarshal([]byte(req.URL.Query().Get("labels")), &labels); err != nil {
			t.Fatalf("decode labels query: %v", err)
		}
		if labels[placement.PlacementLabelKey] != "lead-placement-1" {
			t.Fatalf("labels query = %#v", labels)
		}
		return jsonResponse(http.StatusOK, listBody([]string{"sandbox-1"}, []string{"destroyed"}, []map[string]string{{
			placement.PlacementLabelKey: "lead-placement-1",
			"loom-workspace":            "WS",
		}})), nil
	})

	sandboxes, err := provider.ListManaged(context.Background(), map[string]string{
		placement.PlacementLabelKey: "lead-placement-1",
	})
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}
	if len(sandboxes) != 1 {
		t.Fatalf("sandboxes = %d, want 1", len(sandboxes))
	}
	if sandboxes[0].State != placement.ProviderSandboxAbsent {
		t.Fatalf("state = %q, want absent", sandboxes[0].State)
	}
}

func TestCreatePtyUsesLeadHookEnvAndIgnoresCommand(t *testing.T) {
	var captured map[string]any
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox/sandbox-1":
			return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://daytona.test/toolbox", nil)), nil
		case req.Method == http.MethodPost && req.URL.Path == "/toolbox/sandbox-1/process/pty":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read pty body: %v", err)
			}
			if err := json.Unmarshal(body, &captured); err != nil {
				t.Fatalf("decode pty body: %v", err)
			}
			return jsonResponse(http.StatusOK, `{"sessionId":"lead"}`), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := provider.CreatePty(context.Background(), "sandbox-1", placement.ProcessSpec{
		SessionID:  placement.LeadPTYSessionID,
		Command:    []string{"some", "unsupported", "command", "--prompt", "prompts/lead.md"},
		WorkingDir: "/workspace",
		Env: map[string]string{
			"LOOM_WORKSPACE": "WS",
			APIKeyEnv:        "must-not-enter-pty",
		},
		TTY: true,
	})
	if err != nil {
		t.Fatalf("CreatePty: %v", err)
	}
	if _, ok := captured["command"]; ok {
		t.Fatalf("PTY payload unexpectedly sent a command field: %#v", captured)
	}
	assertRequestString(t, captured, "id", "lead")
	assertRequestString(t, captured, "cwd", "/workspace")
	assertRequestNumber(t, captured, "cols", 120)
	assertRequestNumber(t, captured, "rows", 40)
	env := captured["envs"].(map[string]any)
	if env[leadBootEnv] != "1" {
		t.Fatalf("%s = %v, want 1", leadBootEnv, env[leadBootEnv])
	}
	if env[leadWorkdirEnv] != "/workspace" {
		t.Fatalf("%s = %v, want /workspace", leadWorkdirEnv, env[leadWorkdirEnv])
	}
	if env[leadPromptFileEnv] != "prompts/lead.md" {
		t.Fatalf("%s = %v, want prompt file", leadPromptFileEnv, env[leadPromptFileEnv])
	}
	if _, ok := env[APIKeyEnv]; ok {
		t.Fatalf("%s was sent in PTY env", APIKeyEnv)
	}
}

func TestPtyCreatePayloadStripsLeadHookEnvForNonLeadSession(t *testing.T) {
	payload := ptyCreatePayload(placement.ProcessSpec{
		SessionID:  "probe",
		WorkingDir: "/workspace",
		Command:    []string{"loom", "lead", "--prompt", "/x.md"},
		Env: map[string]string{
			"KEEP":            "yes",
			leadBootEnv:       "1",
			leadWorkdirEnv:    "/wrong",
			leadPromptFileEnv: "/wrong.md",
		},
	})

	if payload.Cwd != "/workspace" {
		t.Fatalf("cwd = %q, want /workspace", payload.Cwd)
	}
	if payload.ID != "probe" {
		t.Fatalf("id = %q, want probe", payload.ID)
	}
	if payload.Envs["KEEP"] != "yes" {
		t.Fatalf("env KEEP = %q, want yes", payload.Envs["KEEP"])
	}
	assertNoLeadHookEnv(t, payload.Envs)
}

func TestPtyCreatePayloadDoesNotTreatUppercaseLeadAsLeadSession(t *testing.T) {
	payload := ptyCreatePayload(placement.ProcessSpec{
		SessionID:  "LEAD",
		WorkingDir: "/workspace",
		Command:    []string{"loom", "lead", "--prompt", "/x.md"},
	})

	if payload.ID != "LEAD" {
		t.Fatalf("id = %q, want LEAD", payload.ID)
	}
	if payload.Cwd != "/workspace" {
		t.Fatalf("cwd = %q, want /workspace", payload.Cwd)
	}
	assertNoLeadHookEnv(t, payload.Envs)
}

func TestPtyCreatePayloadLeadSessionOverridesSmuggledHookEnv(t *testing.T) {
	payload := ptyCreatePayload(placement.ProcessSpec{
		SessionID:  placement.LeadPTYSessionID,
		WorkingDir: " /workspace ",
		Command:    []string{"loom", "lead", "--prompt=/right.md"},
		Env: map[string]string{
			"KEEP":            "yes",
			APIKeyEnv:         "must-not-enter-pty",
			leadBootEnv:       "smuggled",
			leadWorkdirEnv:    "/wrong",
			leadPromptFileEnv: "/wrong.md",
		},
	})

	if payload.Cwd != "/workspace" {
		t.Fatalf("cwd = %q, want /workspace", payload.Cwd)
	}
	if payload.Envs["KEEP"] != "yes" {
		t.Fatalf("env KEEP = %q, want yes", payload.Envs["KEEP"])
	}
	if payload.Envs[leadBootEnv] != "1" {
		t.Fatalf("%s = %q, want 1", leadBootEnv, payload.Envs[leadBootEnv])
	}
	if payload.Envs[leadWorkdirEnv] != "/workspace" {
		t.Fatalf("%s = %q, want /workspace", leadWorkdirEnv, payload.Envs[leadWorkdirEnv])
	}
	if payload.Envs[leadPromptFileEnv] != "/right.md" {
		t.Fatalf("%s = %q, want /right.md", leadPromptFileEnv, payload.Envs[leadPromptFileEnv])
	}
	if _, ok := payload.Envs[APIKeyEnv]; ok {
		t.Fatalf("%s was sent in PTY env", APIKeyEnv)
	}
}

// The broker's default lead command carries no --prompt and may carry no
// working dir, so injection then sets only LOOM_LEAD_BOOT and the other two
// hook keys are protected by the strip alone. This pins the strip for that
// shape; without it, a smuggled LOOM_LEAD_WORKDIR would boot the lead in the
// wrong checkout.
func TestPtyCreatePayloadLeadSessionStripsHookEnvWithoutOverrides(t *testing.T) {
	payload := ptyCreatePayload(placement.ProcessSpec{
		SessionID: placement.LeadPTYSessionID,
		Command:   []string{"loom", "lead"},
		Env: map[string]string{
			leadWorkdirEnv:    "/etc",
			leadPromptFileEnv: "/wrong.md",
		},
	})

	if payload.Envs[leadBootEnv] != "1" {
		t.Fatalf("%s = %q, want 1", leadBootEnv, payload.Envs[leadBootEnv])
	}
	for _, key := range []string{leadWorkdirEnv, leadPromptFileEnv} {
		if value, ok := payload.Envs[key]; ok {
			t.Fatalf("smuggled %s survived = %q", key, value)
		}
	}
}

func TestCreatePtyRejectsEmptySessionIDBeforeHTTP(t *testing.T) {
	var calls int
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		calls++
		t.Fatalf("unexpected HTTP request %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	err := provider.CreatePty(context.Background(), "sandbox-1", placement.ProcessSpec{SessionID: " \t\n "})
	if err == nil || !strings.Contains(err.Error(), "pty session id required") {
		t.Fatalf("CreatePty empty session = %v, want validation error", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP requests = %d, want 0", calls)
	}
}

func TestCreatePtyMapsDuplicateSession(t *testing.T) {
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox/sandbox-1":
			return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://daytona.test/toolbox", nil)), nil
		case req.Method == http.MethodPost && req.URL.Path == "/toolbox/sandbox-1/process/pty":
			return jsonResponse(http.StatusConflict, `{"message":"PTY session lead already exists"}`), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := provider.CreatePty(context.Background(), "sandbox-1", placement.ProcessSpec{SessionID: placement.LeadPTYSessionID})
	if !errors.Is(err, placement.ErrPtySessionAlreadyExists) {
		t.Fatalf("CreatePty duplicate = %v, want ErrPtySessionAlreadyExists", err)
	}
	if errors.Is(err, placement.ErrSandboxNotFound) {
		t.Fatalf("duplicate PTY was mapped to sandbox not found: %v", err)
	}
}

func TestListPtySessionsAndKillUseToolbox(t *testing.T) {
	var sawDelete bool
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox/sandbox-1":
			return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://daytona.test/toolbox", nil)), nil
		case req.Method == http.MethodGet && req.URL.Path == "/toolbox/sandbox-1/process/pty":
			return jsonResponse(http.StatusOK, `{"sessions":[{"id":"lead"},{"id":"debug"}]}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/toolbox/sandbox-1/process/pty/lead":
			sawDelete = true
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	sessions, err := provider.ListPtySessions(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatalf("ListPtySessions: %v", err)
	}
	if !reflect.DeepEqual(sessions, []placement.PtySession{{SessionID: "lead"}, {SessionID: "debug"}}) {
		t.Fatalf("sessions = %#v", sessions)
	}
	if err := provider.KillPtySession(context.Background(), "sandbox-1", "lead"); err != nil {
		t.Fatalf("KillPtySession: %v", err)
	}
	if !sawDelete {
		t.Fatal("DELETE toolbox PTY was not called")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestProvider(t *testing.T, roundTrip roundTripFunc) *Provider {
	t.Helper()
	t.Setenv(APIKeyEnv, "test-token") //nolint:gosec // test credential
	provider, err := New(Config{
		APIURL:                 "https://daytona.test/api",
		HTTPClient:             &http.Client{Transport: roundTrip},
		CreateAutoStopInterval: -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return provider
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func sandboxBody(id, state, toolboxURL string, labels map[string]string) string {
	if labels == nil {
		labels = map[string]string{}
	}
	body, err := json.Marshal(map[string]any{
		"id":              id,
		"organizationId":  "org",
		"name":            id,
		"snapshot":        DefaultSnapshotName,
		"user":            "daytona",
		"env":             map[string]string{},
		"labels":          labels,
		"public":          false,
		"networkBlockAll": false,
		"target":          "us",
		"cpu":             1,
		"gpu":             0,
		"memory":          2,
		"disk":            8,
		"state":           state,
		"toolboxProxyUrl": toolboxURL,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func listBody(ids []string, states []string, labels []map[string]string) string {
	items := make([]map[string]any, len(ids))
	for i := range ids {
		items[i] = map[string]any{
			"id":              ids[i],
			"organizationId":  "org",
			"name":            ids[i],
			"target":          "us",
			"user":            "daytona",
			"public":          false,
			"cpu":             1,
			"gpu":             0,
			"memory":          2,
			"disk":            8,
			"state":           states[i],
			"labels":          labels[i],
			"toolboxProxyUrl": "https://proxy.test/toolbox",
		}
	}
	body, err := json.Marshal(map[string]any{"items": items, "nextCursor": nil})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func assertRequestString(t *testing.T, values map[string]any, key, want string) {
	t.Helper()
	if got, _ := values[key].(string); got != want {
		t.Fatalf("%s = %q, want %q in %#v", key, got, want, values)
	}
}

func assertRequestNumber(t *testing.T, values map[string]any, key string, want float64) {
	t.Helper()
	if got, _ := values[key].(float64); got != want {
		t.Fatalf("%s = %v, want %v in %#v", key, got, want, values)
	}
}

func assertNoLeadHookEnv[V any](t *testing.T, env map[string]V) {
	t.Helper()
	for _, key := range []string{leadBootEnv, leadWorkdirEnv, leadPromptFileEnv} {
		if _, ok := env[key]; ok {
			t.Fatalf("%s was copied into env: %#v", key, env)
		}
	}
}
