package archtest

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const performanceInventorySchemaVersion = 1

const (
	performanceMeasured       = "measured"
	performanceNotYetMigrated = "not-yet-migrated"
	performanceNotApplicable  = "not-applicable"
)

var performanceSourceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// PerformanceInventory is the Phase 0/1 performance and operability record.
// Fixed fields make omission of any required baseline category a schema error.
type PerformanceInventory struct {
	SchemaVersion             int                      `yaml:"schema_version"`
	LoomServeStartupReadiness StartupReadinessMetric   `yaml:"loom_serve_startup_readiness"`
	WorkflowApprovalLatency   WorkflowLatencyMetric    `yaml:"workflow_approval_latency"`
	FleetDBRoundTrips         RoundTripMetric          `yaml:"fleetdb_round_trips"`
	ProductionBackgroundLoops BackgroundLoopMetric     `yaml:"production_background_loops"`
	FullBuildGateDuration     BuildGateDurationMetric  `yaml:"full_build_gate_duration"`
	FrontendRouteChunkSizes   FrontendRouteChunkMetric `yaml:"frontend_route_chunk_sizes"`
}

// PerformanceRecord carries provenance shared by every metric.
type PerformanceRecord struct {
	Status    string              `yaml:"status"`
	Rationale string              `yaml:"rationale"`
	Evidence  PerformanceEvidence `yaml:"evidence"`
}

// PerformanceEvidence makes every result attributable and reproducible.
// Command is an argv vector; unavailable metrics explicitly encode [] and
// explain the future procedure instead of inventing a number.
type PerformanceEvidence struct {
	SourceSHA      string                 `yaml:"source_sha"`
	MeasuredAt     string                 `yaml:"measured_at"`
	EvidenceSource string                 `yaml:"evidence_source"`
	Command        []string               `yaml:"command"`
	Procedure      string                 `yaml:"procedure"`
	Environment    PerformanceEnvironment `yaml:"environment"`
}

// PerformanceEnvironment records the execution profile needed to compare a
// future sample. Unknown historical values must be stated explicitly.
type PerformanceEnvironment struct {
	OperatingSystem string   `yaml:"operating_system"`
	Architecture    string   `yaml:"architecture"`
	CPU             string   `yaml:"cpu"`
	Memory          string   `yaml:"memory"`
	GoVersion       string   `yaml:"go_version"`
	NodeVersion     string   `yaml:"node_version"`
	Profile         string   `yaml:"profile"`
	Dependencies    []string `yaml:"dependencies"`
}

// RecordedNumber distinguishes an explicitly recorded YAML null from an
// omitted value. That lets a not-yet-migrated result remain honest while the
// validator still rejects incomplete records.
type RecordedNumber struct {
	Value   float64
	Present bool
	Valid   bool
}

// UnmarshalYAML records whether value was present and whether it was numeric.
func (number *RecordedNumber) UnmarshalYAML(node *yaml.Node) error {
	number.Present = true
	if node.Tag == "!!null" {
		return nil
	}
	if err := node.Decode(&number.Value); err != nil {
		return fmt.Errorf("measurement value must be a number or null: %w", err)
	}
	if math.IsNaN(number.Value) || math.IsInf(number.Value, 0) {
		return errors.New("measurement value must be finite")
	}
	number.Valid = true
	return nil
}

// PerformanceMeasurement is a value plus an explicit comparison unit.
type PerformanceMeasurement struct {
	Value RecordedNumber `yaml:"value"`
	Unit  string         `yaml:"unit"`
}

// UnmarshalYAML preserves explicit null and retains strict field checking even
// though yaml.v3 otherwise skips a field-level unmarshaler for null nodes.
func (measurement *PerformanceMeasurement) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("performance measurement must be a mapping")
	}
	seen := make(map[string]struct{}, 2)
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		value := node.Content[index+1]
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("performance measurement has duplicate field %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "value":
			if err := measurement.Value.UnmarshalYAML(value); err != nil {
				return err
			}
		case "unit":
			if err := value.Decode(&measurement.Unit); err != nil {
				return fmt.Errorf("measurement unit must be a string: %w", err)
			}
		default:
			return fmt.Errorf("field %s not found in type archtest.PerformanceMeasurement", key)
		}
	}
	return nil
}

