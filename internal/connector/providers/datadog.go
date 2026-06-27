package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// Datadog connector actions (dotted snake_case per the CV1 action grammar).
const (
	// ActionDatadogMonitorsRead lists monitors (read-only).
	ActionDatadogMonitorsRead = "datadog.monitors.read"
	// ActionDatadogAlertRead reads one monitor's alert state (read-only).
	ActionDatadogAlertRead = "datadog.alert.read"
	// ActionDatadogIncidentsWrite declares an incident, gated by a
	// best-effort pre-egress alert-still-firing check on the source
	// monitor: the POST is never issued when the alert is no longer
	// firing (and not recently resolved) — StaleSubject instead. Datadog
	// offers no native write precondition, so the check-to-post TOCTOU
	// window is accepted and documented; an incident can be resolved or
	// deleted afterwards.
	ActionDatadogIncidentsWrite = "datadog.incidents.write"
)

// DatadogActions returns the actions the Datadog provider implements (a
// copy).
func DatadogActions() []string {
	return []string{
		ActionDatadogMonitorsRead,
		ActionDatadogAlertRead,
		ActionDatadogIncidentsWrite,
	}
}

// DefaultDatadogBaseURL is the public Datadog API endpoint (US1 site).
const DefaultDatadogBaseURL = "https://api.datadoghq.com"

// datadogRecentlyResolvedWindow is how long after resolution an alert still
// counts as actionable for incidents.write: the egress-needs findings allow
// "firing/recently-resolved" so a declaration racing the monitor's recovery
// is not spuriously refused.
const datadogRecentlyResolvedWindow = 5 * time.Minute

// Datadog is the Provider adapter for the Datadog API. The base URL is
// injectable for tests; the zero values fall back to the public API and
// http.DefaultClient. now is injectable for freshness-window tests.
type Datadog struct {
	baseURL string
	client  *http.Client
	now     func() time.Time
}

var _ Provider = (*Datadog)(nil)

// NewDatadog builds a Datadog provider. client defaults to
// http.DefaultClient and baseURL to DefaultDatadogBaseURL when empty.
func NewDatadog(client *http.Client, baseURL string) *Datadog {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = DefaultDatadogBaseURL
	}
	return &Datadog{baseURL: strings.TrimSuffix(baseURL, "/"), client: client, now: time.Now}
}

// Call implements Provider, dispatching on spec.Action.
func (d *Datadog) Call(ctx context.Context, spec CallSpec) (CallResult, error) {
	switch spec.Action {
	case ActionDatadogMonitorsRead:
		return d.monitorsRead(ctx, spec)
	case ActionDatadogAlertRead:
		return d.alertRead(ctx, spec)
	case ActionDatadogIncidentsWrite:
		return d.incidentsWrite(ctx, spec)
	default:
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("datadog provider does not implement %q: %w", spec.Action, ErrUnknownAction)
	}
}

