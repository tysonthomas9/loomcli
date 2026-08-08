package archtest

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

const (
	productionPackageShapeSchemaVersion = 1
	packageShapeRoot                    = "internal"
	packageShapeGeneratedFilesPolicy    = "include-for-package-topology"
)

var packageShapeExcludedDirectories = []string{
	".git",
	"node_modules",
	"third_party",
	"vendor",
	"worktrees",
}

// ProductionPackageShapeInventory is the exact checked-in set of production
// Go package directories under internal plus fragmentation ceilings for that
// set. Package paths are repository-relative and sorted.
//
// A package directory exists when it contains at least one non-test .go file
// in any build-tag variant. Generated .go files intentionally count here: a
// generated-only package still creates a compiled import boundary and is part
// of the repository topology. This differs from semantic declaration scans,
// which may exclude generated files with generated(). _test.go files never
// create a production package or contribute to its file count.
type ProductionPackageShapeInventory struct {
	SchemaVersion int                             `yaml:"schema_version"`
	Semantics     ProductionPackageShapeSemantics `yaml:"semantics"`
	Ceilings      ProductionPackageShapeCeilings  `yaml:"ceilings"`
	Packages      []string                        `yaml:"packages"`
}

// ProductionPackageShapeSemantics makes the source boundary explicit in the
// checked-in artifact so a future edit cannot silently weaken the scan.
type ProductionPackageShapeSemantics struct {
	Root                string   `yaml:"root"`
	ExcludeTestFiles    bool     `yaml:"exclude_test_files"`
	GeneratedFiles      string   `yaml:"generated_files"`
	ExcludedDirectories []string `yaml:"excluded_directories"`
}

// ProductionPackageShapeCeilings prevents package-count and small-package
// fragmentation from increasing. Total, module, and outside-module ceilings
// also have to equal the exact approved package inventory.
type ProductionPackageShapeCeilings struct {
	TotalPackages         int `yaml:"total_packages"`
	ModulePackages        int `yaml:"module_packages"`
	OutsideModulePackages int `yaml:"outside_module_packages"`
	OneFilePackages       int `yaml:"one_file_packages"`
	OneOrTwoFilePackages  int `yaml:"one_or_two_file_packages"`
}

// ProductionPackageShape is the mechanically observed repository shape.
type ProductionPackageShape struct {
	Packages              []string
	TotalPackages         int
	ModulePackages        int
	OutsideModulePackages int
	OneFilePackages       int
	OneOrTwoFilePackages  int
}

// LoadProductionPackageShapeInventory strictly decodes and validates the
// checked-in package-shape baseline.
func LoadProductionPackageShapeInventory(path string) (ProductionPackageShapeInventory, error) {
	var inventory ProductionPackageShapeInventory
	if err := decodeYAML(path, &inventory); err != nil {
		return ProductionPackageShapeInventory{}, fmt.Errorf("decode production package shape inventory: %w", err)
	}
	if err := inventory.Validate(); err != nil {
		return ProductionPackageShapeInventory{}, err
	}
	return inventory, nil
}

// Validate rejects malformed inventories and attempts to narrow the source
// walk. Keeping the package-list counts equal to their ceilings ensures that a
// package deletion requires an intentional baseline refresh rather than
// leaving a stale approval behind.
func (inventory ProductionPackageShapeInventory) Validate() error {
	if inventory.SchemaVersion != productionPackageShapeSchemaVersion {
		return fmt.Errorf("production package shape schema_version: got %d, want %d", inventory.SchemaVersion, productionPackageShapeSchemaVersion)
	}
	if err := inventory.Semantics.validate(); err != nil {
		return err
	}
	if len(inventory.Packages) == 0 {
		return errors.New("production package shape inventory must not be empty")
	}
	for _, path := range inventory.Packages {
		if !cleanInternalPackagePath(path) {
			return fmt.Errorf("production package shape path %q must be a clean repository-relative path under internal", path)
		}
	}
	if err := validateSortedUnique("production package shape package", inventory.Packages); err != nil {
		return err
	}
	return inventory.Ceilings.validate(inventory.Packages)
}

func (semantics ProductionPackageShapeSemantics) validate() error {
	if semantics.Root != packageShapeRoot {
		return fmt.Errorf("production package shape root: got %q, want %q", semantics.Root, packageShapeRoot)
	}
	if !semantics.ExcludeTestFiles {
		return errors.New("production package shape must exclude _test.go files")
	}
	if semantics.GeneratedFiles != packageShapeGeneratedFilesPolicy {
		return fmt.Errorf("production package shape generated_files: got %q, want %q", semantics.GeneratedFiles, packageShapeGeneratedFilesPolicy)
	}
	if !slices.Equal(semantics.ExcludedDirectories, packageShapeExcludedDirectories) {
		return fmt.Errorf("production package shape excluded_directories: got %v, want %v", semantics.ExcludedDirectories, packageShapeExcludedDirectories)
	}
	return nil
}

