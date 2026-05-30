package defs

import (
	"fmt"
	"strings"
	"unicode"
)

var knownModelProviders = map[string]bool{
	"amazon-bedrock":         true,
	"anthropic":              true,
	"azure-openai-responses": true,
	"cerebras":               true,
	"cloudflare-ai-gateway":  true,
	"cloudflare-workers-ai":  true,
	"deepseek":               true,
	"fireworks":              true,
	"github-copilot":         true,
	"google":                 true,
	"google-vertex":          true,
	"groq":                   true,
	"huggingface":            true,
	"kimi-coding":            true,
	"local":                  true,
	"minimax":                true,
	"minimax-cn":             true,
	"mistral":                true,
	"moonshotai":             true,
	"moonshotai-cn":          true,
	"openai":                 true,
	"openai-codex":           true,
	"opencode":               true,
	"opencode-go":            true,
	"openrouter":             true,
	"test":                   true,
	"together":               true,
	"vercel-ai-gateway":      true,
	"xai":                    true,
	"xiaomi":                 true,
	"xiaomi-token-plan-ams":  true,
	"xiaomi-token-plan-cn":   true,
	"xiaomi-token-plan-sgp":  true,
	"zai":                    true,
}

var knownBareModelAliases = map[string]bool{
	"gpt-4":             true,
	"gpt-4-turbo":       true,
	"gpt-4.1":           true,
	"gpt-4.1-mini":      true,
	"gpt-4.1-nano":      true,
	"gpt-4o":            true,
	"gpt-4o-mini":       true,
	"gpt-5":             true,
	"gpt-5-chat-latest": true,
	"gpt-5-codex":       true,
	"gpt-5-mini":        true,
	"gpt-5-nano":        true,
	"gpt-5-pro":         true,
	"o1":                true,
	"o1-pro":            true,
	"o3":                true,
	"o3-mini":           true,
	"o3-pro":            true,
	"o4-mini":           true,
}

func validateAgentModel(agent AgentModule, policy *ModelPolicy) error {
	model := strings.TrimSpace(agent.Model)
	if model == "" {
		return nil
	}
	if err := validateModelSpecifier(model, policy); err != nil {
		return fmt.Errorf("%s: agent %q model %q: %w", agent.SourcePath, agent.Name, model, err)
	}
	return nil
}

func validateModelSpecifier(model string, policy *ModelPolicy) error {
	if strings.Contains(model, "://") {
		return fmt.Errorf("must be a model specifier, not a URL")
	}
	for _, r := range model {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("must not contain whitespace or control characters")
		}
	}
	if len(model) > 256 {
		return fmt.Errorf("must be 256 characters or fewer")
	}
	if policy != nil && policy.AllowUnknown {
		return nil
	}
	if modelAllowedByPolicy(model, policy) {
		return nil
	}
	provider, _, ok := strings.Cut(model, "/")
	if !ok {
		if knownBareModelAliases[model] {
			return nil
		}
		return fmt.Errorf("unknown bare model alias; use a provider/model specifier or add it to loom.config.ts models.allowed")
	}
	if provider == "" || strings.TrimSpace(provider) != provider {
		return fmt.Errorf("provider prefix is required")
	}
	if knownModelProviders[provider] || providerAllowedByPolicy(provider, policy) {
		return nil
	}
	return fmt.Errorf("unknown model provider %q; add it to loom.config.ts models.allowedProviders for private gateways", provider)
}

func modelAllowedByPolicy(model string, policy *ModelPolicy) bool {
	if policy == nil {
		return false
	}
	for _, allowed := range policy.AllowedModels {
		if strings.TrimSpace(allowed) == model {
			return true
		}
	}
	return false
}

func providerAllowedByPolicy(provider string, policy *ModelPolicy) bool {
	if policy == nil {
		return false
	}
	for _, allowed := range policy.AllowedProviders {
		if strings.TrimSpace(allowed) == provider {
			return true
		}
	}
	return false
}

func validateModelPolicy(policy *ModelPolicy) error {
	if policy == nil {
		return nil
	}
	if err := validateUniqueStrings("loom.config.ts", "allowed model", policy.AllowedModels); err != nil {
		return err
	}
	if err := validateUniqueStrings("loom.config.ts", "allowed model provider", policy.AllowedProviders); err != nil {
		return err
	}
	for _, provider := range policy.AllowedProviders {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			continue
		}
		if strings.Contains(provider, "/") || strings.Contains(provider, "://") {
			return fmt.Errorf("loom.config.ts: allowed model provider %q must be a provider prefix, not a model or URL", provider)
		}
		for _, r := range provider {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return fmt.Errorf("loom.config.ts: allowed model provider %q must not contain whitespace or control characters", provider)
			}
		}
	}
	return nil
}
