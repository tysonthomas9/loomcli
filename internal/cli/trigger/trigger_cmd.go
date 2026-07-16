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

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
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
	bindCreateSecret    string
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
	Use:   "create",
	Short: "Create a trigger binding that maps a route key to a pinned driver version",
	Args:  cobra.NoArgs,
	RunE:  runBindingsCreate,
}

var bindingsUpdateCmd = &cobra.Command{
	Use:   "update <binding-id>",
	Short: "Update Router v2 fields on an existing trigger binding",
	Args:  cobra.ExactArgs(1),
	RunE:  runBindingsUpdate,
}

var bindingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List trigger bindings",
	Args:  cobra.NoArgs,
	RunE:  runBindingsList,
}

var bindingsShowCmd = &cobra.Command{
	Use:     "show <binding-id>",
	Aliases: []string{"get"},
	Short:   "Show a single trigger binding",
	Args:    cobra.ExactArgs(1),
	RunE:    runBindingsShow,
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
	if p := strings.TrimSpace(f.concurrencyPolicy); p != "" && !isValidConcurrencyPolicy(domain.TriggerBindingConcurrencyPolicy(p)) {
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
func (f *routerBindingFlags) actorFilter() *domain.TriggerActorFilter {
	kinds := make([]string, 0, len(f.actorExclude))
	for _, k := range f.actorExclude {
		if t := strings.TrimSpace(k); t != "" {
			kinds = append(kinds, t)
		}
	}
	if len(kinds) == 0 {
		return nil
	}
	return &domain.TriggerActorFilter{ExcludeActorKinds: kinds}
}

// patch builds a TriggerBindingUpdate from the flags the operator actually
// set. A changed --actor-filter-exclude with only blank values becomes the
// zero filter, which clears the stored filter (C4 replace-whole semantics).
func (f *routerBindingFlags) patch(flags *pflag.FlagSet) (store.TriggerBindingUpdate, error) {
	if err := f.validate(); err != nil {
		return store.TriggerBindingUpdate{}, err
	}
	patch := store.TriggerBindingUpdate{
		SubjectKeyTemplate:  strPtrIfChanged(flags, "subject-key-template", f.subjectKeyTemplate),
		RetryMaxAttempts:    intPtrIfChanged(flags, "retry-max-attempts", f.retryMaxAttempts),
		RetryBackoffSeconds: intPtrIfChanged(flags, "retry-backoff", f.retryBackoffSecs),
		Schedule:            strPtrIfChanged(flags, "schedule", f.schedule),
		ScheduleTimezone:    strPtrIfChanged(flags, "schedule-timezone", f.scheduleTimezone),
	}
	if flags.Changed("concurrency-policy") {
		p := domain.TriggerBindingConcurrencyPolicy(strings.TrimSpace(f.concurrencyPolicy))
		patch.ConcurrencyPolicy = &p
	}
	if flags.Changed("actor-filter-exclude") {
		filter := f.actorFilter()
		if filter == nil {
			filter = &domain.TriggerActorFilter{}
		}
		patch.ActorFilter = filter
	}
	if flags.Changed("event-pattern") {
		v := append([]string(nil), f.patterns...)
		patch.EventTypePatterns = &v
	}
	if patch == (store.TriggerBindingUpdate{}) {
		return store.TriggerBindingUpdate{}, fmt.Errorf("no fields to update: pass at least one flag")
	}
	return patch, nil
}

func isValidConcurrencyPolicy(p domain.TriggerBindingConcurrencyPolicy) bool {
	switch p {
	case domain.TriggerBindingConcurrencyAllow,
		domain.TriggerBindingConcurrencyForbid,
		domain.TriggerBindingConcurrencyReplace,
		domain.TriggerBindingConcurrencyQueue,
		domain.TriggerBindingConcurrencyOneActivePerEpic:
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
	routeKey := strings.TrimSpace(bindCreateRouteKey)
	if routeKey == "" {
		return fmt.Errorf("--route-key is required")
	}
	driverRef := strings.TrimSpace(firstNonEmpty(bindCreateDriver, bindCreateWorkflow))
	if driverRef == "" {
		return fmt.Errorf("one of --driver or --workflow is required")
	}
	source := firstNonEmpty(strings.TrimSpace(bindCreateSource), "github")
	// An enabled github binding with no secret rejects every signed webhook
	// (HMAC verification fails on an empty secret), so refuse to create one.
	if source == "github" && !bindCreateDisabled && strings.TrimSpace(bindCreateSecret) == "" {
		return fmt.Errorf("enabled github bindings require --secret (or pass --disabled to create it inactive)")
	}
	if err := bindCreateRouter.validateForCreate(source); err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		return createBindingInWorkspace(ctx, cmd, h, ws, routeKey, source)
	})
}

func createBindingInWorkspace(ctx context.Context, cmd *cobra.Command, h *bootstrap.StoreHandle, ws, routeKey, source string) error {
	driverRef := strings.TrimSpace(firstNonEmpty(bindCreateDriver, bindCreateWorkflow))
	driver, err := resolveDriver(ctx, h.Store, ws, driverRef)
	if err != nil {
		return err
	}
	versionID := strings.TrimSpace(bindCreateVersion)
	if versionID == "" {
		versionID = driver.ActiveVersionID
	}
	if versionID == "" {
		return fmt.Errorf("driver %q has no active version; pass --driver-version or activate one first", driver.DriverID)
	}
	binding, err := h.Store.TriggerBindings().Create(ctx, newBindingCreateInput(ws, routeKey, source, driver.DriverID, versionID))
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

func newBindingCreateInput(ws, routeKey, source, driverID, versionID string) store.TriggerBindingCreate {
	return store.TriggerBindingCreate{
		WorkspaceKey:        ws,
		BindingID:           firstNonEmpty(strings.TrimSpace(bindCreateBindingID), defaultBindingID(routeKey)),
		Name:                firstNonEmpty(strings.TrimSpace(bindCreateName), routeKey),
		SourceKind:          source,
		RouteKey:            routeKey,
		EventTypePatterns:   bindCreateRouter.patterns,
		DriverID:            driverID,
		DriverVersionID:     versionID,
		TargetEntrypoint:    strings.TrimSpace(bindCreateEntry),
		ConcurrencyPolicy:   domain.TriggerBindingConcurrencyPolicy(strings.TrimSpace(bindCreateRouter.concurrencyPolicy)),
		WebhookSecret:       bindCreateSecret,
		SubjectKeyTemplate:  strings.TrimSpace(bindCreateRouter.subjectKeyTemplate),
		ActorFilter:         bindCreateRouter.actorFilter(),
		RetryMaxAttempts:    bindCreateRouter.retryMaxAttempts,
		RetryBackoffSeconds: bindCreateRouter.retryBackoffSecs,
		Schedule:            strings.TrimSpace(bindCreateRouter.schedule),
		ScheduleTimezone:    strings.TrimSpace(bindCreateRouter.scheduleTimezone),
		Enabled:             !bindCreateDisabled,
	}
}

func runBindingsUpdate(cmd *cobra.Command, args []string) error {
	patch, err := bindUpdateRouter.patch(cmd.Flags())
	if err != nil {
		return err
	}
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		binding, err := h.Store.TriggerBindings().Update(ctx, ws, strings.TrimSpace(args[0]), patch)
		if err != nil {
			return fmt.Errorf("update trigger binding: %w", err)
		}
		if bindUpdateJSON {
			return cmdstore.WriteJSON(binding)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated trigger binding %s (policy=%s, enabled=%t)\n",
			binding.BindingID, binding.ConcurrencyPolicy, binding.Enabled)
		return nil
	})
}