// monitorsRead lists monitors; optional camelCase args name and monitorTags
// map to Datadog's query parameters.
func (d *Datadog) monitorsRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	query := url.Values{}
	if v, ok := stringArg(spec.Args, "name"); ok {
		query.Set("name", v)
	}
	if v, ok := stringArg(spec.Args, "monitorTags"); ok {
		query.Set("monitor_tags", v)
	}
	res, err := d.do(ctx, spec, http.MethodGet, "/api/v1/monitor", query, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			d.upstreamError(spec, res)
	}
	var raw []map[string]any
	_ = json.Unmarshal(res.body, &raw)
	monitors := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		monitors = append(monitors, monitorSummary(m))
	}
	return CallResult{
		Status:   res.status,
		Body:     map[string]any{"monitors": monitors},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// alertRead reads one monitor's alert state by monitorId.
func (d *Datadog) alertRead(ctx context.Context, spec CallSpec) (CallResult, error) {
	id, err := monitorIDArg(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	res, err := d.do(ctx, spec, http.MethodGet, "/api/v1/monitor/"+strconv.Itoa(id), nil, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			d.upstreamError(spec, res)
	}
	return CallResult{
		Status:   res.status,
		Body:     monitorSummary(decodeObject(res.body)),
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// incidentsWrite declares an incident after a best-effort pre-egress
// freshness read of the source monitor (args.monitorId): the POST is never
// issued unless the alert is still firing or resolved within
// datadogRecentlyResolvedWindow. A missing monitorId is the missing
// freshness subject, refused as PreconditionRequired before any egress.
func (d *Datadog) incidentsWrite(ctx context.Context, spec CallSpec) (CallResult, error) {
	id, present, err := optionalMonitorIDArg(spec.Args)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if !present {
		return CallResult{Decision: domain.ConnectorCallPreconditionRequired},
			&PreconditionRequired{Action: spec.Action, Fields: []string{"monitorId"}}
	}
	if err := requireIdempotencyKey(spec); err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	title, ok := stringArg(spec.Args, "title")
	if !ok {
		return CallResult{Decision: domain.ConnectorCallUpstreamError},
			fmt.Errorf("%s requires args.title: %w", spec.Action, domain.ErrInvalid)
	}

	if result, err := d.monitorFreshnessCheck(ctx, spec, id); err != nil {
		return result, err
	}

	attrs := map[string]any{"title": title}
	if v, ok := spec.Args["customerImpacted"].(bool); ok {
		attrs["customer_impacted"] = v
	}
	payload := map[string]any{
		"data": map[string]any{"type": "incidents", "attributes": attrs},
	}
	res, err := d.do(ctx, spec, http.MethodPost, "/api/v2/incidents", nil, payload)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	if res.status != http.StatusOK && res.status != http.StatusCreated {
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			d.upstreamError(spec, res)
	}
	obj := decodeObject(res.body)
	data, _ := obj["data"].(map[string]any)
	return CallResult{
		Status: res.status,
		Body: map[string]any{
			"id":    data["id"],
			"title": nestedString(obj, "data", "attributes", "title"),
		},
		Decision: domain.ConnectorCallGranted,
	}, nil
}

// monitorFreshnessCheck is incidentsWrite's best-effort pre-egress
// alert-still-firing check: it reads the source monitor and refuses with
// StaleSubject when it is gone or no longer actionable. A nil error means
// the incident POST may proceed.
func (d *Datadog) monitorFreshnessCheck(ctx context.Context, spec CallSpec, id int) (CallResult, error) {
	res, err := d.do(ctx, spec, http.MethodGet, "/api/v1/monitor/"+strconv.Itoa(id), nil, nil)
	if err != nil {
		return CallResult{Decision: domain.ConnectorCallUpstreamError}, err
	}
	switch {
	case res.status == http.StatusNotFound:
		return CallResult{Decision: domain.ConnectorCallStaleSubject},
			&StaleSubject{
				Action:   spec.Action,
				Resource: spec.Resource,
				Expected: "firing",
				Reason:   fmt.Sprintf("monitor %d not found", id),
			}
	case res.status != http.StatusOK:
		return CallResult{Status: res.status, Decision: domain.ConnectorCallUpstreamError},
			d.upstreamError(spec, res)
	}
	monitor := decodeObject(res.body)
	state, _ := monitor["overall_state"].(string)
	if !d.alertActionable(state, monitor) {
		return CallResult{Decision: domain.ConnectorCallStaleSubject},
			&StaleSubject{
				Action:   spec.Action,
				Resource: spec.Resource,
				Expected: "firing",
				Reason:   fmt.Sprintf("alert no longer firing (monitor state %q, not recently resolved)", state),
			}
	}
	return CallResult{}, nil
}

// alertActionable reports whether a monitor still warrants an incident:
// firing states pass outright; an OK monitor passes only when its state
// changed within datadogRecentlyResolvedWindow (recently resolved).
func (d *Datadog) alertActionable(state string, monitor map[string]any) bool {
	switch state {
	case "Alert", "Warn", "No Data":
		return true
	}
	modified, _ := monitor["overall_state_modified"].(string)
	t, err := time.Parse(time.RFC3339, modified)
	if err != nil {
		return false
	}
	return d.now().Sub(t) <= datadogRecentlyResolvedWindow
}

// do issues one Datadog API request: the credential splits into the
// DD-API-KEY and DD-APPLICATION-KEY headers and rides nowhere else.
func (d *Datadog) do(ctx context.Context, spec CallSpec, method, path string, query url.Values, payload any) (httpResult, error) {
	u := d.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	apiKey, appKey := splitDatadogCredential(spec.Credential)
	return doJSON(ctx, d.client, spec, method, u, payload,
		func(req *http.Request) {
			req.Header.Set("DD-API-KEY", apiKey)
			if appKey != "" {
				req.Header.Set("DD-APPLICATION-KEY", appKey)
			}
		},
		d.sanitizer(spec))
}

// upstreamError maps a non-success Datadog response to the structured
// provider errors: 429 becomes RateLimited with the provider-suggested wait;
// everything else becomes UpstreamError with a sanitized summary plucked
// from Datadog's errors array — never the raw body.
func (d *Datadog) upstreamError(spec CallSpec, res httpResult) error {
	if res.status == http.StatusTooManyRequests {
		return &RateLimited{
			Action:     spec.Action,
			Status:     res.status,
			RetryAfter: datadogRetryAfter(res.header),
		}
	}
	class := ClassClientError
	if res.status >= 500 {
		class = ClassServerError
	}
	return &UpstreamError{
		Action:  spec.Action,
		Class:   class,
		Status:  res.status,
		Summary: d.sanitizer(spec)(datadogErrorMessage(res.body)),
	}
}

// sanitizer scrubs both halves of the split Datadog credential (and the
// combined literal) before any upstream text reaches an error string or
// audit summary.
func (d *Datadog) sanitizer(spec CallSpec) func(string) string {
	apiKey, appKey := splitDatadogCredential(spec.Credential)
	return func(msg string) string {
		if apiKey != "" {
			msg = strings.ReplaceAll(msg, apiKey, redactedMarker)
		}
		if appKey != "" {
			msg = strings.ReplaceAll(msg, appKey, redactedMarker)
		}
		return sanitizeUpstreamMessage(msg, spec.Credential)
	}
}

// splitDatadogCredential splits the sealed-credential plaintext into its API
// key and optional application key halves; the stored format is "apiKey" or
// "apiKey:appKey".
func splitDatadogCredential(credential string) (apiKey, appKey string) {
	if i := strings.IndexByte(credential, ':'); i >= 0 {
		return credential[:i], credential[i+1:]
	}
	return credential, ""
}

// datadogRetryAfter prefers the standard Retry-After header, falling back to
// Datadog's X-RateLimit-Reset (seconds until the window resets).
func datadogRetryAfter(header http.Header) time.Duration {
	if wait := parseRetryAfter(header); wait > 0 {
		return wait
	}
	secs, err := strconv.Atoi(header.Get("X-RateLimit-Reset"))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// datadogErrorMessage plucks the first entry of Datadog's errors array from
// an error body; only this field — never the raw body — feeds error
// summaries.
func datadogErrorMessage(body []byte) string {
	var obj struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &obj); err != nil || len(obj.Errors) == 0 {
		return ""
	}
	return obj.Errors[0]
}

// monitorSummary whitelists the camelCase monitor fields exposed in
// CallResult.Body.
func monitorSummary(m map[string]any) map[string]any {
	return map[string]any{
		"id":                   m["id"],
		"name":                 m["name"],
		"type":                 m["type"],
		"overallState":         m["overall_state"],
		"overallStateModified": m["overall_state_modified"],
	}
}

// monitorIDArg extracts the required monitorId arg.
func monitorIDArg(args map[string]any) (int, error) {
	id, present, err := optionalMonitorIDArg(args)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, fmt.Errorf("args.monitorId required: %w", domain.ErrInvalid)
	}
	return id, nil
}

// optionalMonitorIDArg extracts the monitorId arg, reporting presence.
func optionalMonitorIDArg(args map[string]any) (int, bool, error) {
	return intArg(args, "monitorId")
}
