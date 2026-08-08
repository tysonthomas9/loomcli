package daytona

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestProviderSandboxCreatedAtIsParsed(t *testing.T) {
	createdAt := "2026-01-02T03:04:05Z"
	want, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		t.Fatalf("parse test time: %v", err)
	}
	provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox/sandbox-1":
			return jsonResponse(http.StatusOK, sandboxBodyWithCreatedAt("sandbox-1", "started", "https://daytona.test/toolbox", map[string]string{
				placement.PlacementLabelKey: "lead-placement-1",
			}, createdAt)), nil
		case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox":
			return jsonResponse(http.StatusOK, listBodyWithCreatedAt([]string{"sandbox-1"}, []string{"started"}, []map[string]string{{
				placement.PlacementLabelKey: "lead-placement-1",
			}}, []string{createdAt})), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	got, err := provider.Get(context.Background(), "sandbox-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.CreatedAt.Equal(want.UTC()) {
		t.Fatalf("Get CreatedAt = %s, want %s", got.CreatedAt, want.UTC())
	}
	listed, err := provider.ListManaged(context.Background(), map[string]string{
		placement.PlacementLabelKey: "lead-placement-1",
	})
	if err != nil {
		t.Fatalf("ListManaged: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %d, want 1", len(listed))
	}
	if !listed[0].CreatedAt.Equal(want.UTC()) {
		t.Fatalf("ListManaged CreatedAt = %s, want %s", listed[0].CreatedAt, want.UTC())
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

func TestPrepareLeadBootExecTimeoutExitCodeAndSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "missing exit code", body: `{}`, wantErr: "no exitCode"},
		{name: "nonzero exit code", body: `{"exitCode":2,"stderr":"bad path"}`, wantErr: "exit code 2"},
		{name: "success", body: `{"exitCode":0,"result":"ok"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured toolboxExecuteRequest
			provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox/sandbox-1":
					return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://daytona.test/toolbox", nil)), nil
				case req.Method == http.MethodPost && req.URL.Path == "/toolbox/sandbox-1/process/execute":
					captured = decodeExecuteRequest(t, req)
					return jsonResponse(http.StatusOK, tc.body), nil
				default:
					t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			})

			err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
				PromptPath: "/tmp/loom-lead-prompt.md",
				PromptText: "hello",
			})
			// The exec timeout derives from the remaining context budget, so
			// it is the prep budget minus elapsed time -- never the toolbox's
			// 10-second default.
			budget := int(defaultLeadBootPrepTimeout / time.Second)
			if captured.Timeout <= budget-30 || captured.Timeout > budget {
				t.Fatalf("timeout = %d, want within (%d, %d]", captured.Timeout, budget-30, budget)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("PrepareLeadBoot: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("PrepareLeadBoot = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestPrepareLeadBootIdempotentCheckoutSkipsClone(t *testing.T) {
	var commands []string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		commands = append(commands, req.Command)
		if len(commands) > 1 {
			t.Fatalf("unexpected extra exec command: %s", req.Command)
		}
		return executeBody(0, "git", "")
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Repo: &placement.RepoClone{
			Name:      "repo",
			RemoteURL: "https://github.com/o/r",
			Checkout:  "/root/workspace/repo",
		},
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "rev-parse --is-inside-work-tree") {
		t.Fatalf("commands = %v, want only rev-parse idempotency check", commands)
	}
}

func TestPrepareLeadBootCloneNormalizesSSHRemote(t *testing.T) {
	var commands []string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		commands = append(commands, req.Command)
		switch len(commands) {
		case 1:
			return executeBody(0, "absent", "")
		case 2:
			return executeBody(0, "", "")
		case 3:
			return executeBody(0, "https://github.com/o/r", "")
		default:
			t.Fatalf("unexpected exec command %d: %s", len(commands), req.Command)
			return ""
		}
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Repo: &placement.RepoClone{
			Name:      "repo",
			RemoteURL: "git@github.com:o/r.git",
			Ref:       "main",
			Checkout:  "/root/workspace/repo",
		},
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	clone := commands[1]
	for _, want := range []string{
		"git clone --depth 1 --single-branch",
		"--branch 'main'",
		"'https://github.com/o/r'",
		// Atomicity: stage into .partial and rename into place, so a killed
		// clone leaves nothing at the checkout path.
		"rm -rf '/root/workspace/repo.partial'",
		"'/root/workspace/repo.partial' && mv '/root/workspace/repo.partial' '/root/workspace/repo'",
	} {
		if !strings.Contains(clone, want) {
			t.Fatalf("clone command %q missing %q", clone, want)
		}
	}
	if strings.Contains(clone, "git@github.com") {
		t.Fatalf("clone command was not normalized: %s", clone)
	}
}

func TestPrepareLeadBootRejectsUnsupportedRemoteBeforeExec(t *testing.T) {
	for _, remote := range []string{"http://github.com/o/r", "ssh://github.com/o/r"} {
		t.Run(remote, func(t *testing.T) {
			var calls int
			provider := newTestProvider(t, func(req *http.Request) (*http.Response, error) {
				calls++
				t.Fatalf("unexpected HTTP request for invalid remote: %s %s", req.Method, req.URL.Path)
				return nil, nil
			})

			err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
				Repo: &placement.RepoClone{RemoteURL: remote, Checkout: "/root/workspace/repo"},
			})
			if err == nil {
				t.Fatal("PrepareLeadBoot succeeded, want invalid remote error")
			}
			if calls != 0 {
				t.Fatalf("HTTP calls = %d, want none", calls)
			}
		})
	}
}

func TestPrepareLeadBootTokenCloneRedactsErrors(t *testing.T) {
	const token = "ghp_SECRET"
	var cloneCommand string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		switch {
		case strings.Contains(req.Command, "rev-parse"):
			return executeBody(0, "absent", "")
		case strings.Contains(req.Command, " clone "):
			cloneCommand = req.Command
			return executeBody(2, "", "fatal: "+req.Command+" token "+token)
		default:
			t.Fatalf("unexpected exec command: %s", req.Command)
			return ""
		}
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Repo: &placement.RepoClone{
			Name:      "repo",
			RemoteURL: "https://github.com/o/r",
			Checkout:  "/root/workspace/repo",
		},
		GitToken: func() (string, error) { return token, nil },
	})
	if err == nil {
		t.Fatal("PrepareLeadBoot succeeded, want clone error")
	}
	if !strings.Contains(cloneCommand, "http.https://github.com/.extraheader=AUTHORIZATION: basic ") {
		t.Fatalf("clone command missing host-derived extraheader: %s", cloneCommand)
	}
	for _, leaked := range []string{token, cloneCommand, "AUTHORIZATION: basic"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error leaked %q: %v", leaked, err)
		}
	}
}

func TestPrepareLeadBootNilGitTokenClonesWithoutExtraHeader(t *testing.T) {
	var clone string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		switch {
		case strings.Contains(req.Command, "rev-parse"):
			return executeBody(0, "absent", "")
		case strings.Contains(req.Command, " clone "):
			clone = req.Command
			return executeBody(0, "", "")
		case strings.Contains(req.Command, "remote.origin.url"):
			return executeBody(0, "https://github.com/o/r", "")
		default:
			t.Fatalf("unexpected exec command: %s", req.Command)
			return ""
		}
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Repo: &placement.RepoClone{RemoteURL: "https://github.com/o/r", Checkout: "/root/workspace/repo"},
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	if strings.Contains(clone, "-c 'http.") {
		t.Fatalf("unauthenticated clone carried http extraheader: %s", clone)
	}
}

func TestPrepareLeadBootPromptWriteDecodesExactText(t *testing.T) {
	const prompt = "line one\nline 'two'\n"
	var command string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		command = req.Command
		return executeBody(0, "", "")
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		PromptPath: "/tmp/loom-lead-prompt.md",
		PromptText: prompt,
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	encoded := between(t, command, "printf %s '", "' | base64 -d")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode prompt payload: %v", err)
	}
	if string(decoded) != prompt {
		t.Fatalf("decoded prompt = %q, want %q", decoded, prompt)
	}
	// Atomicity: write to .tmp then rename, so a lead mid-read of the prompt
	// file never observes a truncated file.
	if !strings.Contains(command, "> '/tmp/loom-lead-prompt.md.tmp' && mv -f '/tmp/loom-lead-prompt.md.tmp' '/tmp/loom-lead-prompt.md'") {
		t.Fatalf("prompt write is not write-then-rename: %s", command)
	}
}

func TestPrepareLeadBootSeedsFilesAtomicallyWithModeAndRedaction(t *testing.T) {
	content := `{"tokens":{"access":"SECRET-TOKEN-VALUE"}}`
	var command string
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		command = req.Command
		return executeBody(0, "", "")
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Files: []placement.SandboxFile{{Path: "/root/.codex/auth.json", Content: []byte(content), Mode: "600"}},
	})
	if err != nil {
		t.Fatalf("PrepareLeadBoot: %v", err)
	}
	encoded := between(t, command, "printf %s '", "' | base64 -d")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != content {
		t.Fatalf("decoded seed file = %q, %v, want original content", decoded, err)
	}
	for _, want := range []string{
		"> '/root/.codex/auth.json.tmp'",
		"chmod 600 '/root/.codex/auth.json.tmp'",
		"mv -f '/root/.codex/auth.json.tmp' '/root/.codex/auth.json'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("seed command %q missing %q", command, want)
		}
	}

	// Failure path: neither the raw content nor its base64 may leak.
	failing := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		return executeBody(1, "", "wrote "+base64.StdEncoding.EncodeToString([]byte(content))+" badly")
	})
	err = failing.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Files: []placement.SandboxFile{{Path: "/root/.codex/auth.json", Content: []byte(content)}},
	})
	if err == nil {
		t.Fatal("PrepareLeadBoot = nil error, want seed failure")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN-VALUE") ||
		strings.Contains(err.Error(), base64.StdEncoding.EncodeToString([]byte(content))) {
		t.Fatalf("seed failure leaked file content: %v", err)
	}

	for _, bad := range []placement.SandboxFile{
		{Path: "relative/auth.json", Content: []byte("x")},
		{Path: "", Content: []byte("x")},
		{Path: "/root/x", Content: []byte("x"), Mode: "rw-"},
		{Path: "/root/x", Content: []byte("x"), Mode: "60000"},
	} {
		err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{Files: []placement.SandboxFile{bad}})
		if err == nil {
			t.Fatalf("PrepareLeadBoot(%+v) = nil error, want validation error", bad)
		}
	}
}

func TestPrepareLeadBootRejectsCredentialBearingPersistedRemote(t *testing.T) {
	provider := newPrepProvider(t, func(req toolboxExecuteRequest) string {
		switch {
		case strings.Contains(req.Command, "rev-parse"):
			return executeBody(0, "absent", "")
		case strings.Contains(req.Command, " clone "):
			return executeBody(0, "", "")
		case strings.Contains(req.Command, "remote.origin.url"):
			return executeBody(0, "https://x-access-token@github.com/o/r", "")
		default:
			t.Fatalf("unexpected exec command: %s", req.Command)
			return ""
		}
	})

	err := provider.PrepareLeadBoot(context.Background(), "sandbox-1", placement.LeadBootPrep{
		Repo: &placement.RepoClone{RemoteURL: "https://github.com/o/r", Checkout: "/root/workspace/repo"},
	})
	if err == nil || !strings.Contains(err.Error(), "credential-bearing") {
		t.Fatalf("PrepareLeadBoot = %v, want credential persistence error", err)
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

func newPrepProvider(t *testing.T, execute func(toolboxExecuteRequest) string) *Provider {
	t.Helper()
	return newTestProvider(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/sandbox/sandbox-1":
			return jsonResponse(http.StatusOK, sandboxBody("sandbox-1", "started", "https://daytona.test/toolbox", nil)), nil
		case req.Method == http.MethodPost && req.URL.Path == "/toolbox/sandbox-1/process/execute":
			return jsonResponse(http.StatusOK, execute(decodeExecuteRequest(t, req))), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})
}

func decodeExecuteRequest(t *testing.T, req *http.Request) toolboxExecuteRequest {
	t.Helper()
	var payload toolboxExecuteRequest
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read execute body: %v", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode execute body: %v", err)
	}
	return payload
}

func executeBody(exitCode int, result, stderr string) string {
	body, err := json.Marshal(map[string]any{
		"exitCode": exitCode,
		"result":   result,
		"stderr":   stderr,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func between(t *testing.T, value, prefix, suffix string) string {
	t.Helper()
	start := strings.Index(value, prefix)
	if start < 0 {
		t.Fatalf("%q missing prefix %q", value, prefix)
	}
	start += len(prefix)
	end := strings.Index(value[start:], suffix)
	if end < 0 {
		t.Fatalf("%q missing suffix %q", value, suffix)
	}
	return value[start : start+end]
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
	return sandboxBodyWithCreatedAt(id, state, toolboxURL, labels, "")
}

func sandboxBodyWithCreatedAt(id, state, toolboxURL string, labels map[string]string, createdAt string) string {
	if labels == nil {
		labels = map[string]string{}
	}
	values := map[string]any{
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
	}
	if createdAt != "" {
		values["createdAt"] = createdAt
	}
	body, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func listBody(ids []string, states []string, labels []map[string]string) string {
	return listBodyWithCreatedAt(ids, states, labels, nil)
}

func listBodyWithCreatedAt(ids []string, states []string, labels []map[string]string, createdAt []string) string {
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
		if len(createdAt) > i && createdAt[i] != "" {
			items[i]["createdAt"] = createdAt[i]
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