func runBindingsList(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		filter := store.TriggerBindingFilter{SourceKind: strings.TrimSpace(bindListSource)}
		if cmd.Flags().Changed("enabled") {
			filter.Enabled = &bindListEnabled
		}
		bindings, err := h.Store.TriggerBindings().List(ctx, ws, filter)
		if err != nil {
			return fmt.Errorf("list trigger bindings: %w", err)
		}
		if bindListJSON {
			return cmdstore.WriteJSON(bindings)
		}
		renderBindingsList(cmd.OutOrStdout(), bindings)
		return nil
	})
}

func runBindingsShow(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		binding, err := h.Store.TriggerBindings().Get(ctx, ws, strings.TrimSpace(args[0]))
		if err != nil {
			return fmt.Errorf("get trigger binding: %w", err)
		}
		return cmdstore.WriteJSON(binding)
	})
}

// renderBindingsList writes the human-readable bindings listing. Kept as a
// pure helper so the golden-output test can exercise it directly.
func renderBindingsList(w io.Writer, bindings []*domain.TriggerBinding) {
	if len(bindings) == 0 {
		_, _ = fmt.Fprintln(w, "No trigger bindings.")
		return
	}
	for _, b := range bindings {
		_, _ = fmt.Fprintln(w, formatBindingRow(b))
	}
}