func (ceilings ProductionPackageShapeCeilings) validate(packages []string) error {
	values := []struct {
		name  string
		value int
	}{
		{name: "total_packages", value: ceilings.TotalPackages},
		{name: "module_packages", value: ceilings.ModulePackages},
		{name: "outside_module_packages", value: ceilings.OutsideModulePackages},
		{name: "one_file_packages", value: ceilings.OneFilePackages},
		{name: "one_or_two_file_packages", value: ceilings.OneOrTwoFilePackages},
	}
	for _, field := range values {
		if field.value < 0 {
			return fmt.Errorf("production package shape ceiling %s must not be negative", field.name)
		}
	}
	if ceilings.TotalPackages == 0 {
		return errors.New("production package shape total_packages must be positive")
	}
	if ceilings.ModulePackages+ceilings.OutsideModulePackages != ceilings.TotalPackages {
		return errors.New("production package shape module and outside-module ceilings must sum to total_packages")
	}
	if ceilings.OneFilePackages > ceilings.OneOrTwoFilePackages || ceilings.OneOrTwoFilePackages > ceilings.TotalPackages {
		return errors.New("production package shape small-package ceilings must satisfy one_file <= one_or_two_file <= total")
	}

	modulePackages := 0
	for _, path := range packages {
		if packageShapeUnderModules(path) {
			modulePackages++
		}
	}
	if got := len(packages); got != ceilings.TotalPackages {
		return fmt.Errorf("production package shape inventory has %d packages, want total_packages ceiling %d", got, ceilings.TotalPackages)
	}
	if modulePackages != ceilings.ModulePackages {
		return fmt.Errorf("production package shape inventory has %d module packages, want module_packages ceiling %d", modulePackages, ceilings.ModulePackages)
	}
	if outside := len(packages) - modulePackages; outside != ceilings.OutsideModulePackages {
		return fmt.Errorf("production package shape inventory has %d outside-module packages, want outside_module_packages ceiling %d", outside, ceilings.OutsideModulePackages)
	}
	return nil
}

// ScanProductionPackageShape walks every production Go source variant below
// internal and returns its package-directory topology. It does not parse files:
// generated files are topology, while tests and vendor-like trees are excluded
// according to the validated, checked-in semantics.
func ScanProductionPackageShape(root string, semantics ProductionPackageShapeSemantics) (ProductionPackageShape, error) {
	if err := semantics.validate(); err != nil {
		return ProductionPackageShape{}, err
	}
	base := filepath.Join(root, filepath.FromSlash(semantics.Root))
	excluded := make(map[string]struct{}, len(semantics.ExcludedDirectories))
	for _, name := range semantics.ExcludedDirectories {
		excluded[name] = struct{}{}
	}

	fileCounts := map[string]int{}
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != base {
				if _, skip := excluded[entry.Name()]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || semantics.ExcludeTestFiles && strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relativeDirectory, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		fileCounts[filepath.ToSlash(relativeDirectory)]++
		return nil
	})
	if err != nil {
		return ProductionPackageShape{}, fmt.Errorf("scan production package shape: %w", err)
	}
	return productionPackageShapeFromFileCounts(fileCounts), nil
}

func productionPackageShapeFromFileCounts(fileCounts map[string]int) ProductionPackageShape {
	shape := ProductionPackageShape{Packages: make([]string, 0, len(fileCounts))}
	for path, count := range fileCounts {
		shape.Packages = append(shape.Packages, path)
		if packageShapeUnderModules(path) {
			shape.ModulePackages++
		} else {
			shape.OutsideModulePackages++
		}
		if count == 1 {
			shape.OneFilePackages++
		}
		if count <= 2 {
			shape.OneOrTwoFilePackages++
		}
	}
	slices.Sort(shape.Packages)
	shape.TotalPackages = len(shape.Packages)
	return shape
}

// CompareProductionPackageShape enforces both halves of the ratchet: the
// observed package paths must exactly match the reviewed inventory, and no
// aggregate fragmentation metric may exceed its checked-in ceiling.
func CompareProductionPackageShape(root string, inventory ProductionPackageShapeInventory) error {
	if err := inventory.Validate(); err != nil {
		return err
	}
	observed, err := ScanProductionPackageShape(root, inventory.Semantics)
	if err != nil {
		return err
	}
	violations := productionPackageShapeViolations(observed, inventory)
	if len(violations) != 0 {
		return errors.New("production package shape mismatch:\n- " + strings.Join(violations, "\n- "))
	}
	return nil
}

