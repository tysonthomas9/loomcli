// Package trigger implements the `loom trigger` command tree for managing the
// trigger-driven driver workflow surface: creating/listing TriggerBindings and
// inspecting the persisted TriggerEvent / TriggerDelivery audit trail.
package trigger

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	bindCreatePatterns  []string
	bindCreateEntry     string
	bindCreateDisabled  bool
	bindCreateJSON      bool

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

var bindingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List trigger bindings",
	Args:  cobra.NoArgs,
	RunE:  runBindingsList,
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
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
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
		source := firstNonEmpty(strings.TrimSpace(bindCreateSource), "github")
		// An enabled github binding with no secret rejects every signed webhook
		// (HMAC verification fails on an empty secret), so refuse to create one.
		if source == "github" && !bindCreateDisabled && strings.TrimSpace(bindCreateSecret) == "" {
			return fmt.Errorf("enabled github bindings require --secret (or pass --disabled to create it inactive)")
		}
		binding, err := h.Store.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
			WorkspaceKey:      ws,
			BindingID:         firstNonEmpty(strings.TrimSpace(bindCreateBindingID), defaultBindingID(routeKey)),
			Name:              firstNonEmpty(strings.TrimSpace(bindCreateName), routeKey),
			SourceKind:        source,
			RouteKey:          routeKey,
			EventTypePatterns: bindCreatePatterns,
			DriverID:          driver.DriverID,
			DriverVersionID:   versionID,
			TargetEntrypoint:  strings.TrimSpace(bindCreateEntry),
			WebhookSecret:     bindCreateSecret,
			Enabled:           !bindCreateDisabled,
		})
		if err != nil {
			return fmt.Errorf("create trigger binding: %w", err)
		}
		if bindCreateJSON {
			return cmdstore.WriteJSON(binding)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created trigger binding %s (route %s → driver %s version %s, enabled=%t)\n",
			binding.BindingID, binding.RouteKey, binding.DriverID, binding.DriverVersionID, binding.Enabled)
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
		if len(bindings) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No trigger bindings.")
			return nil
		}
		for _, b := range bindings {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-28s route=%-32s driver=%s enabled=%t\n", b.BindingID, b.RouteKey, b.DriverID, b.Enabled)
		}
		return nil
	})
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
	bindingsCreateCmd.Flags().StringSliceVar(&bindCreatePatterns, "event-pattern", nil, "event type pattern(s) (repeatable)")
	bindingsCreateCmd.Flags().StringVar(&bindCreateEntry, "entrypoint", "", "target entrypoint (default: driver default)")
	bindingsCreateCmd.Flags().BoolVar(&bindCreateDisabled, "disabled", false, "create the binding disabled")
	bindingsCreateCmd.Flags().BoolVar(&bindCreateJSON, "json", false, "JSON output")

	bindingsListCmd.Flags().StringVar(&bindListSource, "source-kind", "", "filter by source kind")
	bindingsListCmd.Flags().BoolVar(&bindListEnabled, "enabled", false, "filter by enabled state (only applied when set)")
	bindingsListCmd.Flags().BoolVar(&bindListJSON, "json", false, "JSON output")

	bindingsCmd.AddCommand(bindingsCreateCmd, bindingsListCmd)

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
