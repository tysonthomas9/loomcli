package localsettings

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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
	FleetDBRedis *redisPatch `json:"fleetdb_redis"`
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
		if err := runtimesettings.Save(dataDir, settings); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, response{Success: false, Error: err.Error()})
			return
		}
		sanitized := runtimesettings.Sanitize(settings)
		handler.WriteJSON(w, http.StatusOK, response{
			Success: true,
			Data:    &sanitized,
			Message: "Redis settings saved. Restart the local runtime to apply changes.",
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
