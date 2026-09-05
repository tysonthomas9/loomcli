// Package spawnmetrics holds the daemon-side spawn outcome counters and the
// on-disk snapshot the serve-side collector reads them from.
//
// It is linked into the daemon binary, so it stays dependency-light: no
// prometheus client here, and no dependency on internal/cli — callers resolve
// the runtime directory themselves and pass a plain path in.
package spawnmetrics

// Class is the bounded set of spawn failure reasons. It exists to keep metric
// label cardinality finite: an error *message* must never reach a label.
type Class string

const (
	// ClassBackendUnavailable covers a spawn refused because the agent backend
	// could not be reached.
	ClassBackendUnavailable Class = "backend_unavailable"
	// ClassMaterializeSkills covers a failure to materialize an agent's skills.
	ClassMaterializeSkills Class = "materialize_skills"
	// ClassBuildCommand covers a failure to build the agent's command line.
	ClassBuildCommand Class = "build_command"
	// ClassStart covers a failure to start the agent process itself.
	ClassStart Class = "start"
	// ClassUnknown is what Normalize returns for anything outside the allowlist.
	ClassUnknown Class = "unknown"
	// ClassNone is the empty class carried by the success row.
	ClassNone Class = ""
)

// classAllowlist is the cardinality guard. Normalize returns ClassUnknown for
// every value absent from it, so a raw string can never become a label.
var classAllowlist = map[Class]bool{
	ClassBackendUnavailable: true,
	ClassMaterializeSkills:  true,
	ClassBuildCommand:       true,
	ClassStart:              true,
	ClassUnknown:            true,
	ClassNone:               true,
}

// Normalize maps an arbitrary string onto the allowlist, returning ClassUnknown
// for anything it does not recognize.
func Normalize(s string) Class {
	c := Class(s)
	if classAllowlist[c] {
		return c
	}
	return ClassUnknown
}

// SpanReason renders the class as the span status string used at the spawn call
// sites, so those literals are generated from the enum rather than duplicated.
func (c Class) SpanReason() string {
	return "spawn." + string(c)
}
