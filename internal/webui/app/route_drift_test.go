package app

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/appstores"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/route"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// newMaximalServer builds a Server with every optional dependency satisfied, so
// that buildModules produces all modules and registerRoutes registers every
// conditional route. The drift test is only meaningful against a maximal
// server: any dependency left nil silently drops routes, which then show up as
// phantom "declared but not served" entries.
func newMaximalServer(t *testing.T) *Server {
	t.Helper()

	t.Setenv("LOOM_WORKER_TOKEN", "drift-test")

	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(func() { hub.Stop() })

	hf := func(http.ResponseWriter, *http.Request) {}

	app := &Server{}
	app.hub = hub
	app.termSvc = &stubTerminalService{}
	app.issueTabStore = issuetabs.NewStore(nil, nil)
	app.fleetRegistry = &fleet.StoreRegistry{}
	app.diffSvc = &stubDiffService{}
	app.fileSvc = &stubFileService{}
	app.claimMetrics = fleet.NewClaimMetrics()
	app.config.Store = memstore.New()
	app.config.LocalSettingsDir = t.TempDir()
	// A URL is enough to register the auth proxy mount; it is never dialed.
	app.config.ExtAuthURL = "http://127.0.0.1:9999"
	app.config.MonitorHandlers = webui.MonitorHandlers{
		Status: hf, Agents: hf, Tasks: hf, Stats: hf, Sync: hf,
		Workspaces: hf, StaleDetector: hf, Usage: hf, Metrics: hf,
		ObservabilityMetrics: hf, ObservabilityEvents: hf,
	}
	// Non-nil or the workspace middleware panics.
	app.wsExistsFn = func(string) bool { return true }
	app.mux = route.NewRecorder()
	// Non-nil or registerWorkspaceRoutes is skipped entirely.
	app.multiPool = daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	t.Cleanup(func() { _ = app.multiPool.Close() })

	app.buildHandlers()
	t.Cleanup(func() {
		app.handlers.ClientErrLimiter.Stop()
		app.handlers.AuthCfgLimiter.Stop()
	})
	// buildHandlers overwrites the whole struct, so these go after it.
	app.handlers.DaemonSupervisor = hf
	app.handlers.DaemonConfig = hf
	app.handlers.GetBackendsHealth = hf
	app.handlers.NotifySessionChange = hf

	termAuth, err := appstores.NewTerminalAuth()
	if err != nil {
		t.Fatalf("appstores.NewTerminalAuth: %v", err)
	}
	t.Cleanup(termAuth.Stop)
	app.termAuth = termAuth
	app.ptyMgr = terminal.NewMultiPTYManager("/bin/sh", 1)

	// Deliberately the ZERO VALUE, not terminal.NewAgentTmuxManager(): the
	// constructor returns ErrTmuxNotFound when the tmux binary is absent, and no
	// CI workflow installs tmux. terminal.Module.Register only checks this field
	// for nil, so the zero value registers the same routes and never execs tmux.
	// This line looks deletable. It is not.
	tmuxMgr := &terminal.AgentTmuxManager{}
	app.agentTmuxMgr = tmuxMgr

	app.agentSvc = svcimpl.NewAgentService(nil, tmuxMgr, termAuth, app.config.Store)
	app.issueSvc = service.NewIssueService(nil, app.multiPool, middleware.WithWorkspace)

	app.buildModules()
	app.registerRoutes()
	// registerWorkerAPIRoutes is called from NewServer, not registerRoutes, and
	// returns early when LOOM_WORKER_TOKEN is unset. Both are needed or the whole
	// /api/internal/workers/ family is missing.
	app.registerWorkerAPIRoutes()

	return app
}

// ── Drift gate ───────────────────────────────────────────────────────────────
//
// TestOpenAPIRouteDrift compares the routes a maximal server registers against
// the operations api/openapi.yaml declares, in BOTH directions, and fails on any
// difference that api/route-drift-allowlist.yaml does not excuse.
//
// The spec had drifted for five months before this gate existed. The reason it
// went unnoticed is instructive and shapes the rules below: the only other check
// (scripts/check-go-api-staleness.sh) SKIPS when it cannot do its job. This test
// never skips. A missing or unparseable spec is a failure.

