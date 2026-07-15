package archtest

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const runtimeInventorySchemaVersion = 2

var (
	runtimeComponentID    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	tickerSiteName        = regexp.MustCompile(`^time\.NewTicker#[1-9][0-9]*$`)
	goroutineSiteName     = regexp.MustCompile(`^go#[1-9][0-9]*$`)
	productionRoots       = []string{"cmd", "internal", "sdk"}
	excludedDirNames      = []string{".git", "node_modules", "third_party", "vendor", "worktrees"}
	runtimeClasses        = []string{"command-poll", "managed", "request-scoped", "startup-wait"}
	goroutineDispositions = []string{"bounded", "command", "component", "helper", "request"}
)

// RuntimeInventory is the checked-in ownership and lifecycle ledger for
// in-scope polling definitions and other boot or long-lived components.
type RuntimeInventory struct {
	SchemaVersion              int                      `yaml:"schema_version"`
	ProductionRoots            []string                 `yaml:"production_roots"`
	Exclusions                 RuntimeExclusions        `yaml:"exclusions"`
	GoroutineDispositionPolicy string                   `yaml:"goroutine_disposition_policy"`
	GoroutineLaunches          []RuntimeGoroutineLaunch `yaml:"goroutine_launches"`
	Components                 []RuntimeComponent       `yaml:"components"`
}

// RuntimeExclusions records the explicit boundary between scanned source and
// files that must not affect the runtime inventory.
type RuntimeExclusions struct {
	TestFiles      bool     `yaml:"test_files"`
	GeneratedFiles bool     `yaml:"generated_files"`
	Directories    []string `yaml:"directories"`
}

// RuntimeComponent describes one stable runtime site and its lifecycle
// contract. Ticker sites use time.NewTicker#N, ordered within their enclosing
// function; non-ticker components use a descriptive component:<name> site.
type RuntimeComponent struct {
	ID              string `yaml:"id"`
	Kind            string `yaml:"kind"`
	File            string `yaml:"file"`
	Function        string `yaml:"function"`
	Site            string `yaml:"site"`
	Capability      string `yaml:"capability"`
	Owner           string `yaml:"owner"`
	Cadence         string `yaml:"cadence"`
	Cancellation    string `yaml:"cancellation"`
	ReadinessHealth string `yaml:"readiness_health"`
	RetryBackoff    string `yaml:"retry_backoff"`
	Classification  string `yaml:"classification"`
}

// TickerSite is the mechanically observed identity of one in-scope non-test
// time.NewTicker call. Line is diagnostic only and is not part of identity.
type TickerSite struct {
	File     string
	Function string
	Site     string
	Line     int
}

// RuntimeGoroutineLaunch is the stable, mechanically observed identity of one
// in-scope non-test source go statement. Line is intentionally omitted from
// the manifest: the ordinal within the enclosing function survives unrelated line movement.
// The all-source scan includes mutually exclusive build-tag variants, while
// tests and generated code follow the inventory's explicit exclusions.
type RuntimeGoroutineLaunch struct {
	File         string   `yaml:"file"`
	Function     string   `yaml:"function"`
	Site         string   `yaml:"site"`
	Callee       string   `yaml:"callee"`
	Disposition  string   `yaml:"disposition"`
	ComponentIDs []string `yaml:"component_ids,omitempty"`
	Reason       string   `yaml:"reason,omitempty"`
}

// GoroutineSite carries a launch identity plus its diagnostic source line.
type GoroutineSite struct {
	RuntimeGoroutineLaunch
	Line int
}

// LoadRuntimeInventory strictly decodes and validates a runtime inventory.
func LoadRuntimeInventory(path string) (RuntimeInventory, error) {
	var value RuntimeInventory
	if err := decodeYAML(path, &value); err != nil {
		return RuntimeInventory{}, fmt.Errorf("decode runtime inventory: %w", err)
	}
	if err := value.Validate(); err != nil {
		return RuntimeInventory{}, err
	}
	return value, nil
}