type StartupReadinessMetric struct {
	Record PerformanceRecord      `yaml:"record"`
	Result StartupReadinessResult `yaml:"result"`
}

type StartupReadinessResult struct {
	SampleCount                 int                    `yaml:"sample_count"`
	PercentileMethod            string                 `yaml:"percentile_method"`
	StartupToListenMilliseconds []float64              `yaml:"startup_to_listen_milliseconds"`
	ReadinessMilliseconds       []float64              `yaml:"readiness_milliseconds"`
	StartupP50                  PerformanceMeasurement `yaml:"startup_p50"`
	StartupP95                  PerformanceMeasurement `yaml:"startup_p95"`
	ReadinessP50                PerformanceMeasurement `yaml:"readiness_p50"`
	ReadinessP95                PerformanceMeasurement `yaml:"readiness_p95"`
}

type WorkflowLatencyMetric struct {
	Record PerformanceRecord     `yaml:"record"`
	Result WorkflowLatencyResult `yaml:"result"`
}

type WorkflowLatencyResult struct {
	UseCase     string                 `yaml:"use_case"`
	SampleCount int                    `yaml:"sample_count"`
	P50         PerformanceMeasurement `yaml:"p50"`
	P95         PerformanceMeasurement `yaml:"p95"`
}

type RoundTripMetric struct {
	Record PerformanceRecord `yaml:"record"`
	Result RoundTripResult   `yaml:"result"`
}

type RoundTripResult struct {
	CommandName   string                 `yaml:"command_name"`
	SamplingScope string                 `yaml:"sampling_scope"`
	RoundTrips    PerformanceMeasurement `yaml:"round_trips_per_command"`
}

type BackgroundLoopMetric struct {
	Record PerformanceRecord    `yaml:"record"`
	Result BackgroundLoopResult `yaml:"result"`
}

type BackgroundLoopResult struct {
	InventoryPath        string `yaml:"inventory_path"`
	TotalComponents      int    `yaml:"total_components"`
	TickerSites          int    `yaml:"ticker_sites"`
	GoroutineLaunchSites int    `yaml:"goroutine_launch_sites"`
	Managed              int    `yaml:"managed"`
	CommandPoll          int    `yaml:"command_poll"`
	RequestScoped        int    `yaml:"request_scoped"`
	StartupWait          int    `yaml:"startup_wait"`
	BackgroundLoopCount  int    `yaml:"background_loop_count"`
	CountRule            string `yaml:"count_rule"`
}

type BuildGateDurationMetric struct {
	Record PerformanceRecord `yaml:"record"`
	Result BuildGateResult   `yaml:"result"`
}

type BuildGateResult struct {
	Scope     string                 `yaml:"scope"`
	Precision string                 `yaml:"precision"`
	Duration  PerformanceMeasurement `yaml:"duration"`
}

type FrontendRouteChunkMetric struct {
	Record PerformanceRecord        `yaml:"record"`
	Result FrontendRouteChunkResult `yaml:"result"`
}

type FrontendRouteChunkResult struct {
	AssetScope    string                 `yaml:"asset_scope"`
	BuildDuration PerformanceMeasurement `yaml:"build_duration"`
	RouteCount    int                    `yaml:"route_count"`
	Routes        []RouteChunk           `yaml:"routes"`
}

type RouteChunk struct {
	Route    string `yaml:"route"`
	Entry    string `yaml:"entry"`
	RawBytes int64  `yaml:"raw_bytes"`
}

// LoadPerformanceInventory strictly decodes and validates the baseline.
func LoadPerformanceInventory(path string) (PerformanceInventory, error) {
	var inventory PerformanceInventory
	if err := decodeYAML(path, &inventory); err != nil {
		return PerformanceInventory{}, fmt.Errorf("decode performance inventory: %w", err)
	}
	if err := inventory.Validate(); err != nil {
		return PerformanceInventory{}, err
	}
	return inventory, nil
}

