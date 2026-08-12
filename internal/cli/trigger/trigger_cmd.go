// Package trigger implements the `loom trigger` command tree for managing the
// trigger-driven driver workflow surface: creating/listing TriggerBindings and
// inspecting the persisted TriggerEvent / TriggerDelivery audit trail.
package trigger

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
)

var triggerCmd = &cobra.Command{
	Use:     "trigger",
	Short:   "Manage trigger bindings and inspect trigger events/deliveries",
	GroupID: "workspace",
}

// --- bindings ---

var bindingsCmd = &cobra.Command{
	Use:   "bindings",
	Short: "Manage trigger bindings",
}

var (
	bindCreateRouteKey  string
	bindCreateWorkflow  string
	bindCreateDriver    string
	bindCreateVersion   string
	bindCreateName      string
	bindCreateSource    string
	bindCreateBindingID string
	bindCreateEntry     string
	bindCreateDisabled  bool
	bindCreateJSON      bool
	bindCreateRouter    routerBindingFlags

	bindUpdateJSON   bool
	bindUpdateRouter routerBindingFlags

	bindListSource  string
	bindListEnabled bool
	bindListJSON    bool
)

var bindingsCreateCmd = &cobra.Command{
	Use:               "create",
	Short:             "Create a trigger binding that maps a route key to a pinned driver version",
	Args:              cobra.NoArgs,
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runBindingsCreate,
}

var bindingsUpdateCmd = &cobra.Command{
	Use:               "update <binding-id>",
	Short:             "Update Router v2 fields on an existing trigger binding",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runBindingsUpdate,
}

var bindingsListCmd = &cobra.Command{
	Use:               "list",
	Short:             "List trigger bindings",
	Args:              cobra.NoArgs,
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runBindingsList,
}

var bindingsShowCmd = &cobra.Command{
	Use:               "show <binding-id>",
	Aliases:           []string{"get"},
	Short:             "Show a single trigger binding",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runBindingsShow,
}

var (
	bindDeleteJSON bool
	bindRunJSON    bool
)

var bindingsDeleteCmd = &cobra.Command{
	Use:               "delete <binding-id>",
	Aliases:           []string{"rm"},
	Short:             "Delete a trigger binding and revoke its connector grants",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runBindingsDelete,
}

var bindingsRunCmd = &cobra.Command{
	Use:               "run <binding-id>",
	Short:             "Dispatch a trigger binding manually",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runBindingsRun,
}

// routerBindingFlags groups the Router v2 binding flags shared by the create
// and update commands. Each command binds its own instance so create/update
// flag state never aliases.
type routerBindingFlags struct {
	subjectKeyTemplate string
	concurrencyPolicy  string
	actorExclude       []string
	retryMaxAttempts   int
	retryBackoffSecs   int
	schedule           string
	scheduleTimezone   string
	patterns           []string
}

func registerRouterBindingFlags(cmd *cobra.Command, f *routerBindingFlags) {
	cmd.Flags().StringVar(&f.subjectKeyTemplate, "subject-key-template", "",
		"concurrency subject key template; tokens: {{subject_ref}}, {{event_type}}, {{attrs.<name>}} (empty = default key <binding-id>|<subject-ref>)")
	cmd.Flags().StringVar(&f.concurrencyPolicy, "concurrency-policy", "",
		"concurrency policy: allow|forbid|replace|queue|one_active_per_epic")
	cmd.Flags().StringArrayVar(&f.actorExclude, "actor-filter-exclude", nil,
		"actor kind(s) to exclude (repeatable); on update, pass a single empty value to clear the filter")
	cmd.Flags().IntVar(&f.retryMaxAttempts, "retry-max-attempts", 0,
		"delivery retry attempts (0 = server default 5)")
	cmd.Flags().IntVar(&f.retryBackoffSecs, "retry-backoff", 0,
		"delivery retry base backoff in seconds, exponential with 1h cap (0 = server default 30)")
	cmd.Flags().StringVar(&f.schedule, "schedule", "",
		"cron expression (standard 5-field or @descriptor); required when --source is cron")
	cmd.Flags().StringVar(&f.scheduleTimezone, "schedule-timezone", "",
		"IANA timezone the schedule is evaluated in (default UTC)")
	// StringArray, not StringSlice: pflag's slice flavor splits values on
	// commas, which would tear apart {a,b} alternation segments.
	cmd.Flags().StringArrayVar(&f.patterns, "event-pattern", nil,
		"event type pattern(s), dot-segmented glob with * and {a,b} (repeatable)")
}