// Validate rejects incomplete lifecycle records and any attempt to weaken the
// source scanning policy.
func (inventory RuntimeInventory) Validate() error {
	if inventory.SchemaVersion != runtimeInventorySchemaVersion {
		return fmt.Errorf("runtime inventory schema_version: got %d, want %d", inventory.SchemaVersion, runtimeInventorySchemaVersion)
	}
	if !slices.Equal(inventory.ProductionRoots, productionRoots) {
		return fmt.Errorf("runtime inventory production_roots: got %v, want %v", inventory.ProductionRoots, productionRoots)
	}
	if !inventory.Exclusions.TestFiles || !inventory.Exclusions.GeneratedFiles {
		return errors.New("runtime inventory must exclude both test and generated files")
	}
	if !slices.Equal(inventory.Exclusions.Directories, excludedDirNames) {
		return fmt.Errorf("runtime inventory excluded directories: got %v, want %v", inventory.Exclusions.Directories, excludedDirNames)
	}
	if inventory.GoroutineDispositionPolicy != "default-deny" {
		return fmt.Errorf("runtime inventory goroutine_disposition_policy: got %q, want default-deny", inventory.GoroutineDispositionPolicy)
	}
	if len(inventory.Components) == 0 {
		return errors.New("runtime inventory components must not be empty")
	}

	ids := make(map[string]struct{}, len(inventory.Components))
	identities := make(map[string]struct{}, len(inventory.Components))
	for i, component := range inventory.Components {
		if err := validateRuntimeComponent(component); err != nil {
			return fmt.Errorf("runtime inventory components[%d]: %w", i, err)
		}
		if _, exists := ids[component.ID]; exists {
			return fmt.Errorf("runtime inventory duplicate component id %q", component.ID)
		}
		ids[component.ID] = struct{}{}
		identity := runtimeIdentity(component.File, component.Function, component.Site)
		if _, exists := identities[identity]; exists {
			return fmt.Errorf("runtime inventory duplicate site %s", identity)
		}
		identities[identity] = struct{}{}
	}
	if err := validateRuntimeGoroutineLaunches(inventory.GoroutineLaunches, ids); err != nil {
		return err
	}
	return nil
}

func validateRuntimeGoroutineLaunches(launches []RuntimeGoroutineLaunch, componentIDs map[string]struct{}) error {
	if len(launches) == 0 {
		return errors.New("runtime inventory goroutine_launches must not be empty")
	}
	identities := make([]string, 0, len(launches))
	for index, launch := range launches {
		if err := validateRuntimeGoroutineLaunch(index, launch, componentIDs); err != nil {
			return err
		}
		identities = append(identities, runtimeIdentity(launch.File, launch.Function, launch.Site))
	}
	if err := validateSortedUnique("runtime goroutine launch", identities); err != nil {
		return err
	}
	return nil
}

func validateRuntimeGoroutineLaunch(index int, launch RuntimeGoroutineLaunch, componentIDs map[string]struct{}) error {
	if err := validateRuntimePath(launch.File); err != nil {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: %w", index, err)
	}
	if strings.TrimSpace(launch.Function) == "" {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: function must not be empty", index)
	}
	if !goroutineSiteName.MatchString(launch.Site) {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: site %q must use go#N", index, launch.Site)
	}
	if launch.Callee == "" || launch.Callee != strings.TrimSpace(launch.Callee) || strings.ContainsAny(launch.Callee, "\r\n") {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: callee must be a non-empty single-line value", index)
	}
	if !slices.Contains(goroutineDispositions, launch.Disposition) {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: disposition %q must be one of %v", index, launch.Disposition, goroutineDispositions)
	}
	if launch.Disposition == "component" {
		return validateRuntimeGoroutineComponentLink(index, launch, componentIDs)
	}
	return validateRuntimeGoroutineExemption(index, launch)
}

func validateRuntimeGoroutineComponentLink(index int, launch RuntimeGoroutineLaunch, componentIDs map[string]struct{}) error {
	if len(launch.ComponentIDs) == 0 {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: component disposition requires at least one component_id", index)
	}
	if err := validateSortedUnique("runtime goroutine component link", launch.ComponentIDs); err != nil {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: %w", index, err)
	}
	for _, componentID := range launch.ComponentIDs {
		if !runtimeComponentID.MatchString(componentID) {
			return fmt.Errorf("runtime inventory goroutine_launches[%d]: component_id %q must be lowercase kebab-case", index, componentID)
		}
		if _, ok := componentIDs[componentID]; !ok {
			return fmt.Errorf("runtime inventory goroutine_launches[%d]: component_id %q does not resolve to a lifecycle component", index, componentID)
		}
	}
	if launch.Reason != "" {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: component disposition must not include an exemption reason", index)
	}
	return nil
}