func formatBindingRow(b *domain.TriggerBinding) string {
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
	Use:   "list",
	Short: "List trigger events",
	Args:  cobra.NoArgs,
	RunE:  runEventsList,
}

var eventsShowCmd = &cobra.Command{
	Use:   "show <event-id>",
	Short: "Show a single trigger event",
	Args:  cobra.ExactArgs(1),
	RunE:  runEventsShow,
}

func runEventsList(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		events, err := h.Store.TriggerEvents().List(ctx, ws, store.TriggerEventFilter{
			SourceKind: strings.TrimSpace(eventsListSource),
			Limit:      eventsListLimit,
		})
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
		for _, e := range events {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-22s %-22s subject=%-24s sig=%s\n", e.EventID, e.EventType, e.SubjectRef, e.SignatureStatus)
		}
		return nil
	})
}

func runEventsShow(cmd *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		event, err := h.Store.TriggerEvents().Get(ctx, ws, strings.TrimSpace(args[0]))
		if err != nil {
			return fmt.Errorf("get trigger event: %w", err)
		}
		return cmdstore.WriteJSON(event)
	})
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
	Use:   "list",
	Short: "List trigger deliveries",
	Args:  cobra.NoArgs,
	RunE:  runDeliveriesList,
}

var deliveriesShowCmd = &cobra.Command{
	Use:   "show <delivery-id>",
	Short: "Show a single trigger delivery",
	Args:  cobra.ExactArgs(1),
	RunE:  runDeliveriesShow,
}

func runDeliveriesList(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		deliveries, err := h.Store.TriggerDeliveries().List(ctx, ws, store.TriggerDeliveryFilter{
			TriggerEventID: strings.TrimSpace(delivListEvent),
			Status:         domain.TriggerDeliveryStatus(strings.TrimSpace(delivListStatus)),
			Limit:          delivListLimit,
		})
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
		for _, d := range deliveries {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-26s event=%-22s status=%-12s run=%s\n", d.DeliveryID, d.TriggerEventID, d.Status, d.DriverRunID)
		}
		return nil
	})
}

func runDeliveriesShow(cmd *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		delivery, err := h.Store.TriggerDeliveries().Get(ctx, ws, strings.TrimSpace(args[0]))
		if err != nil {
			return fmt.Errorf("get trigger delivery: %w", err)
		}
		return cmdstore.WriteJSON(delivery)
	})
}

// resolveDriver looks up a driver by ID, then by name — mirroring how the
// workflows module resolves a workflow's backing driver.
func resolveDriver(ctx context.Context, st store.Store, ws, ref string) (*domain.Driver, error) {
	driver, err := st.Drivers().Get(ctx, ws, ref)
	if err == nil {
		return driver, nil
	}
	if !cmdstore.IsNotFound(err) {
		return nil, fmt.Errorf("get driver %q: %w", ref, err)
	}
	drivers, err := st.Drivers().List(ctx, ws, store.DriverFilter{Name: ref, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("list drivers: %w", err)
	}
	if len(drivers) == 0 {
		return nil, fmt.Errorf("driver or workflow %q not found in workspace %q", ref, ws)
	}
	return drivers[0], nil
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
	bindingsCreateCmd.Flags().StringVar(&bindCreateSecret, "secret", "", "webhook HMAC secret for signature verification")
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

	bindingsCmd.AddCommand(bindingsCreateCmd, bindingsUpdateCmd, bindingsListCmd, bindingsShowCmd)

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