const (
	// routeDriftSpecPath is relative to this package directory.
	routeDriftSpecPath = "../../../api/openapi.yaml"
	// routeDriftAllowlistPath is likewise relative; it is also the name quoted
	// in every failure message, so keep the two in sync.
	routeDriftAllowlistPath = "../../../api/route-drift-allowlist.yaml"
	routeDriftAllowlistName = "api/route-drift-allowlist.yaml"
	routeDriftAPIPrefix     = "/api/"
)

// routeDriftHTTPMethods is the set of OpenAPI path-item keys that are
// operations. Everything else under a path (parameters, summary, $ref, ...) is
// not.
var routeDriftHTTPMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// routeDriftMount describes one method-less pattern — a mount rather than an
// operation. Go's ServeMux treats `/api/foo/` as a subtree prefix, and whether
// that subtree "covers" the paths beneath it differs per mount, so the answer is
// spelled out per entry rather than defaulted. A silent default here is exactly
// how the previous drift went unnoticed for five months, which is why an
// unrecognized method-less pattern is a hard failure rather than a guess.
type routeDriftMount struct {
	pattern  string
	covering bool
	why      string
}

var routeDriftMounts = []routeDriftMount{
	{
		pattern:  "/api/",
		covering: false,
		why: "JSON-404 sink registered last (routes.go). It serves nothing; if it " +
			"were treated as covering, every declared operation would look served " +
			"and the declared_not_served direction would be permanently inert.",
	},
	{
		pattern:  "/api/workspaces/{ws}/",
		covering: false,
		why: "dispatches into wsMux. The routes beneath it are recorded separately " +
			"by wsMuxRec and appear in registeredRoutes() in their own right, so " +
			"treating the mount as covering would mask a missing workspace route.",
	},
	{
		pattern:  "/api/auth/",
		covering: true,
		why: "reverse proxy to the external BetterAuth service (registerAuthProxy). " +
			"Everything beneath it is genuinely served by the proxy; without this " +
			"rule the declared GET /api/auth/token is a false declared_not_served.",
	},
	{
		pattern:  "/api/internal/workers/",
		covering: true,
		why: "token-authed worker sub-mux; every path beneath it is served. The " +
			"family is intentionally undocumented and, because this mount covers " +
			"it, needs no entry in " + routeDriftAllowlistName + " — an entry there " +
			"would be reported as a no-op by TestRouteDriftAllowlistIsClean.",
	},
}

// routeDriftNonAPIDeclared lists the operations that are declared AND served but
// live outside the /api/ prefix this gate scopes itself to. They are named
// explicitly so that "out of scope" is a decision recorded in one place rather
// than an accident of the prefix filter.
var routeDriftNonAPIDeclared = map[string]string{
	"GET /health":  "liveness probe, served at the root by design",
	"GET /metrics": "Prometheus scrape endpoint, served at the root by design",
}

// normalizeRouteWildcards rewrites every {wildcard} to a bare {} so that the
// spec's {issueId} and the mux's {id} compare equal. It also handles the
// multi-segment {path...} form with no special-casing.
func normalizeRouteWildcards(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			b.WriteByte(p[i])
			continue
		}
		end := strings.IndexByte(p[i:], '}')
		if end < 0 {
			// Unbalanced brace: leave the rest verbatim so the mismatch is visible
			// rather than silently swallowed.
			b.WriteString(p[i:])
			return b.String()
		}
		b.WriteString("{}")
		i += end
	}
	return b.String()
}