func validateRuntimeGoroutineExemption(index int, launch RuntimeGoroutineLaunch) error {
	if len(launch.ComponentIDs) != 0 {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: %s disposition must not include component_ids", index, launch.Disposition)
	}
	if launch.Reason == "" || launch.Reason != strings.TrimSpace(launch.Reason) || strings.ContainsAny(launch.Reason, "\r\n") {
		return fmt.Errorf("runtime inventory goroutine_launches[%d]: %s disposition requires a non-empty single-line reason", index, launch.Disposition)
	}
	return nil
}

func validateRuntimeComponent(component RuntimeComponent) error {
	if !runtimeComponentID.MatchString(component.ID) {
		return fmt.Errorf("id %q must be a lowercase kebab-case identifier", component.ID)
	}
	if component.Kind != "ticker" && component.Kind != "component" {
		return fmt.Errorf("component %q kind %q must be ticker or component", component.ID, component.Kind)
	}
	if err := validateRuntimePath(component.File); err != nil {
		return fmt.Errorf("component %q: %w", component.ID, err)
	}
	if strings.TrimSpace(component.Function) == "" {
		return fmt.Errorf("component %q function must not be empty", component.ID)
	}
	if component.Kind == "ticker" {
		if !tickerSiteName.MatchString(component.Site) {
			return fmt.Errorf("component %q ticker site %q must use time.NewTicker#N", component.ID, component.Site)
		}
	} else if !strings.HasPrefix(component.Site, "component:") || strings.TrimSpace(strings.TrimPrefix(component.Site, "component:")) == "" {
		return fmt.Errorf("component %q non-ticker site %q must use component:<name>", component.ID, component.Site)
	}
	required := []struct {
		name  string
		value string
	}{
		{name: "capability", value: component.Capability},
		{name: "owner", value: component.Owner},
		{name: "cadence", value: component.Cadence},
		{name: "cancellation", value: component.Cancellation},
		{name: "readiness_health", value: component.ReadinessHealth},
		{name: "retry_backoff", value: component.RetryBackoff},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("component %q %s must not be empty", component.ID, field.name)
		}
	}
	if !slices.Contains(runtimeClasses, component.Classification) {
		return fmt.Errorf("component %q classification %q must be one of %v", component.ID, component.Classification, runtimeClasses)
	}
	return nil
}

func validateRuntimePath(path string) error {
	if path == "" || path != filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) || strings.HasPrefix(path, "/") || !strings.HasSuffix(path, ".go") {
		return fmt.Errorf("file %q must be a clean relative Go path", path)
	}
	if strings.HasSuffix(path, "_test.go") {
		return fmt.Errorf("file %q must be production source, not a test file", path)
	}
	for _, root := range productionRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return nil
		}
	}
	return fmt.Errorf("file %q is outside production roots %v", path, productionRoots)
}

// ScanProductionTickerSites parses production Go source and returns every
// non-generated time.NewTicker call with a whitespace-independent identity.
func ScanProductionTickerSites(root string, inventory RuntimeInventory) ([]TickerSite, error) {
	if err := inventory.Validate(); err != nil {
		return nil, err
	}
	excluded := make(map[string]struct{}, len(inventory.Exclusions.Directories))
	for _, name := range inventory.Exclusions.Directories {
		excluded[name] = struct{}{}
	}

	var sites []TickerSite
	for _, sourceRoot := range inventory.ProductionRoots {
		rootSites, err := scanTickerRoot(root, sourceRoot, inventory.Exclusions, excluded)
		if err != nil {
			return nil, err
		}
		sites = append(sites, rootSites...)
	}
	slices.SortFunc(sites, func(a, b TickerSite) int {
		return strings.Compare(runtimeIdentity(a.File, a.Function, a.Site), runtimeIdentity(b.File, b.Function, b.Site))
	})
	return sites, nil
}

// ScanProductionGoroutineSites parses every in-scope non-test Go source variant and
// returns every go statement with a whitespace- and line-independent identity.
// This is intentionally broader than the named component ledger: short-lived
// request helpers remain visible in the exact launch-site ratchet instead of
// becoming an unreviewed escape hatch for background work.
func ScanProductionGoroutineSites(root string, inventory RuntimeInventory) ([]GoroutineSite, error) {
	if err := inventory.Validate(); err != nil {
		return nil, err
	}
	excluded := make(map[string]struct{}, len(inventory.Exclusions.Directories))
	for _, name := range inventory.Exclusions.Directories {
		excluded[name] = struct{}{}
	}

	var sites []GoroutineSite
	for _, sourceRoot := range inventory.ProductionRoots {
		rootSites, err := scanGoroutineRoot(root, sourceRoot, inventory.Exclusions, excluded)
		if err != nil {
			return nil, err
		}
		sites = append(sites, rootSites...)
	}
	slices.SortFunc(sites, func(a, b GoroutineSite) int {
		return strings.Compare(
			runtimeIdentity(a.File, a.Function, a.Site),
			runtimeIdentity(b.File, b.Function, b.Site),
		)
	})
	return sites, nil
}