// validate mirrors fleet-db's per-field Router v2 rules (C3,
// validateTriggerBindingRouterParams): concurrency policy enum, event-pattern
// grammar, subject key template tokens, non-negative retry values and IANA
// timezone. The cron *expression* grammar is validated server-side by the
// robfig/cron parser; the CLI only enforces presence (validateForCreate).
// Note: non-allow policies deliberately do NOT require a subject template —
// deliveries fall back to the default key "<binding_id>|<subject_ref>".
func (f *routerBindingFlags) validate() error {
	if p := strings.TrimSpace(f.concurrencyPolicy); p != "" && !isValidConcurrencyPolicy(automation.BindingConcurrencyPolicy(p)) {
		return fmt.Errorf("--concurrency-policy %q is invalid: must be one of allow, forbid, replace, queue, one_active_per_epic", p)
	}
	for _, pattern := range f.patterns {
		if err := trigger.ValidatePattern(pattern); err != nil {
			return fmt.Errorf("--event-pattern %q: %w", pattern, err)
		}
	}
	if tpl := strings.TrimSpace(f.subjectKeyTemplate); tpl != "" {
		if err := validateSubjectKeyTemplate(tpl); err != nil {
			return fmt.Errorf("--subject-key-template: %w", err)
		}
	}
	if f.retryMaxAttempts < 0 {
		return fmt.Errorf("--retry-max-attempts must be non-negative")
	}
	if f.retryBackoffSecs < 0 {
		return fmt.Errorf("--retry-backoff must be non-negative")
	}
	if tz := strings.TrimSpace(f.scheduleTimezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("--schedule-timezone %q is not a valid IANA timezone", tz)
		}
	}
	return nil
}

// validateForCreate adds the cross-field rules that only make sense when the
// whole binding is in hand (update patches are checked server-side against the
// stored binding instead).
func (f *routerBindingFlags) validateForCreate(source string) error {
	if err := f.validate(); err != nil {
		return err
	}
	if source == "cron" && strings.TrimSpace(f.schedule) == "" {
		return fmt.Errorf("--schedule is required when --source is %q", "cron")
	}
	if strings.TrimSpace(f.scheduleTimezone) != "" && strings.TrimSpace(f.schedule) == "" {
		return fmt.Errorf("--schedule-timezone requires --schedule")
	}
	return nil
}

// actorFilter converts the repeatable --actor-filter-exclude values into a
// domain filter, dropping blank entries. Returns nil when no constraint
// remains (create sends no filter; update callers map nil to the zero filter,
// which clears it server-side).
func (f *routerBindingFlags) actorFilter() *automation.ActorFilter {
	kinds := make([]string, 0, len(f.actorExclude))
	for _, k := range f.actorExclude {
		if t := strings.TrimSpace(k); t != "" {
			kinds = append(kinds, t)
		}
	}
	if len(kinds) == 0 {
		return nil
	}
	return &automation.ActorFilter{ExcludeActorKinds: kinds}
}

// patch builds the management API patch from the flags the operator actually
// set. A changed --actor-filter-exclude with only blank values becomes the
// explicit clear operation (C4 replace-whole semantics).
func (f *routerBindingFlags) patch(flags *pflag.FlagSet) (triggerBindingPatchRequest, error) {
	if err := f.validate(); err != nil {
		return triggerBindingPatchRequest{}, err
	}
	patch := triggerBindingPatchRequest{
		SubjectKeyTemplate:  strPtrIfChanged(flags, "subject-key-template", f.subjectKeyTemplate),
		RetryMaxAttempts:    intPtrIfChanged(flags, "retry-max-attempts", f.retryMaxAttempts),
		RetryBackoffSeconds: intPtrIfChanged(flags, "retry-backoff", f.retryBackoffSecs),
		Schedule:            strPtrIfChanged(flags, "schedule", f.schedule),
		ScheduleTimezone:    strPtrIfChanged(flags, "schedule-timezone", f.scheduleTimezone),
	}
	if flags.Changed("concurrency-policy") {
		p := automation.BindingConcurrencyPolicy(strings.TrimSpace(f.concurrencyPolicy))
		patch.ConcurrencyPolicy = &p
	}
	if flags.Changed("actor-filter-exclude") {
		filter := f.actorFilter()
		if filter == nil {
			patch.ClearActorFilter = true
		} else {
			patch.ActorFilter = filter
		}
	}
	if flags.Changed("event-pattern") {
		v := append([]string(nil), f.patterns...)
		patch.EventTypePatterns = &v
	}
	if patch == (triggerBindingPatchRequest{}) {
		return triggerBindingPatchRequest{}, fmt.Errorf("no fields to update: pass at least one flag")
	}
	return patch, nil
}