// routeDriftServed splits the registered patterns into served operations (keyed
// by normalized "METHOD /path") and the covering mount prefixes. It fails the
// test on any pattern shape the rules above do not account for.
func routeDriftServed(t *testing.T, patterns []string) (map[string]string, []string) {
	t.Helper()

	mountByPattern := make(map[string]routeDriftMount, len(routeDriftMounts))
	for _, m := range routeDriftMounts {
		mountByPattern[m.pattern] = m
	}

	served := make(map[string]string, len(patterns))
	var covering []string

	for _, raw := range patterns {
		method, path := "", raw
		if i := strings.IndexByte(raw, ' '); i >= 0 {
			method, path = raw[:i], strings.TrimSpace(raw[i+1:])
		}

		// Host-prefixed patterns ("example.com/api/x"): none exist today. Strip
		// the host if one ever appears so the comparison stays path-based.
		if !strings.HasPrefix(path, "/") {
			if i := strings.IndexByte(path, '/'); i >= 0 {
				path = path[i:]
			}
		}

		// {$} is Go 1.22's exact-match wildcard. None exist today and the
		// normalization above would quietly turn one into a path parameter, so
		// refuse rather than mis-compare.
		if strings.Contains(path, "{$}") {
			t.Fatalf("route %q uses the {$} exact-match wildcard, which this drift "+
				"gate does not model. Teach routeDriftServed how {$} should compare "+
				"against the spec before registering it.", raw)
		}

		if method == "" {
			m, known := mountByPattern[path]
			if !known {
				t.Fatalf("method-less mount %q is not in routeDriftMounts. Every "+
					"subtree mount must be classified explicitly as covering or not — "+
					"see the table in route_drift_test.go. Add it with a reason; do not "+
					"let it default.", path)
			}
			if m.covering {
				covering = append(covering, normalizeRouteWildcards(path))
			}
			continue
		}

		if !strings.HasPrefix(path, routeDriftAPIPrefix) {
			// Out of scope by the explicit rule above. Anything outside /api/ that
			// is not on that list is a new root-level route: name it there.
			key := method + " " + path
			if _, ok := routeDriftNonAPIDeclared[key]; !ok {
				t.Fatalf("route %q is served outside the %s scope of this gate and is "+
					"not one of the explicitly excluded root routes. Add it to "+
					"routeDriftNonAPIDeclared with a reason, or move it under %s.",
					key, routeDriftAPIPrefix, routeDriftAPIPrefix)
			}
			continue
		}

		served[method+" "+normalizeRouteWildcards(path)] = method + " " + path
	}

	return served, covering
}

// routeDriftDeclared parses api/openapi.yaml and returns its /api/ operations
// keyed by normalized "METHOD /path".
func routeDriftDeclared(t *testing.T) map[string]string {
	t.Helper()

	raw, err := os.ReadFile(routeDriftSpecPath)
	if err != nil {
		t.Fatalf("read %s: %v (the drift gate never skips: a spec it cannot read is "+
			"a failure, not a pass)", routeDriftSpecPath, err)
	}

	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse %s: %v", routeDriftSpecPath, err)
	}
	if len(spec.Paths) == 0 {
		t.Fatalf("%s declares no paths — the spec is empty or its shape changed",
			routeDriftSpecPath)
	}

	declared := make(map[string]string, len(spec.Paths)*2)
	for path, item := range spec.Paths {
		for key := range item {
			method := strings.ToUpper(key)
			if !routeDriftHTTPMethods[strings.ToLower(key)] {
				continue
			}
			full := method + " " + path
			if !strings.HasPrefix(path, routeDriftAPIPrefix) {
				if _, ok := routeDriftNonAPIDeclared[full]; !ok {
					t.Fatalf("%s declares %q outside the %s scope of this gate. Add it "+
						"to routeDriftNonAPIDeclared with a reason if it is meant to live "+
						"at the root.", routeDriftSpecPath, full, routeDriftAPIPrefix)
				}
				continue
			}
			declared[method+" "+normalizeRouteWildcards(path)] = full
		}
	}
	return declared
}

// routeDriftAllowlistEntry is one `{ route, reason }` pair.
type routeDriftAllowlistEntry struct {
	Route  string `yaml:"route"`
	Reason string `yaml:"reason"`
}