func scanTickerRoot(root, sourceRoot string, exclusions RuntimeExclusions, excluded map[string]struct{}) ([]TickerSite, error) {
	base := filepath.Join(root, filepath.FromSlash(sourceRoot))
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect production root %s: %w", sourceRoot, err)
	}

	var sites []TickerSite
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if _, skip := excluded[entry.Name()]; skip && path != base {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || exclusions.TestFiles && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path) //nolint:gosec // WalkDir constrains files to validated production roots.
		if err != nil {
			return err
		}
		if exclusions.GeneratedFiles && generated(contents) {
			return nil
		}
		fileSites, err := scanTickerFile(root, path, contents)
		if err != nil {
			return err
		}
		sites = append(sites, fileSites...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan production root %s: %w", sourceRoot, err)
	}
	return sites, nil
}

func scanGoroutineRoot(root, sourceRoot string, exclusions RuntimeExclusions, excluded map[string]struct{}) ([]GoroutineSite, error) {
	base := filepath.Join(root, filepath.FromSlash(sourceRoot))
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect production root %s: %w", sourceRoot, err)
	}

	var sites []GoroutineSite
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if _, skip := excluded[entry.Name()]; skip && path != base {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || exclusions.TestFiles && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path) //nolint:gosec // WalkDir constrains files to validated production roots.
		if err != nil {
			return err
		}
		if exclusions.GeneratedFiles && generated(contents) {
			return nil
		}
		fileSites, err := scanGoroutineFile(root, path, contents)
		if err != nil {
			return err
		}
		sites = append(sites, fileSites...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan production root %s: %w", sourceRoot, err)
	}
	return sites, nil
}

func scanTickerFile(root, path string, contents []byte) ([]TickerSite, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, contents, 0)
	if err != nil {
		return nil, fmt.Errorf("parse ticker sites in %s: %w", path, err)
	}
	timeAliases, dotImport, err := timeImportAliases(parsed)
	if err != nil || len(timeAliases) == 0 && !dotImport {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	var sites []TickerSite
	var packagePositions []token.Pos
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			positions := tickerPositions(fn.Body, timeAliases, dotImport)
			sites = appendTickerSites(sites, rel, runtimeFunctionName(fn), positions, fset)
			continue
		}
		packagePositions = append(packagePositions, tickerPositions(decl, timeAliases, dotImport)...)
	}
	slices.Sort(packagePositions)
	sites = appendTickerSites(sites, rel, "<package>", packagePositions, fset)
	return sites, nil
}

func scanGoroutineFile(root, path string, contents []byte) ([]GoroutineSite, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, contents, 0)
	if err != nil {
		return nil, fmt.Errorf("parse goroutine launches in %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	var sites []GoroutineSite
	var packagePositions []goroutineLaunchExpression
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			launches, err := goroutineLaunchExpressions(function.Body, fset)
			if err != nil {
				return nil, fmt.Errorf("identify goroutine callees in %s: %w", path, err)
			}
			sites = appendGoroutineSites(sites, rel, runtimeFunctionName(function), launches, fset)
			continue
		}
		launches, err := goroutineLaunchExpressions(declaration, fset)
		if err != nil {
			return nil, fmt.Errorf("identify goroutine callees in %s: %w", path, err)
		}
		packagePositions = append(packagePositions, launches...)
	}
	slices.SortFunc(packagePositions, func(a, b goroutineLaunchExpression) int {
		return int(a.Position - b.Position)
	})
	return appendGoroutineSites(sites, rel, "<package>", packagePositions, fset), nil
}

type goroutineLaunchExpression struct {
	Position token.Pos
	Callee   string
}

