package localsettings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	runtimesettings "github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

type response struct {
	Success bool                               `json:"success"`
	Data    *runtimesettings.SanitizedSettings `json:"data,omitempty"`
	Error   string                             `json:"error,omitempty"`
	Message string                             `json:"message,omitempty"`
}

type patchRequest struct {
	FleetDBRedis       *redisPatch              `json:"fleetdb_redis"`
	AgentRuntime       *agentRuntimePatch       `json:"agent_runtime"`
	LocalTaskRunner    *localTaskRunnerPatch    `json:"local_task_runner"`
	RuntimeCredentials *runtimeCredentialsPatch `json:"runtime_credentials"`
}

type redisPatch struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	RedisURL      *string `json:"redis_url,omitempty"`
	Addr          *string `json:"addr,omitempty"`
	Password      *string `json:"password,omitempty"` //nolint:gosec // G117: password update payload for local Redis settings.
	ClearPassword bool    `json:"clear_password,omitempty"`
	DB            *int    `json:"db,omitempty"`
	TLS           *bool   `json:"tls,omitempty"`
}

type agentRuntimePatch struct {
	Default *string `json:"default,omitempty"`
}

type localTaskRunnerPatch struct {
	OpenCodeModel *string `json:"opencode_model,omitempty"`
}

type runtimeCredentialsPatch struct {
	Daytona *runtimeCredentialPatch `json:"daytona,omitempty"`
	GitHub  *runtimeCredentialPatch `json:"github,omitempty"`
}

type runtimeCredentialPatch struct {
	// #nosec G117 -- this is a credential-input PATCH DTO; carrying the api_key
	// the operator is setting is the field's purpose, not an accidental leak.
	APIKey *string `json:"api_key,omitempty"`
	Token  *string `json:"token,omitempty"`
	Clear  bool    `json:"clear,omitempty"`
}

// HandleGet returns sanitized desktop-local runtime settings.
func HandleGet(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		settings, err := load(dataDir)
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, response{Success: false, Error: err.Error()})
			return
		}
		sanitized := runtimesettings.Sanitize(settings)
		handler.WriteJSON(w, http.StatusOK, response{Success: true, Data: &sanitized})
	}
}

// HandlePatch updates desktop-local runtime settings. Redis changes require
// restarting the local runtime because embedded fleet-db reads them at startup.
func HandlePatch(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req patchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, response{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, response{Success: false, Error: "invalid request body"})
			return
		}

		settings, err := load(dataDir)
		if err != nil {
			handler.WriteJSON(w, http.StatusInternalServerError, response{Success: false, Error: err.Error()})
			return
		}
		if req.FleetDBRedis != nil {
			if err := applyRedisPatch(&settings, *req.FleetDBRedis); err != nil {
				handler.WriteJSON(w, http.StatusBadRequest, response{Success: false, Error: err.Error()})
				return
			}
		}
		if req.AgentRuntime != nil {
			if err := applyAgentRuntimePatch(&settings, *req.AgentRuntime); err != nil {
				handler.WriteJSON(w, http.StatusBadRequest, response{Success: false, Error: err.Error()})
				return
			}
		}
		if req.LocalTaskRunner != nil {
			applyLocalTaskRunnerPatch(&settings, *req.LocalTaskRunner)
		}
		if req.RuntimeCredentials != nil {
			if err := applyRuntimeCredentialsPatch(dataDir, &settings, *req.RuntimeCredentials); err != nil {
				handler.WriteJSON(w, http.StatusBadRequest, response{Success: false, Error: err.Error()})
				return
			}
		}
		if err := runtimesettings.Save(dataDir, settings); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, response{Success: false, Error: err.Error()})
			return
		}
		sanitized := runtimesettings.Sanitize(settings)
		handler.WriteJSON(w, http.StatusOK, response{
			Success: true,
			Data:    &sanitized,
			Message: "Local settings saved.",
		})
	}
}

func load(dataDir string) (runtimesettings.Settings, error) {
	if strings.TrimSpace(dataDir) == "" {
		return runtimesettings.Settings{}, errors.New("local settings data dir is not configured")
	}
	return runtimesettings.Load(dataDir)
}