func isValidConcurrencyPolicy(p automation.BindingConcurrencyPolicy) bool {
	switch p {
	case automation.ConcurrencyAllow,
		automation.ConcurrencyForbid,
		automation.ConcurrencyReplace,
		automation.ConcurrencyQueue,
		automation.ConcurrencyOneActivePerEpic:
		return true
	}
	return false
}

// validateSubjectKeyTemplate mirrors fleet-db's models.ValidateSubjectKeyTemplate
// (C3): templates substitute {{subject_ref}}, {{event_type}} and
// {{attrs.<name>}} only — they never read the raw payload, so any other token
// is rejected.
func validateSubjectKeyTemplate(tpl string) error {
	rest := tpl
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			return nil
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return fmt.Errorf("unterminated token near %q", rest[start:])
		}
		token := strings.TrimSpace(rest[start+2 : start+end])
		if !validSubjectKeyToken(token) {
			return fmt.Errorf("token %q is invalid: allowed tokens are subject_ref, event_type, attrs.<name>", token)
		}
		rest = rest[start+end+2:]
	}
}

func validSubjectKeyToken(token string) bool {
	if token == "subject_ref" || token == "event_type" {
		return true
	}
	name := strings.TrimPrefix(token, "attrs.")
	return name != token && strings.TrimSpace(name) != ""
}

func strPtrIfChanged(flags *pflag.FlagSet, name, val string) *string {
	if !flags.Changed(name) {
		return nil
	}
	v := strings.TrimSpace(val)
	return &v
}

func intPtrIfChanged(flags *pflag.FlagSet, name string, val int) *int {
	if !flags.Changed(name) {
		return nil
	}
	v := val
	return &v
}

func runBindingsCreate(cmd *cobra.Command, _ []string) error {
	source := firstNonEmpty(strings.TrimSpace(bindCreateSource), "github")
	routeKey := strings.TrimSpace(bindCreateRouteKey)
	// A cron binding fires by schedule and has no external route, so it needs a
	// --binding-id but not a --route-key. Event sources need an explicit route.
	if source == "cron" {
		if routeKey == "" && strings.TrimSpace(bindCreateBindingID) == "" {
			return fmt.Errorf("--binding-id is required for a cron trigger binding")
		}
	} else if routeKey == "" {
		return fmt.Errorf("--route-key is required")
	}
	driverRef := strings.TrimSpace(firstNonEmpty(bindCreateDriver, bindCreateWorkflow))
	if driverRef == "" {
		return fmt.Errorf("one of --driver or --workflow is required")
	}
	if err := bindCreateRouter.validateForCreate(source); err != nil {
		return err
	}
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	binding, err := client.createBinding(ctx, newBindingCreateRequest(routeKey, source))
	if err != nil {
		return fmt.Errorf("create trigger binding: %w", err)
	}
	if bindCreateJSON {
		return cmdstore.WriteJSON(binding)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created trigger binding %s (route %s → driver %s version %s, enabled=%t)\n",
		binding.BindingID, binding.RouteKey, binding.DriverID, binding.DriverVersionID, binding.Enabled)
	return nil
}