func appendGoroutineSites(sites []GoroutineSite, file, function string, launches []goroutineLaunchExpression, fset *token.FileSet) []GoroutineSite {
	for index, launch := range launches {
		sites = append(sites, GoroutineSite{
			RuntimeGoroutineLaunch: RuntimeGoroutineLaunch{
				File: file, Function: function, Site: fmt.Sprintf("go#%d", index+1), Callee: launch.Callee,
			},
			Line: fset.Position(launch.Position).Line,
		})
	}
	return sites
}

func goroutineLaunchExpressions(node ast.Node, fset *token.FileSet) ([]goroutineLaunchExpression, error) {
	var launches []goroutineLaunchExpression
	var renderErr error
	if node == nil {
		return launches, nil
	}
	ast.Inspect(node, func(candidate ast.Node) bool {
		if renderErr != nil {
			return false
		}
		statement, ok := candidate.(*ast.GoStmt)
		if ok {
			callee, err := renderGoroutineCallee(statement.Call.Fun, fset)
			if err != nil {
				renderErr = err
				return false
			}
			launches = append(launches, goroutineLaunchExpression{Position: statement.Pos(), Callee: callee})
		}
		return true
	})
	if renderErr != nil {
		return nil, renderErr
	}
	slices.SortFunc(launches, func(a, b goroutineLaunchExpression) int {
		return int(a.Position - b.Position)
	})
	return launches, nil
}

func renderGoroutineCallee(expression ast.Expr, fset *token.FileSet) (string, error) {
	unwrapped := expression
	for {
		parenthesized, ok := unwrapped.(*ast.ParenExpr)
		if !ok {
			break
		}
		unwrapped = parenthesized.X
	}
	if _, anonymous := unwrapped.(*ast.FuncLit); anonymous {
		return "<anonymous>", nil
	}
	var rendered bytes.Buffer
	if err := format.Node(&rendered, fset, expression); err != nil {
		return "", err
	}
	callee := strings.TrimSpace(rendered.String())
	if callee == "" || strings.ContainsAny(callee, "\r\n") {
		return "", fmt.Errorf("goroutine callee %q is not a non-empty single-line expression", callee)
	}
	return callee, nil
}

func appendTickerSites(sites []TickerSite, file, function string, positions []token.Pos, fset *token.FileSet) []TickerSite {
	for i, position := range positions {
		sites = append(sites, TickerSite{
			File:     file,
			Function: function,
			Site:     fmt.Sprintf("time.NewTicker#%d", i+1),
			Line:     fset.Position(position).Line,
		})
	}
	return sites
}

func timeImportAliases(file *ast.File) (map[string]struct{}, bool, error) {
	aliases := map[string]struct{}{}
	dotImport := false
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, false, err
		}
		if path != "time" {
			continue
		}
		if spec.Name == nil {
			aliases["time"] = struct{}{}
			continue
		}
		switch spec.Name.Name {
		case ".":
			dotImport = true
		case "_":
			// A blank import cannot provide NewTicker.
		default:
			aliases[spec.Name.Name] = struct{}{}
		}
	}
	return aliases, dotImport, nil
}

func tickerPositions(node ast.Node, aliases map[string]struct{}, dotImport bool) []token.Pos {
	var positions []token.Pos
	if node == nil {
		return positions
	}
	ast.Inspect(node, func(candidate ast.Node) bool {
		call, ok := candidate.(*ast.CallExpr)
		if !ok || !isNewTickerCall(call.Fun, aliases, dotImport) {
			return true
		}
		positions = append(positions, call.Pos())
		return true
	})
	slices.Sort(positions)
	return positions
}

func isNewTickerCall(expression ast.Expr, aliases map[string]struct{}, dotImport bool) bool {
	if dotImport {
		if ident, ok := expression.(*ast.Ident); ok && ident.Name == "NewTicker" {
			return true
		}
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewTicker" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = aliases[ident.Name]
	return ok
}

func runtimeFunctionName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverBaseName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverBaseName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverBaseName(value.X)
	case *ast.IndexExpr:
		return receiverBaseName(value.X)
	case *ast.IndexListExpr:
		return receiverBaseName(value.X)
	default:
		return "<receiver>"
	}
}

