package backends

import "testing"

var (
	_ MetadataProvider                          = (*GeminiBackend)(nil)
	_ HealthCheckableBackend                    = (*GeminiBackend)(nil)
	_ ResumableNonInteractiveBackend            = (*GeminiBackend)(nil)
	_ ProviderMetadataReporter                  = (*GeminiBackend)(nil)
	_ interface{ LastSessionID(string) string } = (*GeminiBackend)(nil)
)

func TestGeminiBackend_InspectCapabilities(t *testing.T) {
	b := &GeminiBackend{}
	caps := InspectCapabilities(b)

	if !caps.HasMeta {
		t.Error("expected HasMeta=true")
	}
	if !caps.HasHealthCheck {
		t.Error("expected HasHealthCheck=true")
	}
	if !caps.HasProviderMetadata {
		t.Error("expected HasProviderMetadata=true")
	}
	if !caps.HasNonInteractiveResume {
		t.Error("expected HasNonInteractiveResume=true")
	}
	if caps.HasStreaming {
		t.Error("expected HasStreaming=false")
	}
	if caps.HasSessions {
		t.Error("expected HasSessions=false")
	}
	if caps.HasToolControl {
		t.Error("expected HasToolControl=false")
	}
	if caps.HasConfig {
		t.Error("expected HasConfig=false")
	}
}