func applyRedisPatch(settings *runtimesettings.Settings, patch redisPatch) error {
	cfg := settings.FleetDBRedis
	if patch.RedisURL != nil && strings.TrimSpace(*patch.RedisURL) != "" {
		parsed, err := runtimesettings.RedisFromURL(*patch.RedisURL)
		if err != nil {
			return err
		}
		cfg = parsed
	}
	if patch.Enabled != nil {
		cfg.Enabled = *patch.Enabled
	}
	if patch.Addr != nil {
		cfg.Addr = strings.TrimSpace(*patch.Addr)
	}
	if patch.DB != nil {
		cfg.DB = *patch.DB
	}
	if patch.TLS != nil {
		cfg.TLS = *patch.TLS
	}
	if patch.Password != nil && *patch.Password != "" {
		cfg.Password = *patch.Password
	}
	if patch.ClearPassword {
		cfg.Password = ""
	}
	if err := runtimesettings.Validate(cfg); err != nil {
		return err
	}
	settings.FleetDBRedis = cfg
	return nil
}

func applyAgentRuntimePatch(settings *runtimesettings.Settings, patch agentRuntimePatch) error {
	cfg := settings.AgentRuntime
	if patch.Default != nil {
		cfg.Default = runtimesettings.NormalizeAgentRuntime(*patch.Default)
	}
	if err := runtimesettings.ValidateAgentRuntime(cfg); err != nil {
		return err
	}
	settings.AgentRuntime = cfg
	return nil
}

func applyLocalTaskRunnerPatch(settings *runtimesettings.Settings, patch localTaskRunnerPatch) {
	cfg := settings.LocalTaskRunner
	if patch.OpenCodeModel != nil {
		cfg.OpenCodeModel = strings.TrimSpace(*patch.OpenCodeModel)
	}
	settings.LocalTaskRunner = cfg
}

func applyRuntimeCredentialsPatch(dataDir string, settings *runtimesettings.Settings, patch runtimeCredentialsPatch) error {
	now := time.Now().UTC()
	if patch.Daytona != nil {
		credential, clear, err := runtimeCredentialFromPatch(dataDir, runtimesettings.RuntimeCredentialProviderDaytona, *patch.Daytona, now)
		if err != nil {
			return err
		}
		if clear {
			settings.RuntimeCredentials.Daytona = runtimesettings.RuntimeCredentialConfig{}
		} else if credential.Sealed != "" {
			settings.RuntimeCredentials.Daytona = credential
		}
	}
	if patch.GitHub != nil {
		credential, clear, err := runtimeCredentialFromPatch(dataDir, runtimesettings.RuntimeCredentialProviderGitHub, *patch.GitHub, now)
		if err != nil {
			return err
		}
		if clear {
			settings.RuntimeCredentials.GitHub = runtimesettings.RuntimeCredentialConfig{}
		} else if credential.Sealed != "" {
			settings.RuntimeCredentials.GitHub = credential
		}
	}
	return nil
}

func runtimeCredentialFromPatch(dataDir, provider string, patch runtimeCredentialPatch, now time.Time) (runtimesettings.RuntimeCredentialConfig, bool, error) {
	if patch.Clear {
		return runtimesettings.RuntimeCredentialConfig{}, true, nil
	}
	value := ""
	switch provider {
	case runtimesettings.RuntimeCredentialProviderDaytona:
		value = firstNonEmptyStringPtr(patch.APIKey, patch.Token)
	case runtimesettings.RuntimeCredentialProviderGitHub:
		value = firstNonEmptyStringPtr(patch.Token, patch.APIKey)
	}
	if strings.TrimSpace(value) == "" {
		return runtimesettings.RuntimeCredentialConfig{}, false, nil
	}
	credential, err := runtimesettings.SealRuntimeCredential(dataDir, provider, value, now)
	return credential, false, err
}

func firstNonEmptyStringPtr(values ...*string) string {
	for _, value := range values {
		if value != nil && strings.TrimSpace(*value) != "" {
			return strings.TrimSpace(*value)
		}
	}
	return ""
}