type routeDriftAllowlist struct {
	Unserved   []routeDriftAllowlistEntry `yaml:"unserved"`
	Undeclared []routeDriftAllowlistEntry `yaml:"undeclared"`
}

func loadRouteDriftAllowlist(t *testing.T) routeDriftAllowlist {
	t.Helper()

	raw, err := os.ReadFile(routeDriftAllowlistPath)
	if err != nil {
		t.Fatalf("read %s: %v", routeDriftAllowlistName, err)
	}
	var al routeDriftAllowlist
	if err := yaml.Unmarshal(raw, &al); err != nil {
		t.Fatalf("parse %s: %v", routeDriftAllowlistName, err)
	}
	return al
}

// normalizedRouteKey turns an allowlist entry's "METHOD /path" into the same
// normalized key the two sets above use, so a later wildcard rename does not
// make the allowlist stale.
func normalizedRouteKey(t *testing.T, route string) string {
	t.Helper()
	i := strings.IndexByte(route, ' ')
	if i < 0 {
		t.Fatalf("%s: entry %q is not of the form \"METHOD /path\"",
			routeDriftAllowlistName, route)
	}
	method := strings.ToUpper(strings.TrimSpace(route[:i]))
	path := strings.TrimSpace(route[i+1:])
	return method + " " + normalizeRouteWildcards(path)
}

func routeDriftAllowlistKeys(t *testing.T, entries []routeDriftAllowlistEntry) map[string]bool {
	t.Helper()
	keys := make(map[string]bool, len(entries))
	for _, e := range entries {
		keys[normalizedRouteKey(t, e.Route)] = true
	}
	return keys
}

