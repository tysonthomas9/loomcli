package archtest

import (
	"fmt"
	"slices"
	"strings"
)

type boundaryPackageKind uint8

const (
	boundaryPackageOther boundaryPackageKind = iota
	boundaryPackageCapabilityPublic
	boundaryPackageCapabilityCore
	boundaryPackageCapabilityAdapter
	boundaryPackageUnknownCapability
	boundaryPackageAppCore
	boundaryPackageAppAdapter
	boundaryPackageComposition
	boundaryPackagePlatform
)

type boundaryPackage struct {
	kind    boundaryPackageKind
	owner   string
	adapter string
}

func forbiddenBoundaryImport(from, to string, graph CapabilityGraph) string {
	fromPackage := classifyBoundaryPackage(from, graph)
	toPackage := classifyBoundaryPackage(to, graph)

	if fromPackage.kind == boundaryPackagePlatform {
		switch {
		case toPackage.kind == boundaryPackagePlatform:
			return ""
		case isModuleInternalPath(to):
			return "platform may not import this package: product/app/legacy internals are forbidden; it may import only other platform packages, standard-library mechanisms, and reviewed external dependencies"
		default:
			return platformExternalImportViolation(to, graph.ExternalImports)
		}
	}

	if isCapabilityPackage(fromPackage.kind) {
		if reason := forbiddenCapabilityImport(to, fromPackage, toPackage, graph); reason != "" {
			return reason
		}
	}

	if fromPackage.kind == boundaryPackageAppCore || fromPackage.kind == boundaryPackageAppAdapter {
		if reason := forbiddenApplicationImport(to, fromPackage, toPackage, graph); reason != "" {
			return reason
		}
	}
	return ""
}

func forbiddenApplicationImport(
	toPath string,
	fromPackage, toPackage boundaryPackage,
	graph CapabilityGraph,
) string {
	if toPackage.kind == boundaryPackageUnknownCapability {
		return "named application workflow may import only declared capability public roots"
	}
	if fromPackage.kind == boundaryPackageAppAdapter {
		return forbiddenApplicationAdapterImport(toPath, fromPackage, toPackage, graph.ExternalImports)
	}
	return forbiddenApplicationCoreImport(toPath, fromPackage, toPackage, graph.ExternalImports)
}

func forbiddenApplicationAdapterImport(
	toPath string,
	fromPackage, toPackage boundaryPackage,
	policy ExternalImportPolicy,
) string {
	switch {
	case toPackage.kind == boundaryPackageAppCore && toPackage.owner == fromPackage.owner:
		return ""
	case toPackage.kind == boundaryPackageAppAdapter &&
		toPackage.owner == fromPackage.owner && toPackage.adapter == fromPackage.adapter:
		return ""
	case toPackage.kind == boundaryPackagePlatform:
		return ""
	case fromPackage.adapter == "fleetdb" && isSharedFleetDBTransport(toPath):
		return ""
	case !isModuleInternalPath(toPath):
		return layerExternalImportViolation(toPath, true, "named application workflow", policy)
	default:
		return "application adapter may import only its workflow API and adapter subtree, approved platform mechanisms, and the shared FleetDB transport from a fleetdb adapter"
	}
}

func forbiddenApplicationCoreImport(
	toPath string,
	fromPackage, toPackage boundaryPackage,
	policy ExternalImportPolicy,
) string {
	if isCapabilityPackage(toPackage.kind) {
		if toPackage.kind != boundaryPackageCapabilityPublic {
			return "named application workflow may import only capability public roots"
		}
		return ""
	}
	if toPackage.kind == boundaryPackageComposition {
		return "named application workflow may not import the composition root"
	}
	if toPackage.kind == boundaryPackageAppAdapter ||
		(toPackage.kind == boundaryPackageAppCore && toPackage.owner != fromPackage.owner) {
		return "named application workflow core may use only its own ports, not application implementations"
	}
	if toPackage.kind == boundaryPackageAppCore || toPackage.kind == boundaryPackagePlatform {
		return ""
	}
	if !isModuleInternalPath(toPath) {
		return layerExternalImportViolation(toPath, false, "named application workflow", policy)
	}
	return "named application workflow core may use only capability public APIs, its own packages and ports, and approved platform mechanisms"
}

func classifyBoundaryPackage(importPath string, graph CapabilityGraph) boundaryPackage {
	if segments, ok := importPathSegments(importPath, graph.ModuleRoot); ok && len(segments) > 0 {
		capability := capabilityNameForRoot(segments[0], graph)
		if capability == "" {
			return boundaryPackage{kind: boundaryPackageUnknownCapability, owner: segments[0]}
		}
		if len(segments) == 1 {
			return boundaryPackage{kind: boundaryPackageCapabilityPublic, owner: capability}
		}
		if isConcreteAdapterSegment(segments[1]) {
			return boundaryPackage{kind: boundaryPackageCapabilityAdapter, owner: capability, adapter: segments[1]}
		}
		return boundaryPackage{kind: boundaryPackageCapabilityCore, owner: capability}
	}
	if segments, ok := importPathSegments(importPath, graph.AppRoot); ok && len(segments) > 0 {
		if segments[0] == "serve" {
			return boundaryPackage{kind: boundaryPackageComposition, owner: "serve"}
		}
		if len(segments) > 1 && isConcreteAdapterSegment(segments[1]) {
			return boundaryPackage{kind: boundaryPackageAppAdapter, owner: segments[0], adapter: segments[1]}
		}
		return boundaryPackage{kind: boundaryPackageAppCore, owner: segments[0]}
	}
	if _, ok := importPathSegments(importPath, graph.PlatformRoot); ok {
		return boundaryPackage{kind: boundaryPackagePlatform}
	}
	return boundaryPackage{kind: boundaryPackageOther}
}

