package archtest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func (i DirectWriteInventory) validateGenericMechanisms() error {
	wantMechanisms := []string{"action_ledger", "lease"}
	wantAdapterRoots := map[string][]string{
		"action_ledger": {"internal/modules/execution/fleetdb"},
		"lease": {
			"internal/modules/agents/fleetdb",
			"internal/modules/execution/fleetdb",
			"internal/modules/interaction/fleetdb",
		},
	}
	gotMechanisms := make([]string, 0, len(i.GenericMechanisms))
	for _, mechanism := range i.GenericMechanisms {
		if mechanism.DirectUses != 0 || mechanism.OwnerMapping == "" || mechanism.Enforcement == "" || mechanism.AllowedAdapterRoots == nil {
			return fmt.Errorf("generic mechanism %s must record zero direct uses plus owner mapping, allowed adapter roots, and enforcement", mechanism.Mechanism)
		}
		if err := validateSortedUnique("generic mechanism "+mechanism.Mechanism+" allowed adapter root", mechanism.AllowedAdapterRoots); err != nil {
			return err
		}
		if !slices.Equal(mechanism.AllowedAdapterRoots, wantAdapterRoots[mechanism.Mechanism]) {
			return fmt.Errorf("generic mechanism %s allowed adapter roots: got %v, want %v", mechanism.Mechanism, mechanism.AllowedAdapterRoots, wantAdapterRoots[mechanism.Mechanism])
		}
		gotMechanisms = append(gotMechanisms, mechanism.Mechanism)
	}
	if !slices.Equal(gotMechanisms, wantMechanisms) {
		return fmt.Errorf("generic mechanisms: got %v, want %v", gotMechanisms, wantMechanisms)
	}
	return nil
}

func (i DirectWriteInventory) validatePersistencePolicy() error {
	if err := validateSortedUnique("direct-write candidate receiver suffix", i.CandidateReceiverSuffixes); err != nil {
		return err
	}
	if len(i.CandidateReceiverSuffixes) == 0 {
		return errors.New("direct-write inventory requires candidate receiver suffixes for undeclared internal persistence surfaces")
	}
	packagesByPath, err := validatePersistencePackages(i.PersistencePackages)
	if err != nil {
		return err
	}
	methodSetsByName, err := validatePersistenceMethodSets(i.MethodSets)
	if err != nil {
		return err
	}
	if err := validatePersistenceReceiverSurfaces(
		i.ReceiverSurfaces,
		packagesByPath,
		methodSetsByName,
		i.CandidateReceiverSuffixes,
	); err != nil {
		return err
	}
	return validatePersistenceFunctionSurfaces(i.FunctionSurfaces, packagesByPath)
}

func validatePersistencePackages(packages []PersistencePackage) (map[string]PersistencePackage, error) {
	if len(packages) == 0 {
		return nil, errors.New("direct-write inventory requires declared persistence packages")
	}
	packagePaths := make([]string, 0, len(packages))
	packagesByPath := make(map[string]PersistencePackage, len(packages))
	for _, pkg := range packages {
		if !strings.HasPrefix(pkg.Path, modulePath+"/internal/") {
			return nil, fmt.Errorf("direct-write persistence package %q must be below the module internal root", pkg.Path)
		}
		if pkg.ReceiverNames == nil || pkg.ReceiverSuffixes == nil || len(pkg.ReceiverNames)+len(pkg.ReceiverSuffixes) == 0 {
			return nil, fmt.Errorf("direct-write persistence package %s requires explicit receiver_names and receiver_suffixes", pkg.Path)
		}
		if err := validateSortedUnique("direct-write persistence receiver name", pkg.ReceiverNames); err != nil {
			return nil, err
		}
		if err := validateSortedUnique("direct-write persistence receiver suffix", pkg.ReceiverSuffixes); err != nil {
			return nil, err
		}
		packagePaths = append(packagePaths, pkg.Path)
		packagesByPath[pkg.Path] = pkg
	}
	if err := validateSortedUnique("direct-write persistence package", packagePaths); err != nil {
		return nil, err
	}
	return packagesByPath, nil
}

func validatePersistenceMethodSets(methodSets []PersistenceMethodSet) (map[string]PersistenceMethodSet, error) {
	methodSetNames := make([]string, 0, len(methodSets))
	methodSetsByName := make(map[string]PersistenceMethodSet, len(methodSets))
	for _, methodSet := range methodSets {
		if methodSet.Name == "" || methodSet.ReadOnly == nil || methodSet.Mutating == nil || len(methodSet.ReadOnly)+len(methodSet.Mutating) == 0 {
			return nil, fmt.Errorf("direct-write method set %q requires explicit non-empty read_only or mutating classifications", methodSet.Name)
		}
		if err := validateSortedUnique("direct-write "+methodSet.Name+" read-only method", methodSet.ReadOnly); err != nil {
			return nil, err
		}
		if err := validateSortedUnique("direct-write "+methodSet.Name+" mutating method", methodSet.Mutating); err != nil {
			return nil, err
		}
		readOnly := sliceSet(methodSet.ReadOnly)
		for _, method := range methodSet.Mutating {
			if _, duplicate := readOnly[method]; duplicate {
				return nil, fmt.Errorf("direct-write method set %s classifies %s as both read-only and mutating", methodSet.Name, method)
			}
		}
		methodSetNames = append(methodSetNames, methodSet.Name)
		methodSetsByName[methodSet.Name] = methodSet
	}
	if err := validateSortedUnique("direct-write method set", methodSetNames); err != nil {
		return nil, err
	}
	return methodSetsByName, nil
}