// CompareRuntimeTickerInventory enforces exact parity between the AST and both
// checked-in ticker records and in-scope non-test goroutine launch definitions. It also
// proves that every explicitly registered non-ticker component still points at
// a real production function. Together with the component-count ratchet, this
// makes runtime-site additions and removals intentional inventory changes.
func CompareRuntimeTickerInventory(root string, inventory RuntimeInventory) error {
	observed, err := ScanProductionTickerSites(root, inventory)
	if err != nil {
		return err
	}
	observedGoroutines, err := ScanProductionGoroutineSites(root, inventory)
	if err != nil {
		return err
	}
	mismatches := compareRuntimeTickerSites(observed, inventory.Components)
	mismatches = append(mismatches, compareRuntimeGoroutineSites(observedGoroutines, inventory.GoroutineLaunches)...)
	mismatches = append(mismatches, runtimeComponentReferenceMismatches(root, inventory.Components)...)
	slices.Sort(mismatches)
	if len(mismatches) > 0 {
		return errors.New("runtime inventory mismatch:\n- " + strings.Join(mismatches, "\n- "))
	}
	return nil
}

func compareRuntimeTickerSites(observed []TickerSite, components []RuntimeComponent) []string {
	expected := make(map[string]RuntimeComponent)
	for _, component := range components {
		if component.Kind == "ticker" {
			expected[runtimeIdentity(component.File, component.Function, component.Site)] = component
		}
	}
	actual := make(map[string]TickerSite, len(observed))
	for _, site := range observed {
		actual[runtimeIdentity(site.File, site.Function, site.Site)] = site
	}

	var mismatches []string
	for identity, site := range actual {
		if _, ok := expected[identity]; !ok {
			mismatches = append(mismatches, fmt.Sprintf("missing ticker %s (line %d)", identity, site.Line))
		}
	}
	for identity, component := range expected {
		if _, ok := actual[identity]; !ok {
			mismatches = append(mismatches, fmt.Sprintf("stale ticker %s (component %s)", identity, component.ID))
		}
	}
	return mismatches
}

func compareRuntimeGoroutineSites(observed []GoroutineSite, expectedLaunches []RuntimeGoroutineLaunch) []string {
	expectedGoroutines := make(map[string]RuntimeGoroutineLaunch, len(expectedLaunches))
	for _, launch := range expectedLaunches {
		expectedGoroutines[runtimeIdentity(launch.File, launch.Function, launch.Site)] = launch
	}
	actualGoroutines := make(map[string]GoroutineSite, len(observed))
	mismatches := []string{}
	for _, launch := range observed {
		identity := runtimeIdentity(launch.File, launch.Function, launch.Site)
		actualGoroutines[identity] = launch
		expectedLaunch, ok := expectedGoroutines[identity]
		if !ok {
			mismatches = append(mismatches, fmt.Sprintf("missing goroutine launch %s (line %d)", identity, launch.Line))
		} else if expectedLaunch.Callee != launch.Callee {
			mismatches = append(mismatches, fmt.Sprintf("goroutine launch %s callee changed from %q to %q (line %d)", identity, expectedLaunch.Callee, launch.Callee, launch.Line))
		}
	}
	for identity := range expectedGoroutines {
		if _, ok := actualGoroutines[identity]; !ok {
			mismatches = append(mismatches, fmt.Sprintf("stale goroutine launch %s", identity))
		}
	}
	return mismatches
}

func runtimeComponentReferenceMismatches(root string, components []RuntimeComponent) []string {
	mismatches := []string{}
	for _, component := range components {
		if component.Kind != "component" {
			continue
		}
		if err := verifyRuntimeComponentReference(root, component); err != nil {
			mismatches = append(mismatches, err.Error())
		}
	}
	return mismatches
}

func verifyRuntimeComponentReference(root string, component RuntimeComponent) error {
	path := filepath.Join(root, filepath.FromSlash(component.File))
	contents, err := os.ReadFile(path) //nolint:gosec // Component paths are validated relative production paths.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stale component %s (component %s): file does not exist", runtimeIdentity(component.File, component.Function, component.Site), component.ID)
		}
		return fmt.Errorf("inspect component %s: %w", component.ID, err)
	}
	if generated(contents) {
		return fmt.Errorf("stale component %s (component %s): file is generated", runtimeIdentity(component.File, component.Function, component.Site), component.ID)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, contents, 0)
	if err != nil {
		return fmt.Errorf("parse component %s source: %w", component.ID, err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && runtimeFunctionName(function) == component.Function {
			return nil
		}
	}
	return fmt.Errorf("stale component %s (component %s): function does not exist", runtimeIdentity(component.File, component.Function, component.Site), component.ID)
}

func runtimeIdentity(file, function, site string) string {
	return file + "::" + function + "::" + site
}