func productionPackageShapeViolations(observed ProductionPackageShape, inventory ProductionPackageShapeInventory) []string {
	approved := make(map[string]struct{}, len(inventory.Packages))
	actual := make(map[string]struct{}, len(observed.Packages))
	for _, path := range inventory.Packages {
		approved[path] = struct{}{}
	}
	for _, path := range observed.Packages {
		actual[path] = struct{}{}
	}

	var violations []string
	for _, path := range observed.Packages {
		if _, ok := approved[path]; !ok {
			// A count-neutral package rename is still an architectural change.
			// Require it to update the exact inventory.
			violations = append(violations, fmt.Sprintf("unapproved production package %s", path))
		}
	}
	for _, path := range inventory.Packages {
		if _, ok := actual[path]; !ok {
			violations = append(violations, fmt.Sprintf("stale/deleted production package inventory entry %s", path))
		}
	}
	return append(violations, productionPackageShapeCeilingViolations(observed, inventory.Ceilings)...)
}

func productionPackageShapeCeilingViolations(observed ProductionPackageShape, ceilings ProductionPackageShapeCeilings) []string {
	metrics := []struct {
		name    string
		value   int
		ceiling int
	}{
		{name: "total package directories", value: observed.TotalPackages, ceiling: ceilings.TotalPackages},
		{name: "packages under internal/modules", value: observed.ModulePackages, ceiling: ceilings.ModulePackages},
		{name: "packages outside internal/modules", value: observed.OutsideModulePackages, ceiling: ceilings.OutsideModulePackages},
		{name: "one-file package directories", value: observed.OneFilePackages, ceiling: ceilings.OneFilePackages},
		{name: "one-or-two-file package directories", value: observed.OneOrTwoFilePackages, ceiling: ceilings.OneOrTwoFilePackages},
	}
	var violations []string
	for _, metric := range metrics {
		if metric.value > metric.ceiling {
			violations = append(violations, fmt.Sprintf("%s = %d exceeds ceiling %d", metric.name, metric.value, metric.ceiling))
		}
	}
	return violations
}

// rebaselineProductionPackageShape removes stale approvals and lowers the
// ceilings after consolidation. It deliberately refuses to approve a new
// package or raise any ceiling; those are architecture decisions, not a
// mechanical refresh.
func rebaselineProductionPackageShape(
	inventory ProductionPackageShapeInventory,
	observed ProductionPackageShape,
) (ProductionPackageShapeInventory, error) {
	if err := inventory.Validate(); err != nil {
		return ProductionPackageShapeInventory{}, err
	}
	approved := make(map[string]struct{}, len(inventory.Packages))
	for _, path := range inventory.Packages {
		approved[path] = struct{}{}
	}
	var additions []string
	for _, path := range observed.Packages {
		if _, ok := approved[path]; !ok {
			additions = append(additions, path)
		}
	}
	if len(additions) != 0 {
		return ProductionPackageShapeInventory{}, fmt.Errorf("refuse to approve new production packages during mechanical refresh: %v", additions)
	}
	if violations := productionPackageShapeCeilingViolations(observed, inventory.Ceilings); len(violations) != 0 {
		return ProductionPackageShapeInventory{}, errors.New("refuse to raise production package shape ceilings during mechanical refresh:\n- " + strings.Join(violations, "\n- "))
	}

	refreshed := inventory
	refreshed.Packages = slices.Clone(observed.Packages)
	refreshed.Ceilings = ProductionPackageShapeCeilings{
		TotalPackages:         observed.TotalPackages,
		ModulePackages:        observed.ModulePackages,
		OutsideModulePackages: observed.OutsideModulePackages,
		OneFilePackages:       observed.OneFilePackages,
		OneOrTwoFilePackages:  observed.OneOrTwoFilePackages,
	}
	if err := refreshed.Validate(); err != nil {
		return ProductionPackageShapeInventory{}, fmt.Errorf("validate refreshed production package shape: %w", err)
	}
	return refreshed, nil
}

func cleanInternalPackagePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, `\`) {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return path == cleaned && (path == packageShapeRoot || strings.HasPrefix(path, packageShapeRoot+"/"))
}

func packageShapeUnderModules(path string) bool {
	return path == "internal/modules" || strings.HasPrefix(path, "internal/modules/")
}