func validatePersistenceReceiverSurfaces(
	surfaces []PersistenceReceiverSurface,
	packagesByPath map[string]PersistencePackage,
	methodSetsByName map[string]PersistenceMethodSet,
	candidateReceiverSuffixes []string,
) error {
	receivers := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		methodSet, ok := methodSetsByName[surface.MethodSet]
		if !ok {
			return fmt.Errorf("direct-write receiver %s uses unknown method set %s", surface.Receiver, surface.MethodSet)
		}
		receiverPackage, receiverName, ok := splitReceiver(surface.Receiver)
		if !ok || receiverPackage != surface.Package {
			return fmt.Errorf("direct-write receiver surface %s does not match package %s receiver policy", surface.Receiver, surface.Package)
		}
		if !strings.HasPrefix(surface.Package, modulePath+"/internal/") {
			return fmt.Errorf("direct-write receiver surface %s must be below the module internal root", surface.Receiver)
		}
		pkg, declaredPackage := packagesByPath[surface.Package]
		matchesCandidate := strings.HasPrefix(surface.Package, modulePath+"/internal/") && slices.ContainsFunc(
			candidateReceiverSuffixes,
			func(suffix string) bool { return strings.HasSuffix(receiverName, suffix) },
		)
		// An exact receiver surface is sufficient policy for a mixed package: it
		// keeps that receiver default-deny without misclassifying every helper in
		// the package as persistence. A declared persistence package remains
		// stricter because its receiver allowlist is itself part of the policy.
		if declaredPackage && !pkg.matchesReceiver(receiverName) && !matchesCandidate {
			return fmt.Errorf("direct-write receiver surface %s does not match package %s receiver policy", surface.Receiver, surface.Package)
		}
		if surface.CapabilityOwner == "" || surface.CapabilityOwner == "unassigned_legacy" {
			return fmt.Errorf("direct-write receiver %s requires an explicit capability owner", surface.Receiver)
		}
		if len(methodSet.Mutating) > 0 && !validPersistenceOwner(surface.CapabilityOwner) {
			return fmt.Errorf("direct-write receiver %s has unsupported mutating capability owner %s", surface.Receiver, surface.CapabilityOwner)
		}
		receivers = append(receivers, surface.Receiver)
	}
	if len(receivers) == 0 {
		return errors.New("direct-write inventory requires declared receiver surfaces")
	}
	return validateSortedUnique("direct-write receiver surface", receivers)
}

func validatePersistenceFunctionSurfaces(
	surfaces []PersistenceFunctionSurface,
	packagesByPath map[string]PersistencePackage,
) error {
	keys := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		if _, ok := packagesByPath[surface.Package]; !ok {
			return fmt.Errorf("direct-write function %s uses undeclared persistence package %s", surface.Function, surface.Package)
		}
		if surface.Function == "" {
			return errors.New("direct-write persistence function requires a name")
		}
		if surface.Access != "read-only" && surface.Access != "mutating" {
			return fmt.Errorf("direct-write persistence function %s.%s access %q must be read-only or mutating", surface.Package, surface.Function, surface.Access)
		}
		if surface.CapabilityOwner == "" || surface.CapabilityOwner == "unassigned_legacy" {
			return fmt.Errorf("direct-write persistence function %s.%s requires an explicit capability owner", surface.Package, surface.Function)
		}
		if !validPersistenceOwner(surface.CapabilityOwner) {
			return fmt.Errorf("direct-write persistence function %s.%s has unsupported capability owner %s", surface.Package, surface.Function, surface.CapabilityOwner)
		}
		keys = append(keys, persistenceFunctionKey(surface.Package, surface.Function))
	}
	return validateSortedUnique("direct-write persistence function surface", keys)
}

func persistenceFunctionKey(packagePath, function string) string {
	return packagePath + "." + function
}

func (pkg PersistencePackage) matchesReceiver(name string) bool {
	if slices.Contains(pkg.ReceiverNames, name) {
		return true
	}
	for _, suffix := range pkg.ReceiverSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func splitReceiver(receiver string) (string, string, bool) {
	named := strings.TrimPrefix(receiver, "*")
	separator := strings.LastIndex(named, ".")
	if separator <= 0 || separator == len(named)-1 {
		return "", "", false
	}
	return named[:separator], named[separator+1:], true
}

func validPersistenceOwner(owner string) bool {
	return slices.Contains([]string{
		"agents", "artifacts", "automation", "connectors", "execution", "fleet-db",
		"interaction", "legacy_tombstone", "named_application_workflow", "read_projection", "sourcecontrol",
		"workflowcatalog", "workitems", "workspace",
	}, owner)
}
