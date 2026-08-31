// Package fleetdbcap declares what this loom build needs a fleet-db to serve.
//
// It is one manifest, deliberately: the boot-time preflight
// (internal/runtimepreflight) and the runtime degradation paths must not be
// able to disagree about whether a missing route is fatal or merely lossy.
// A capability that is Degrades here degrades everywhere.
//
// The package is data only — no HTTP, no store, no CLI — so anything may
// import it without dragging a transport along.
//
// GROWTH RULE: add an entry here when loom starts calling a new fleet-db route
// family, and only then. The Capability name must match the name fleet-db
// reports in GET /api/v1/capabilities (its internal/api CapabilityManifest);
// a name that matches nothing fleet-db can ever report would make every
// deployment look incompatible. Choose the Level by asking one question: can
// the daemon do its job at all without this route? If yes, it is Degrades and
// needs a DegradedEffect saying, in one line, what an operator will observe.
package fleetdbcap

// Level says how much a missing capability costs.
type Level int

const (
	// Required means the daemon cannot do its job without the route: the
	// preflight fails and the process exits rather than printing a healthy
	// banner and then failing every spawn.
	Required Level = iota
	// Degrades means a named, bounded loss of function: the degradation is
	// reported on the banner and the daemon starts.
	Degrades
)

// String renders the level for messages and logs.
func (l Level) String() string {
	if l == Degrades {
		return "degrades"
	}
	return "required"
}

// Requirement is one fleet-db route family this loom build calls.
type Requirement struct {
	// Capability is the name fleet-db reports for the route family.
	Capability string
	// Feature is the loom feature that calls it, named the way an operator
	// would name it — this is what "needed by" prints.
	Feature string
	// Level says whether absence is fatal or merely degrading.
	Level Level
	// Route is the human-readable method + path, for the boot message. It is
	// documentation, never used to build a request.
	Route string
	// DegradedEffect describes what an operator will observe when a Degrades
	// capability is absent. Required for Degrades, empty for Required.
	DegradedEffect string
}

// Requirements returns the fleet-db capabilities this loom build needs.
//
// The returned slice is a fresh copy: callers (tests especially) may sort or
// filter it without mutating the manifest.
func Requirements() []Requirement {
	return append([]Requirement(nil), requirements...)
}

var requirements = []Requirement{
	{
		Capability: "issues",
		Feature:    "task claim and status transitions",
		Level:      Required,
		Route:      "GET  /api/v1/{workspace}/issues",
	},
	{
		Capability: "agents",
		Feature:    "agent registration and supervision",
		Level:      Required,
		Route:      "GET  /api/v1/{workspace}/agents",
	},
	{
		Capability: "agent-leases",
		Feature:    "single-owner agent leasing",
		Level:      Required,
		Route:      "GET  /api/v1/{workspace}/agent-leases",
	},
	{
		Capability: "skills",
		Feature:    "skill materialization",
		Level:      Required,
		Route:      "GET  /api/v1/{workspace}/skills",
	},
	{
		Capability:     "skill-materialization-leases",
		Feature:        "concurrent skill materialization",
		Level:          Degrades,
		Route:          "POST /api/v1/{workspace}/skill-materialization-leases",
		DegradedEffect: "skill materialization runs unlocked; concurrent spawns on one host may redo work",
	},
}