// Validate requires complete evidence and status-coherent results for every
// Phase 0/1 metric category.
func (inventory PerformanceInventory) Validate() error {
	if inventory.SchemaVersion != performanceInventorySchemaVersion {
		return fmt.Errorf("performance inventory schema_version: got %d, want %d", inventory.SchemaVersion, performanceInventorySchemaVersion)
	}
	checks := []struct {
		name   string
		record PerformanceRecord
		result func(string) error
	}{
		{"loom_serve_startup_readiness", inventory.LoomServeStartupReadiness.Record, inventory.validateStartup},
		{"workflow_approval_latency", inventory.WorkflowApprovalLatency.Record, inventory.validateWorkflowLatency},
		{"fleetdb_round_trips", inventory.FleetDBRoundTrips.Record, inventory.validateRoundTrips},
		{"production_background_loops", inventory.ProductionBackgroundLoops.Record, inventory.validateBackgroundLoops},
		{"full_build_gate_duration", inventory.FullBuildGateDuration.Record, inventory.validateBuildGate},
		{"frontend_route_chunk_sizes", inventory.FrontendRouteChunkSizes.Record, inventory.validateFrontendChunks},
	}
	for _, check := range checks {
		if err := validatePerformanceRecord(check.name, check.record); err != nil {
			return err
		}
		if err := check.result(check.record.Status); err != nil {
			return fmt.Errorf("performance metric %s: %w", check.name, err)
		}
	}
	return nil
}

// ComparePerformanceRuntimeInventory prevents the operability baseline from
// drifting away from the strict named-component inventory it summarizes.
func ComparePerformanceRuntimeInventory(performance PerformanceInventory, runtimeInventory RuntimeInventory) error {
	result := performance.ProductionBackgroundLoops.Result
	counts := make(map[string]int)
	tickerSites := 0
	for _, component := range runtimeInventory.Components {
		counts[component.Classification]++
		if component.Kind == "ticker" {
			tickerSites++
		}
	}
	if result.TotalComponents != len(runtimeInventory.Components) || result.TickerSites != tickerSites ||
		result.GoroutineLaunchSites != len(runtimeInventory.GoroutineLaunches) {
		return fmt.Errorf("performance runtime totals components=%d tickers=%d goroutine_launches=%d do not match runtime inventory components=%d tickers=%d goroutine_launches=%d",
			result.TotalComponents, result.TickerSites, result.GoroutineLaunchSites,
			len(runtimeInventory.Components), tickerSites, len(runtimeInventory.GoroutineLaunches))
	}
	if result.Managed != counts["managed"] || result.CommandPoll != counts["command-poll"] ||
		result.RequestScoped != counts["request-scoped"] || result.StartupWait != counts["startup-wait"] ||
		result.BackgroundLoopCount != counts["managed"] {
		return fmt.Errorf("performance runtime classifications do not match runtime inventory: performance=%+v inventory=%+v", result, counts)
	}
	return nil
}

func validatePerformanceRecord(name string, record PerformanceRecord) error {
	if record.Status != performanceMeasured && record.Status != performanceNotYetMigrated && record.Status != performanceNotApplicable {
		return fmt.Errorf("performance metric %s has unsupported status %q", name, record.Status)
	}
	if strings.TrimSpace(record.Rationale) == "" {
		return fmt.Errorf("performance metric %s requires rationale", name)
	}
	evidence := record.Evidence
	if !performanceSourceSHA.MatchString(evidence.SourceSHA) {
		return fmt.Errorf("performance metric %s source_sha must be a 40-character lowercase Git SHA", name)
	}
	if !validPerformanceDate(evidence.MeasuredAt) {
		return fmt.Errorf("performance metric %s measured_at must be an ISO date or RFC3339 timestamp", name)
	}
	if strings.TrimSpace(evidence.EvidenceSource) == "" || strings.TrimSpace(evidence.Procedure) == "" {
		return fmt.Errorf("performance metric %s requires evidence_source and procedure", name)
	}
	if evidence.Command == nil {
		return fmt.Errorf("performance metric %s must explicitly record command", name)
	}
	if record.Status == performanceMeasured && len(evidence.Command) == 0 {
		return fmt.Errorf("performance metric %s measured status requires a command", name)
	}
	if record.Status != performanceMeasured && len(evidence.Command) != 0 {
		return fmt.Errorf("performance metric %s unavailable status requires command: []", name)
	}
	for index, arg := range evidence.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("performance metric %s command[%d] must not be blank", name, index)
		}
	}
	return validatePerformanceEnvironment(name, evidence.Environment)
}