func newBindingCreateRequest(routeKey, source string) triggerBindingCreateRequest {
	bindingID := firstNonEmpty(strings.TrimSpace(bindCreateBindingID), defaultBindingID(routeKey))
	enabled := !bindCreateDisabled
	request := triggerBindingCreateRequest{
		BindingID: bindingID,
		// Name falls back to binding_id because cron leaves route_key empty and
		// cannot use it to seed a display name.
		Name:                firstNonEmpty(strings.TrimSpace(bindCreateName), routeKey, bindingID),
		SourceKind:          source,
		RouteKey:            routeKey,
		EventTypePatterns:   append([]string(nil), bindCreateRouter.patterns...),
		DriverVersionID:     strings.TrimSpace(bindCreateVersion),
		Entrypoint:          strings.TrimSpace(bindCreateEntry),
		ConcurrencyPolicy:   automation.BindingConcurrencyPolicy(strings.TrimSpace(bindCreateRouter.concurrencyPolicy)),
		SubjectKeyTemplate:  strings.TrimSpace(bindCreateRouter.subjectKeyTemplate),
		ActorFilter:         bindCreateRouter.actorFilter(),
		RetryMaxAttempts:    bindCreateRouter.retryMaxAttempts,
		RetryBackoffSeconds: bindCreateRouter.retryBackoffSecs,
		Schedule:            strings.TrimSpace(bindCreateRouter.schedule),
		ScheduleTimezone:    strings.TrimSpace(bindCreateRouter.scheduleTimezone),
		Enabled:             &enabled,
	}
	if driver := strings.TrimSpace(bindCreateDriver); driver != "" {
		request.DriverID = driver
	} else {
		request.Workflow = strings.TrimSpace(bindCreateWorkflow)
	}
	return request
}

func runBindingsUpdate(cmd *cobra.Command, args []string) error {
	patch, err := bindUpdateRouter.patch(cmd.Flags())
	if err != nil {
		return err
	}
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	binding, err := client.updateBinding(ctx, strings.TrimSpace(args[0]), patch)
	if err != nil {
		return fmt.Errorf("update trigger binding: %w", err)
	}
	if bindUpdateJSON {
		return cmdstore.WriteJSON(binding)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated trigger binding %s (policy=%s, enabled=%t)\n",
		binding.BindingID, binding.ConcurrencyPolicy, binding.Enabled)
	return nil
}

// runBindingsDelete delegates the restartable disable/revoke/delete workflow
// to serve, which owns both Automation authority and connector credentials.
func runBindingsDelete(cmd *cobra.Command, args []string) error {
	bindingID := strings.TrimSpace(args[0])
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	result, err := client.deleteBinding(ctx, bindingID)
	if err != nil {
		return fmt.Errorf("delete trigger binding: %w", err)
	}
	if bindDeleteJSON {
		return cmdstore.WriteJSON(result)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted trigger binding %s (grants revoked=%d)\n", result.BindingID, result.GrantsRevoked)
	return nil
}

func runBindingsList(cmd *cobra.Command, _ []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	bindings, err := client.listBindings(ctx)
	if err != nil {
		return fmt.Errorf("list trigger bindings: %w", err)
	}
	bindings = filterBindingList(bindings, strings.TrimSpace(bindListSource), cmd.Flags().Changed("enabled"), bindListEnabled)
	if bindListJSON {
		return cmdstore.WriteJSON(bindings)
	}
	renderBindingsList(cmd.OutOrStdout(), bindings)
	return nil
}

func runBindingsShow(cmd *cobra.Command, args []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	binding, err := client.getBinding(ctx, strings.TrimSpace(args[0]))
	if err != nil {
		return fmt.Errorf("get trigger binding: %w", err)
	}
	return cmdstore.WriteJSON(binding)
}

func runBindingsRun(cmd *cobra.Command, args []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	bindingID := strings.TrimSpace(args[0])
	run, err := client.runBinding(ctx, bindingID)
	if err != nil {
		return fmt.Errorf("run trigger binding: %w", err)
	}
	if bindRunJSON {
		return cmdstore.WriteJSON(run)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded trigger binding run %s (%s)\n", run.RunID, run.Status)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Binding: %s driver %s version %s\n", bindingID, run.DriverID, run.DriverVersionID)
	return nil
}

func filterBindingList(bindings []*automation.Binding, sourceKind string, filterEnabled, enabled bool) []*automation.Binding {
	if sourceKind == "" && !filterEnabled {
		return bindings
	}
	filtered := make([]*automation.Binding, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil || (sourceKind != "" && binding.SourceKind != sourceKind) || (filterEnabled && binding.Enabled != enabled) {
			continue
		}
		filtered = append(filtered, binding)
	}
	return filtered
}

// renderBindingsList writes the human-readable bindings listing. Kept as a
// pure helper so the golden-output test can exercise it directly.
func renderBindingsList(w io.Writer, bindings []*automation.Binding) {
	if len(bindings) == 0 {
		_, _ = fmt.Fprintln(w, "No trigger bindings.")
		return
	}
	for _, b := range bindings {
		_, _ = fmt.Fprintln(w, formatBindingRow(b))
	}
}

func formatBindingRow(b *automation.Binding) string {
	policy := string(b.ConcurrencyPolicy)
	if policy == "" {
		policy = "-"
	}
	row := fmt.Sprintf("%-28s route=%-32s driver=%-20s policy=%-19s retry=%d/%ds enabled=%t",
		b.BindingID, b.RouteKey, b.DriverID, policy, b.RetryMaxAttempts, b.RetryBackoffSeconds, b.Enabled)
	if b.Schedule != "" {
		row += fmt.Sprintf(" schedule=%q", b.Schedule)
		if b.ScheduleTimezone != "" {
			row += " tz=" + b.ScheduleTimezone
		}
	}
	if b.SubjectKeyTemplate != "" {
		row += " subject-template=" + b.SubjectKeyTemplate
	}
	return row
}

// --- events ---

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Inspect persisted trigger events",
}

