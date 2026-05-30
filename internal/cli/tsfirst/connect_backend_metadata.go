package tsfirst

import backendcaps "github.com/tysonthomas9/loomcli/internal/cli/backends"

func backendReportedProviderMetadata(backend any, workDir string) map[string]any {
	reporter, ok := backend.(backendcaps.ProviderMetadataReporter)
	if !ok {
		return nil
	}
	return cloneMap(reporter.LastProviderMetadata(workDir))
}

func mergeBackendProviderMetadata(current map[string]any, backendMetadata map[string]any) map[string]any {
	if len(backendMetadata) == 0 {
		return current
	}
	out := cloneMap(current)
	if out == nil {
		out = make(map[string]any, 1)
	}
	out["backend_reported"] = backendMetadata
	return out
}

func backendProviderModel(backendMetadata map[string]any) string {
	if value, ok := backendMetadata["provider_model"].(string); ok {
		return value
	}
	return ""
}