func validPerformanceDate(value string) bool {
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return true
	}
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}

func validatePerformanceEnvironment(name string, environment PerformanceEnvironment) error {
	values := []struct{ name, value string }{
		{"operating_system", environment.OperatingSystem},
		{"architecture", environment.Architecture},
		{"cpu", environment.CPU},
		{"memory", environment.Memory},
		{"go_version", environment.GoVersion},
		{"node_version", environment.NodeVersion},
		{"profile", environment.Profile},
	}
	for _, field := range values {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("performance metric %s environment.%s must not be blank", name, field.name)
		}
	}
	if environment.Dependencies == nil {
		return fmt.Errorf("performance metric %s environment.dependencies must be explicitly recorded", name)
	}
	for index, dependency := range environment.Dependencies {
		if strings.TrimSpace(dependency) == "" {
			return fmt.Errorf("performance metric %s environment.dependencies[%d] must not be blank", name, index)
		}
	}
	return nil
}

func (inventory PerformanceInventory) validateStartup(status string) error {
	if status != performanceMeasured {
		return errors.New("loom serve startup/readiness must be measured")
	}
	result := inventory.LoomServeStartupReadiness.Result
	if result.SampleCount < 2 || len(result.StartupToListenMilliseconds) != result.SampleCount || len(result.ReadinessMilliseconds) != result.SampleCount {
		return errors.New("startup/readiness sample_count must match both sample arrays and be at least 2")
	}
	if result.PercentileMethod != "nearest-rank" {
		return errors.New("startup/readiness percentile_method must be nearest-rank")
	}
	if err := validatePositiveSamples("startup_to_listen_milliseconds", result.StartupToListenMilliseconds); err != nil {
		return err
	}
	if err := validatePositiveSamples("readiness_milliseconds", result.ReadinessMilliseconds); err != nil {
		return err
	}
	checks := []struct {
		name        string
		measurement PerformanceMeasurement
		want        float64
	}{
		{"startup_p50", result.StartupP50, nearestRank(result.StartupToListenMilliseconds, 0.50)},
		{"startup_p95", result.StartupP95, nearestRank(result.StartupToListenMilliseconds, 0.95)},
		{"readiness_p50", result.ReadinessP50, nearestRank(result.ReadinessMilliseconds, 0.50)},
		{"readiness_p95", result.ReadinessP95, nearestRank(result.ReadinessMilliseconds, 0.95)},
	}
	for _, check := range checks {
		if err := validateMeasurement(check.name, status, check.measurement, "milliseconds"); err != nil {
			return err
		}
		if math.Abs(check.measurement.Value.Value-check.want) > 0.0005 {
			return fmt.Errorf("%s got %.3f, want nearest-rank %.3f", check.name, check.measurement.Value.Value, check.want)
		}
	}
	return nil
}

func validatePositiveSamples(name string, values []float64) error {
	for index, value := range values {
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s[%d] must be positive and finite", name, index)
		}
	}
	return nil
}

func nearestRank(values []float64, percentile float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	rank := int(math.Ceil(percentile * float64(len(ordered))))
	return ordered[rank-1]
}

func (inventory PerformanceInventory) validateWorkflowLatency(status string) error {
	result := inventory.WorkflowApprovalLatency.Result
	if strings.TrimSpace(result.UseCase) == "" {
		return errors.New("workflow latency use_case must not be blank")
	}
	if status == performanceMeasured && result.SampleCount <= 0 {
		return errors.New("measured workflow latency requires samples")
	}
	if status != performanceMeasured && result.SampleCount != 0 {
		return errors.New("unavailable workflow latency must have sample_count 0")
	}
	if err := validateMeasurement("p50", status, result.P50, "milliseconds"); err != nil {
		return err
	}
	return validateMeasurement("p95", status, result.P95, "milliseconds")
}