var (
	eventsListSource string
	eventsListLimit  int
	eventsListJSON   bool
)

var eventsListCmd = &cobra.Command{
	Use:               "list",
	Short:             "List trigger events",
	Args:              cobra.NoArgs,
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runEventsList,
}

var eventsShowCmd = &cobra.Command{
	Use:               "show <event-id>",
	Short:             "Show a single trigger event",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runEventsShow,
}

func runEventsList(cmd *cobra.Command, _ []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	events, err := client.listEvents(ctx, eventsListSource, eventsListLimit)
	if err != nil {
		return fmt.Errorf("list trigger events: %w", err)
	}
	if eventsListJSON {
		return cmdstore.WriteJSON(events)
	}
	if len(events) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No trigger events.")
		return nil
	}
	for _, event := range events {
		if event == nil {
			return fmt.Errorf("list trigger events: management API returned a null event")
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-22s %-22s subject=%-24s sig=%s\n", event.EventID, event.EventType, event.SubjectRef, event.SignatureStatus)
	}
	return nil
}

func runEventsShow(cmd *cobra.Command, args []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	event, err := client.getEvent(ctx, strings.TrimSpace(args[0]))
	if err != nil {
		return fmt.Errorf("get trigger event: %w", err)
	}
	return cmdstore.WriteJSON(event)
}

// --- deliveries ---

var deliveriesCmd = &cobra.Command{
	Use:   "deliveries",
	Short: "Inspect persisted trigger deliveries",
}

var (
	delivListEvent  string
	delivListStatus string
	delivListLimit  int
	delivListJSON   bool
)

var deliveriesListCmd = &cobra.Command{
	Use:               "list",
	Short:             "List trigger deliveries",
	Args:              cobra.NoArgs,
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runDeliveriesList,
}

var deliveriesShowCmd = &cobra.Command{
	Use:               "show <delivery-id>",
	Short:             "Show a single trigger delivery",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: cli.PrepareStandaloneHTTPCommand,
	RunE:              runDeliveriesShow,
}

func runDeliveriesList(cmd *cobra.Command, _ []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	deliveries, err := client.listDeliveries(ctx, delivListEvent, delivListStatus, delivListLimit)
	if err != nil {
		return fmt.Errorf("list trigger deliveries: %w", err)
	}
	if delivListJSON {
		return cmdstore.WriteJSON(deliveries)
	}
	if len(deliveries) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No trigger deliveries.")
		return nil
	}
	for _, delivery := range deliveries {
		if delivery == nil {
			return fmt.Errorf("list trigger deliveries: management API returned a null delivery")
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-26s event=%-22s status=%-12s run=%s\n", delivery.DeliveryID, delivery.TriggerEventID, delivery.Status, delivery.DriverRunID)
	}
	return nil
}