func forbiddenCapabilityImport(
	toPath string,
	fromPackage, toPackage boundaryPackage,
	graph CapabilityGraph,
) string {
	if toPackage.kind == boundaryPackageUnknownCapability {
		return "capability imports must target a declared capability public root"
	}

	if fromPackage.kind == boundaryPackageCapabilityAdapter {
		switch {
		case toPackage.kind == boundaryPackageCapabilityPublic && toPackage.owner == fromPackage.owner:
			return ""
		case toPackage.kind == boundaryPackageCapabilityAdapter &&
			toPackage.owner == fromPackage.owner && toPackage.adapter == fromPackage.adapter:
			return ""
		case toPackage.kind == boundaryPackagePlatform:
			return ""
		case fromPackage.adapter == "fleetdb" && isSharedFleetDBTransport(toPath):
			return ""
		case !isModuleInternalPath(toPath):
			return layerExternalImportViolation(toPath, true, "capability", graph.ExternalImports)
		default:
			return "capability adapter may import only its own public root and adapter subtree, approved platform mechanisms, and the shared FleetDB transport from a fleetdb adapter"
		}
	}

	if isCapabilityPackage(toPackage.kind) {
		if toPackage.owner == fromPackage.owner {
			if toPackage.kind == boundaryPackageCapabilityAdapter {
				return "capability core may not import its own concrete adapter"
			}
			return ""
		}
		if toPackage.kind != boundaryPackageCapabilityPublic {
			return "cross-capability imports must use the public root"
		}
		if !graphAllowsImport(graph, fromPackage.owner, toPackage.owner) {
			return "capability edge is not declared"
		}
		return ""
	}
	if toPackage.kind == boundaryPackagePlatform {
		return ""
	}
	if !isModuleInternalPath(toPath) {
		return layerExternalImportViolation(toPath, false, "capability", graph.ExternalImports)
	}
	return fmt.Sprintf("capability core may not import internal implementation package %s; use its own packages, declared capability public roots, or approved platform mechanisms", toPath)
}

func layerExternalImportViolation(importPath string, adapter bool, subject string, policy ExternalImportPolicy) string {
	if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
		return fmt.Sprintf("%s package may not import Loom implementation package %s outside the approved module, app, or platform roots", subject, importPath)
	}
	if isStandardLibraryImport(importPath) {
		if !adapter && matchesImportPrefix(importPath, policy.CoreDeniedStandardPrefixes) {
			return fmt.Sprintf("%s core may not import standard-library infrastructure package %s", subject, importPath)
		}
		return ""
	}
	allowed := policy.CoreAllowedPrefixes
	layer := "core"
	if adapter {
		allowed = policy.AdapterAllowedPrefixes
		layer = "adapter"
	}
	if matchesImportPrefix(importPath, allowed) {
		return ""
	}
	return fmt.Sprintf("%s %s external import %s is not approved by the capability graph", subject, layer, importPath)
}

func isStandardLibraryImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return first != "" && !strings.Contains(first, ".")
}

func platformExternalImportViolation(importPath string, policy ExternalImportPolicy) string {
	if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
		return fmt.Sprintf("platform may not import Loom implementation package %s outside internal/platform", importPath)
	}
	if isStandardLibraryImport(importPath) || matchesImportPrefix(importPath, policy.PlatformAllowedPrefixes) {
		return ""
	}
	return fmt.Sprintf("platform external import %s is not approved by the capability graph", importPath)
}

func isModuleInternalPath(importPath string) bool {
	prefix := modulePath + "/internal"
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func importPathSegments(importPath, root string) ([]string, bool) {
	prefix := modulePath + "/" + strings.Trim(root, "/")
	if importPath == prefix {
		return nil, true
	}
	if !strings.HasPrefix(importPath, prefix+"/") {
		return nil, false
	}
	return strings.Split(strings.TrimPrefix(importPath, prefix+"/"), "/"), true
}

func capabilityNameForRoot(root string, graph CapabilityGraph) string {
	for _, capability := range graph.Capabilities {
		if capability.Root == root {
			return capability.Name
		}
	}
	return ""
}

func isConcreteAdapterSegment(segment string) bool {
	return segment == "filesystem" || segment == "fleetdb" || segment == "httpapi"
}

func isSharedFleetDBTransport(importPath string) bool {
	prefix := modulePath + "/internal/infra/fleetdb"
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}

func isCapabilityPackage(kind boundaryPackageKind) bool {
	return kind == boundaryPackageCapabilityPublic || kind == boundaryPackageCapabilityCore || kind == boundaryPackageCapabilityAdapter
}

func isForbiddenModuleDependency(importPath string) bool {
	for _, prefix := range []string{
		modulePath + "/internal/app",
		modulePath + "/internal/domain",
		modulePath + "/internal/driver",
		// Phase 5 physically retired this legacy root. Keep the import
		// tombstone so a later file cannot silently recreate the facade.
		modulePath + "/internal/workflows",
		modulePath + "/internal/store",
		modulePath + "/internal/webui",
		modulePath + "/internal/cli",
		modulePath + "/internal/infra",
	} {
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}

func capabilityForImport(importPath string, graph CapabilityGraph) string {
	classified := classifyBoundaryPackage(importPath, graph)
	if !isCapabilityPackage(classified.kind) {
		return ""
	}
	return classified.owner
}

func isCapabilityPublicRoot(importPath string, graph CapabilityGraph) bool {
	return classifyBoundaryPackage(importPath, graph).kind == boundaryPackageCapabilityPublic
}

func graphAllowsImport(graph CapabilityGraph, from, to string) bool {
	for _, edge := range graph.Edges {
		if edge.From == from && edge.To == to && slices.Contains(edge.Kinds, "import") {
			return true
		}
	}
	return false
}
