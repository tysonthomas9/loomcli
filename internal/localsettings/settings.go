// Package localsettings stores desktop-local runtime settings.
package localsettings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	fileName = "local-settings.json"

	EnvFleetDBRedisAddr     = "LOOM_FLEET_DB_REDIS_ADDR"
	EnvFleetDBRedisPassword = "LOOM_FLEET_DB_REDIS_PASSWORD" //nolint:gosec // env var name, not a credential
	EnvFleetDBRedisDB       = "LOOM_FLEET_DB_REDIS_DB"
	EnvFleetDBRedisTLS      = "LOOM_FLEET_DB_REDIS_TLS"
)

// Settings is the persisted desktop-local settings file.
type Settings struct {
	Version      int         `json:"version"`
	FleetDBRedis RedisConfig `json:"fleetdb_redis"`
}

// RedisConfig configures the Redis backing store used by embedded fleet-db.
type RedisConfig struct {
	Enabled  bool   `json:"enabled"`
	Addr     string `json:"addr,omitempty"`
	Password string `json:"password,omitempty"`
	DB       int    `json:"db,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
}

// SanitizedSettings is safe to return to the UI.
type SanitizedSettings struct {
	Version      int                  `json:"version"`
	FleetDBRedis SanitizedRedisConfig `json:"fleetdb_redis"`
}

// SanitizedRedisConfig omits Redis credentials.
type SanitizedRedisConfig struct {
	Enabled     bool   `json:"enabled"`
	Addr        string `json:"addr,omitempty"`
	DB          int    `json:"db"`
	TLS         bool   `json:"tls"`
	PasswordSet bool   `json:"password_set"`
}

// Path returns the settings file path under the local data directory.
func Path(dataDir string) string {
	return filepath.Join(dataDir, fileName)
}

// Default returns empty local settings.
func Default() Settings {
	return Settings{Version: 1}
}

// Load reads settings from dataDir. A missing file returns defaults.
func Load(dataDir string) (Settings, error) {
	if dataDir == "" {
		return Default(), errors.New("local settings data dir is required")
	}
	data, err := os.ReadFile(Path(dataDir)) //nolint:gosec // user-private data dir
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Default(), fmt.Errorf("read local settings: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Default(), fmt.Errorf("parse local settings: %w", err)
	}
	if settings.Version == 0 {
		settings.Version = 1
	}
	return settings, nil
}

// Save validates and writes settings to dataDir with user-only permissions.
func Save(dataDir string, settings Settings) error {
	if dataDir == "" {
		return errors.New("local settings data dir is required")
	}
	settings.Version = 1
	if err := Validate(settings.FleetDBRedis); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("mkdir local settings dir: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal local settings: %w", err)
	}
	path := Path(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil { //nolint:gosec // contains local Redis credential
		return fmt.Errorf("write local settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename local settings: %w", err)
	}
	_ = os.Chmod(path, 0600)
	return nil
}

// Validate rejects unusable Redis settings.
func Validate(cfg RedisConfig) error {
	if !cfg.Enabled {
		return nil
	}
	cfg.Addr = strings.TrimSpace(cfg.Addr)
	if cfg.Addr == "" {
		return errors.New("redis address is required")
	}
	if cfg.DB < 0 {
		return errors.New("redis database must be 0 or greater")
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return fmt.Errorf("redis address must be host:port")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("redis port must be numeric")
	}
	return nil
}

// Sanitize returns settings without credentials.
func Sanitize(settings Settings) SanitizedSettings {
	redis := settings.FleetDBRedis
	return SanitizedSettings{
		Version: settings.Version,
		FleetDBRedis: SanitizedRedisConfig{
			Enabled:     redis.Enabled,
			Addr:        redis.Addr,
			DB:          redis.DB,
			TLS:         redis.TLS,
			PasswordSet: redis.Password != "",
		},
	}
}

// RuntimeHash returns a stable fingerprint for deciding whether a running
// embedded fleet-db process matches the desired Redis settings.
func RuntimeHash(cfg RedisConfig) string {
	if !cfg.Enabled {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(cfg.Addr),
		strconv.Itoa(cfg.DB),
		strconv.FormatBool(cfg.TLS),
		cfg.Password,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Env appends the fleet-db Redis environment for external Redis settings.
func Env(env []string, cfg RedisConfig) []string {
	if !cfg.Enabled {
		return env
	}
	env = append(env,
		EnvFleetDBRedisAddr+"="+strings.TrimSpace(cfg.Addr),
		EnvFleetDBRedisDB+"="+strconv.Itoa(cfg.DB),
		EnvFleetDBRedisTLS+"="+strconv.FormatBool(cfg.TLS),
	)
	if cfg.Password != "" {
		env = append(env, EnvFleetDBRedisPassword+"="+cfg.Password)
	}
	return env
}

// RedisFromURL parses redis:// or rediss:// connection strings. It also accepts
// a redis-cli command containing --tls and -u/--uri for paste-friendly setup.
func RedisFromURL(raw string) (RedisConfig, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return RedisConfig{}, errors.New("redis URL is required")
	}
	token, forceTLS := extractRedisURL(input)
	if token == "" {
		return RedisConfig{}, errors.New("redis URL must start with redis:// or rediss://")
	}
	u, err := url.Parse(token)
	if err != nil {
		return RedisConfig{}, fmt.Errorf("parse redis URL: %w", err)
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return RedisConfig{}, errors.New("redis URL scheme must be redis:// or rediss://")
	}
	if u.Host == "" {
		return RedisConfig{}, errors.New("redis URL host is required")
	}
	addr := u.Host
	if _, _, err := net.SplitHostPort(addr); err != nil {
		if strings.Contains(err.Error(), "missing port in address") {
			addr = net.JoinHostPort(u.Hostname(), "6379")
		} else {
			return RedisConfig{}, fmt.Errorf("redis URL address: %w", err)
		}
	}
	password, _ := u.User.Password()
	db := 0
	if path := strings.Trim(u.EscapedPath(), "/"); path != "" {
		n, err := strconv.Atoi(path)
		if err != nil {
			return RedisConfig{}, fmt.Errorf("redis URL database must be numeric")
		}
		db = n
	}
	cfg := RedisConfig{
		Enabled:  true,
		Addr:     addr,
		Password: password,
		DB:       db,
		TLS:      forceTLS || u.Scheme == "rediss",
	}
	return cfg, Validate(cfg)
}

func extractRedisURL(input string) (string, bool) {
	if strings.HasPrefix(input, "redis://") || strings.HasPrefix(input, "rediss://") {
		return input, false
	}
	fields := strings.Fields(input)
	forceTLS := false
	for _, field := range fields {
		if field == "--tls" {
			forceTLS = true
			break
		}
	}
	for i, field := range fields {
		switch {
		case field == "-u" || field == "--uri":
			if i+1 < len(fields) {
				return fields[i+1], forceTLS
			}
		case strings.HasPrefix(field, "--uri="):
			return strings.TrimPrefix(field, "--uri="), forceTLS
		case strings.HasPrefix(field, "redis://") || strings.HasPrefix(field, "rediss://"):
			return field, forceTLS
		}
	}
	return "", forceTLS
}
