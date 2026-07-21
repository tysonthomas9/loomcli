package archtest

import (
	"fmt"
	"strings"
)

// persistenceClassifier is deliberately exhaustive and default-deny. A method
// or package function is never ignored merely because its name is absent from a
// mutator allowlist: every observed call must be declared read-only or mutating
// in the checked-in inventory for its specific persistence surface.
type persistenceClassifier struct {
	packages                  map[string]PersistencePackage
	surfaces                  map[string]persistenceSurfacePolicy
	functions                 map[string]persistenceFunctionPolicy
	candidateReceiverSuffixes []string
}

type persistenceSurfacePolicy struct {
	owner   string
	methods map[string]persistenceAccess
}

type persistenceFunctionPolicy struct {
	owner  string
	access persistenceAccess
}

func newPersistenceClassifier(inventory DirectWriteInventory) persistenceClassifier {
	classifier := persistenceClassifier{
		packages:                  make(map[string]PersistencePackage, len(inventory.PersistencePackages)),
		surfaces:                  make(map[string]persistenceSurfacePolicy, len(inventory.ReceiverSurfaces)),
		functions:                 make(map[string]persistenceFunctionPolicy, len(inventory.FunctionSurfaces)),
		candidateReceiverSuffixes: append([]string(nil), inventory.CandidateReceiverSuffixes...),
	}
	for _, pkg := range inventory.PersistencePackages {
		classifier.packages[pkg.Path] = pkg
	}
	methodSets := make(map[string]map[string]persistenceAccess, len(inventory.MethodSets))
	for _, methodSet := range inventory.MethodSets {
		methodSets[methodSet.Name] = methodAccessMap(methodSet)
	}
	for _, surface := range inventory.ReceiverSurfaces {
		classifier.surfaces[surface.Receiver] = persistenceSurfacePolicy{
			owner: surface.CapabilityOwner, methods: methodSets[surface.MethodSet],
		}
	}
	for _, surface := range inventory.FunctionSurfaces {
		// A function is classified individually: package helpers can belong to
		// different capabilities even when they live in the same legacy package.
		classifier.functions[persistenceFunctionKey(surface.Package, surface.Function)] = persistenceFunctionPolicy{
			owner: surface.CapabilityOwner, access: persistenceAccessFromLabel(surface.Access),
		}
	}
	return classifier
}

func persistenceAccessFromLabel(label string) persistenceAccess {
	if label == "mutating" {
		return persistenceMutating
	}
	return persistenceReadOnly
}

func methodAccessMap(methodSet PersistenceMethodSet) map[string]persistenceAccess {
	result := make(map[string]persistenceAccess, len(methodSet.ReadOnly)+len(methodSet.Mutating))
	for _, method := range methodSet.ReadOnly {
		result[method] = persistenceReadOnly
	}
	for _, method := range methodSet.Mutating {
		result[method] = persistenceMutating
	}
	return result
}

func (c persistenceClassifier) classify(receiver, method string) (persistenceAccess, string, bool) {
	surface, ok := c.surfaces[receiver]
	if ok {
		access, classified := surface.methods[method]
		return access, surface.owner, classified
	}
	function, ok := c.functions[persistenceFunctionKey(receiver, method)]
	if !ok {
		return 0, "", false
	}
	return function.access, function.owner, true
}

func (c persistenceClassifier) isPersistenceFunctionPackage(packagePath string) bool {
	_, ok := c.packages[packagePath]
	return ok
}

func (c persistenceClassifier) isPersistenceCandidate(packagePath, receiverName string) bool {
	if pkg, ok := c.packages[packagePath]; ok && pkg.matchesReceiver(receiverName) {
		return true
	}
	if strings.HasPrefix(packagePath, modulePath+"/internal/") {
		for _, suffix := range c.candidateReceiverSuffixes {
			if strings.HasSuffix(receiverName, suffix) {
				return true
			}
		}
	}
	for declared, pkg := range c.packages {
		if pkg.GuardSubpackages && strings.HasPrefix(packagePath, declared+"/") {
			return true
		}
	}
	return false
}

func (c persistenceClassifier) undeclaredPersistenceImport(importPath string) bool {
	if _, declared := c.packages[importPath]; declared {
		return false
	}
	for declared, pkg := range c.packages {
		if pkg.GuardSubpackages && strings.HasPrefix(importPath, declared+"/") {
			return true
		}
	}
	return false
}

func (c persistenceClassifier) unclassifiedReason(receiver, method string) string {
	if _, declared := c.surfaces[receiver]; !declared {
		if c.isPersistenceFunctionPackage(receiver) {
			return fmt.Sprintf("unclassified persistence package function %s.%s; declare it read-only or mutating", receiver, method)
		}
		return fmt.Sprintf("undeclared persistence receiver surface %s (method %s)", receiver, method)
	}
	return fmt.Sprintf("unclassified persistence method %s.%s; declare it read-only or mutating", receiver, method)
}