func (inventory PerformanceInventory) validateRoundTrips(status string) error {
	result := inventory.FleetDBRoundTrips.Result
	if strings.TrimSpace(result.CommandName) == "" || strings.TrimSpace(result.SamplingScope) == "" {
		return errors.New("fleet-db round trips require command_name and sampling_scope")
	}
	return validateMeasurement("round_trips_per_command", status, result.RoundTrips, "round-trips/command")
}

func (inventory PerformanceInventory) validateBackgroundLoops(status string) error {
	if status != performanceMeasured {
		return errors.New("production background loops must be measured")
	}
	result := inventory.ProductionBackgroundLoops.Result
	if result.InventoryPath != "internal/archtest/testdata/runtime-components.yaml" || strings.TrimSpace(result.CountRule) == "" {
		return errors.New("background loop result must reference runtime-components.yaml and state its count rule")
	}
	if result.TotalComponents <= 0 || result.TickerSites <= 0 || result.GoroutineLaunchSites <= 0 || result.BackgroundLoopCount <= 0 {
		return errors.New("background loop component, ticker, goroutine-launch, and loop counts must be positive")
	}
	classified := result.Managed + result.CommandPoll + result.RequestScoped + result.StartupWait
	if classified != result.TotalComponents {
		return fmt.Errorf("background loop classification count %d does not equal total_components %d", classified, result.TotalComponents)
	}
	if result.TickerSites > result.TotalComponents || result.BackgroundLoopCount > result.TotalComponents {
		return errors.New("background ticker/loop counts cannot exceed total_components")
	}
	return nil
}

func (inventory PerformanceInventory) validateBuildGate(status string) error {
	if status != performanceMeasured {
		return errors.New("full build/gate duration must be measured")
	}
	result := inventory.FullBuildGateDuration.Result
	if strings.TrimSpace(result.Scope) == "" || !slices.Contains([]string{"exact", "approximate"}, result.Precision) {
		return errors.New("full build/gate result requires scope and exact/approximate precision")
	}
	return validateMeasurement("duration", status, result.Duration, "seconds")
}

func (inventory PerformanceInventory) validateFrontendChunks(status string) error {
	if status != performanceMeasured {
		return errors.New("frontend route chunk sizes must be measured")
	}
	result := inventory.FrontendRouteChunkSizes.Result
	if strings.TrimSpace(result.AssetScope) == "" || result.RouteCount <= 0 || len(result.Routes) != result.RouteCount {
		return errors.New("frontend chunks require asset_scope and matching positive route_count")
	}
	if err := validateMeasurement("build_duration", status, result.BuildDuration, "seconds"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(result.Routes))
	for index, route := range result.Routes {
		if !strings.HasPrefix(route.Route, "/") || strings.TrimSpace(route.Entry) == "" || route.RawBytes <= 0 {
			return fmt.Errorf("frontend route[%d] requires absolute route, entry, and positive raw_bytes", index)
		}
		if _, exists := seen[route.Route]; exists {
			return fmt.Errorf("frontend chunks contain duplicate route %q", route.Route)
		}
		seen[route.Route] = struct{}{}
		if index > 0 && result.Routes[index-1].Route >= route.Route {
			return errors.New("frontend routes must be sorted by route")
		}
	}
	return nil
}

func validateMeasurement(name, status string, measurement PerformanceMeasurement, unit string) error {
	if !measurement.Value.Present {
		return fmt.Errorf("%s must explicitly include value", name)
	}
	if measurement.Unit != unit {
		return fmt.Errorf("%s unit got %q, want %q", name, measurement.Unit, unit)
	}
	if status == performanceMeasured {
		if !measurement.Value.Valid || measurement.Value.Value <= 0 {
			return fmt.Errorf("%s measured value must be positive", name)
		}
		return nil
	}
	if measurement.Value.Valid {
		return fmt.Errorf("%s must be null while status is %s", name, status)
	}
	return nil
}