func runDeliveriesShow(cmd *cobra.Command, args []string) error {
	ctx := triggerCommandContext(cmd)
	client, err := newTriggerManagementClient(ctx)
	if err != nil {
		return err
	}
	delivery, err := client.getDelivery(ctx, strings.TrimSpace(args[0]))
	if err != nil {
		return fmt.Errorf("get trigger delivery: %w", err)
	}
	return cmdstore.WriteJSON(delivery)
}

func triggerCommandContext(cmd *cobra.Command) context.Context {
	if cmd != nil && cmd.Context() != nil {
		return cmd.Context()
	}
	return context.Background()
}

func defaultBindingID(routeKey string) string {
	return "binding-" + strings.ReplaceAll(routeKey, ".", "-")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func init() {
	bindingsCreateCmd.Flags().StringVar(&bindCreateRouteKey, "route-key", "", "route key, e.g. github.pull_request.opened")
	bindingsCreateCmd.Flags().StringVar(&bindCreateWorkflow, "workflow", "", "workflow name backing the driver")
	bindingsCreateCmd.Flags().StringVar(&bindCreateDriver, "driver", "", "driver id or name (alternative to --workflow)")
	bindingsCreateCmd.Flags().StringVar(&bindCreateVersion, "driver-version", "", "pin a specific driver version id (default: active version)")
	bindingsCreateCmd.Flags().StringVar(&bindCreateName, "name", "", "binding display name (default: route key)")
	bindingsCreateCmd.Flags().StringVar(&bindCreateSource, "source", "github", "source kind")
	bindingsCreateCmd.Flags().StringVar(&bindCreateBindingID, "binding-id", "", "binding id (default: derived from route key)")
	bindingsCreateCmd.Flags().StringVar(&bindCreateEntry, "entrypoint", "", "target entrypoint (default: driver default)")
	bindingsCreateCmd.Flags().BoolVar(&bindCreateDisabled, "disabled", false, "create the binding disabled")
	bindingsCreateCmd.Flags().BoolVar(&bindCreateJSON, "json", false, "JSON output")
	registerRouterBindingFlags(bindingsCreateCmd, &bindCreateRouter)

	bindingsUpdateCmd.Flags().BoolVar(&bindUpdateJSON, "json", false, "JSON output")
	registerRouterBindingFlags(bindingsUpdateCmd, &bindUpdateRouter)

	bindingsListCmd.Flags().StringVar(&bindListSource, "source-kind", "", "filter by source kind")
	bindingsListCmd.Flags().BoolVar(&bindListEnabled, "enabled", false, "filter by enabled state (only applied when set)")
	bindingsListCmd.Flags().BoolVar(&bindListJSON, "json", false, "JSON output")

	bindingsDeleteCmd.Flags().BoolVar(&bindDeleteJSON, "json", false, "JSON output")
	bindingsRunCmd.Flags().BoolVar(&bindRunJSON, "json", false, "JSON output")

	bindingsCmd.AddCommand(bindingsCreateCmd, bindingsUpdateCmd, bindingsListCmd, bindingsShowCmd, bindingsDeleteCmd, bindingsRunCmd)

	eventsListCmd.Flags().StringVar(&eventsListSource, "source-kind", "", "filter by source kind")
	eventsListCmd.Flags().IntVar(&eventsListLimit, "limit", 0, "max results")
	eventsListCmd.Flags().BoolVar(&eventsListJSON, "json", false, "JSON output")
	// `show` is a detail view and always prints JSON, like other show commands.
	eventsCmd.AddCommand(eventsListCmd, eventsShowCmd)

	deliveriesListCmd.Flags().StringVar(&delivListEvent, "event", "", "filter by trigger event id")
	deliveriesListCmd.Flags().StringVar(&delivListStatus, "status", "", "filter by delivery status")
	deliveriesListCmd.Flags().IntVar(&delivListLimit, "limit", 0, "max results")
	deliveriesListCmd.Flags().BoolVar(&delivListJSON, "json", false, "JSON output")
	deliveriesCmd.AddCommand(deliveriesListCmd, deliveriesShowCmd)

	triggerCmd.AddCommand(bindingsCmd, eventsCmd, deliveriesCmd)
	cli.RegisterCommand(triggerCmd)
}
