package fleet

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// fleet-db strict-decodes update bodies (disallowUnknownFields), so a single
// field loom sends that its UpdateIssueRequest does not declare fails the WHOLE
// PATCH — every other field in the same request is silently lost. That is not
// hypothetical: `design_format` (params.go, added by #150) is absent from
// fleet-db's UpdateIssueRequest, so a Planner writing its design lost the design
// AND the accompanying fields, ending `has_design: false` with no user-visible
// error (tests/aft/FINDINGS.md §1.13, confirmed against a live backend).
//
// This mirrors createWithoutExternalRef's approach on the create path: when the
// server names an unsupported field, drop exactly that field and retry, so the
// rest of the update still lands. It is deliberately reactive — loom does not
// keep its own copy of fleet-db's schema, which would drift in the other
// direction and silently stop sending fields a newer fleet-db does accept.
// ANCHORED on purpose. A loose `unknown field "X"` search anywhere in the message
// would also fire on a business-rule error that merely mentions the phrase — e.g.
// `policy references unknown field "owner"` — and loom would silently delete `owner`
// and retry instead of surfacing the real validation failure. fleet-db's strict
// decoder produces exactly this message and nothing else, so require the whole string.
var unknownFieldRe = regexp.MustCompile(`^(?:json: )?unknown field "([^"]+)"$`)

// unknownUpdateField reports the field name fleet-db rejected, if the error is
// that specific strict-decode validation failure.
func unknownUpdateField(err error) (string, bool) {
	var backendErr *backend.BackendError
	if !errors.As(err, &backendErr) || backendErr.Kind != backend.KindValidation {
		return "", false
	}
	m := unknownFieldRe.FindStringSubmatch(strings.TrimSpace(backendErr.Message))
	if len(m) != 2 || strings.TrimSpace(m[1]) == "" {
		return "", false
	}
	return m[1], true
}

// patchIssueTolerantly PATCHes req, retrying without any field fleet-db reports
// as unknown. Returns the fields that had to be dropped so callers can surface
// the drift rather than silently diverging from what was asked.
func (b *FleetBackend) patchIssueTolerantly(
	ctx context.Context,
	id string,
	req map[string]interface{},
	actor string,
) (dropped []string, err error) {
	path := "/issues/" + url.PathEscape(id)
	// Each iteration removes exactly one field, so this cannot spin: it is
	// bounded by the field count, and any error that is not a named
	// unknown-field rejection returns immediately.
	for len(req) > 0 {
		if _, err = b.execResponseAsActor(ctx, "Update", "PATCH", path, req, actor); err == nil {
			sort.Strings(dropped)
			return dropped, nil
		}
		field, ok := unknownUpdateField(err)
		if !ok {
			return dropped, err
		}
		if _, present := req[field]; !present {
			// The server named a field we did not send. Retrying would loop
			// forever on an unchanged body, so surface the original error.
			return dropped, err
		}
		delete(req, field)
		dropped = append(dropped, field)
		slog.Warn("fleet update: dropping field rejected by fleet-db",
			"issue", id, "field", field,
			"detail", "loom sends a field this fleet-db's UpdateIssueRequest does not declare; the remaining fields are being retried")
	}

	// Everything was rejected. Reporting success here would be silent data
	// loss — the caller asked for changes and none of them landed.
	sort.Strings(dropped)
	return dropped, backend.ErrValidation("Update",
		"fleet-db rejected every field in this update as unknown: "+strings.Join(dropped, ", "))
}