// coveredByMount reports whether a normalized path sits beneath a covering
// mount prefix.
func coveredByMount(key string, covering []string) bool {
	i := strings.IndexByte(key, ' ')
	if i < 0 {
		return false
	}
	path := key[i+1:]
	for _, prefix := range covering {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func TestOpenAPIRouteDrift(t *testing.T) {
	app := newMaximalServer(t)
	served, covering := routeDriftServed(t, app.registeredRoutes())
	declared := routeDriftDeclared(t)
	allow := loadRouteDriftAllowlist(t)

	t.Run("declared_not_served", func(t *testing.T) {
		allowed := routeDriftAllowlistKeys(t, allow.Unserved)

		var offenders []string
		for key, original := range declared {
			if _, ok := served[key]; ok {
				continue
			}
			if coveredByMount(key, covering) {
				continue
			}
			if allowed[key] {
				continue
			}
			offenders = append(offenders, original)
		}
		sort.Strings(offenders)

		if len(offenders) > 0 {
			t.Errorf("%d operation(s) are declared in %s but no route serves them:\n  %s\n\n"+
				"Either implement them, remove them from the spec, or — if the gap is "+
				"deliberate — add each one under `unserved:` in %s with a reason.",
				len(offenders), routeDriftSpecPath, strings.Join(offenders, "\n  "),
				routeDriftAllowlistName)
		}
	})

	t.Run("served_not_declared", func(t *testing.T) {
		allowed := routeDriftAllowlistKeys(t, allow.Undeclared)

		var offenders []string
		for key, original := range served {
			if _, ok := declared[key]; ok {
				continue
			}
			if allowed[key] {
				continue
			}
			offenders = append(offenders, original)
		}
		sort.Strings(offenders)

		if len(offenders) > 0 {
			t.Errorf("%d route(s) are served but not declared in %s:\n  %s\n\n"+
				"Either document them in the spec, or — if they are internal — add each "+
				"one under `undeclared:` in %s with a reason.",
				len(offenders), routeDriftSpecPath, strings.Join(offenders, "\n  "),
				routeDriftAllowlistName)
		}
	})
}

// TestRouteDriftFixtureIsMaximal is the ratchet that keeps the served_not_declared
// direction honest. Every optional dependency newMaximalServer forgets silently
// removes routes from the comparison, so the gate would go quiet rather than red.
func TestRouteDriftFixtureIsMaximal(t *testing.T) {
	const wantModules = 20
	const hint = "the maximal-server fixture is missing a dependency, or a new module " +
		"was added to buildModules — see §3 of PUPPET-501"

	app := newMaximalServer(t)

	if got := len(app.wsModules); got != wantModules {
		t.Errorf("len(wsModules) = %d, want %d: %s", got, wantModules, hint)
	}

	// A handful of routes that only exist when a specific optional dependency is
	// wired up. Each is the canary for a different one.
	wantRoutes := []string{
		"GET /api/daemon/supervisor",              // handlers.DaemonSupervisor
		"GET /api/monitor/status",                 // config.MonitorHandlers
		"GET /api/local/settings",                 // config.LocalSettingsDir
		"GET /api/workspaces/{ws}/agents",         // agentSvc + config.Store
		"GET /api/workspaces/{ws}/terminal/token", // termAuth
		"POST /api/internal/workers/register",     // LOOM_WORKER_TOKEN + registerWorkerAPIRoutes
	}

	registered := make(map[string]bool)
	for _, r := range app.registeredRoutes() {
		registered[normalizeRouteWildcards(r)] = true
	}

	var missing []string
	for _, want := range wantRoutes {
		if !registered[normalizeRouteWildcards(want)] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Errorf("conditional route(s) not registered:\n  %s\n\n%s",
			strings.Join(missing, "\n  "), hint)
	}
}

// TestRouteDriftAllowlistIsClean keeps api/route-drift-allowlist.yaml from
// rotting. It is what makes documenting a route safe: the moment an operation
// lands in the spec, its allowlist entry becomes a contradiction and this test
// says so.
func TestRouteDriftAllowlistIsClean(t *testing.T) {
	app := newMaximalServer(t)
	served, _ := routeDriftServed(t, app.registeredRoutes())
	declared := routeDriftDeclared(t)
	allow := loadRouteDriftAllowlist(t)

	check := func(kind string, entries []routeDriftAllowlistEntry) {
		seen := make(map[string]bool, len(entries))
		for _, e := range entries {
			if strings.TrimSpace(e.Reason) == "" {
				t.Errorf("%s: %s entry %q has an empty reason. Every entry must say why "+
					"the gap is acceptable.", routeDriftAllowlistName, kind, e.Route)
			}
			key := normalizedRouteKey(t, e.Route)
			if seen[key] {
				t.Errorf("%s: %s entry %q is listed twice.",
					routeDriftAllowlistName, kind, e.Route)
			}
			seen[key] = true

			_, isServed := served[key]
			_, isDeclared := declared[key]

			if !isServed && !isDeclared {
				t.Errorf("%s: %s entry %q is stale — it matches no registered route and "+
					"no declared operation. Delete it.",
					routeDriftAllowlistName, kind, e.Route)
				continue
			}

			switch kind {
			case "undeclared":
				if isDeclared {
					t.Errorf("%s: %q is listed under `undeclared:` but IS declared in %s. "+
						"Pick one: if the spec now documents it, delete the allowlist entry.",
						routeDriftAllowlistName, e.Route, routeDriftSpecPath)
				}
				if !isServed {
					t.Errorf("%s: %q is listed under `undeclared:` but no route serves it. "+
						"Delete it.", routeDriftAllowlistName, e.Route)
				}
			case "unserved":
				if isServed {
					t.Errorf("%s: %q is listed under `unserved:` but IS served. "+
						"Delete the allowlist entry.", routeDriftAllowlistName, e.Route)
				}
				if !isDeclared {
					t.Errorf("%s: %q is listed under `unserved:` but the spec does not "+
						"declare it. Delete it.", routeDriftAllowlistName, e.Route)
				}
			}
		}
	}

	check("unserved", allow.Unserved)
	check("undeclared", allow.Undeclared)
}
