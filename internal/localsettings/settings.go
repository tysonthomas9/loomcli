// Package localsettings stores desktop-local runtime settings.
package localsettings

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
)

const (
	fileName                     = "local-settings.json"
	runtimeCredentialKeyFileName = "runtime-credentials.key" //nolint:gosec // fixed filename, not a credential value

	AgentRuntimeLocal   = "local"
	AgentRuntimeDaytona = "daytona"

	RuntimeCredentialProviderDaytona = "daytona"
	RuntimeCredentialProviderGitHub  = "github"

	EnvFleetDBRedisAddr     = "LOOM_FLEET_DB_REDIS_ADDR"
	EnvFleetDBRedisPassword = "LOOM_FLEET_DB_REDIS_PASSWORD" //nolint:gosec // env var name, not a credential
	EnvFleetDBRedisDB       = "LOOM_FLEET_DB_REDIS_DB"
	EnvFleetDBRedisTLS      = "LOOM_FLEET_DB_REDIS_TLS"
)

// Settings is the persisted desktop-local settings file.
type Settings struct {
	Version            int                        `json:"version"`
	FleetDBRedis       RedisConfig                `json:"fleetdb_redis"`
	AgentRuntime       AgentRuntimeConfig         `json:"agent_runtime"`
	LocalTaskRunner    LocalTaskRunnerConfig      `json:"local_task_runner,omitempty"`
	RuntimeCredentials RuntimeCredentialSetConfig `json:"runtime_credentials,omitempty"`
}

// RedisConfig configures the Redis backing store used by embedded fleet-db.
type RedisConfig struct {
	Enabled  bool   `json:"enabled"`
	Addr     string `json:"addr,omitempty"`
	Password string `json:"password,omitempty"` //nolint:gosec // G117: persisted local Redis password setting.
	DB       int    `json:"db,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
}

// AgentRuntimeConfig controls where app-triggered task agents run by default.
type AgentRuntimeConfig struct {
	Default string `json:"default"`
}

// LocalTaskRunnerConfig stores non-secret app settings for local CLI runners.
type LocalTaskRunnerConfig struct {
	OpenCodeModel string `json:"opencode_model,omitempty"`
}

// RuntimeCredentialSetConfig stores sealed app-local runtime credentials.
type RuntimeCredentialSetConfig struct {
	Daytona RuntimeCredentialConfig `json:"daytona,omitempty"`
	GitHub  RuntimeCredentialConfig `json:"github,omitempty"`
}

// RuntimeCredentialConfig is ciphertext-only credential metadata.
type RuntimeCredentialConfig struct {
	Sealed    string    `json:"sealed,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// SanitizedSettings is safe to return to the UI.
type SanitizedSettings struct {
	Version            int                            `json:"version"`
	FleetDBRedis       SanitizedRedisConfig           `json:"fleetdb_redis"`
	AgentRuntime       SanitizedAgentRuntimeConfig    `json:"agent_runtime"`
	LocalTaskRunner    SanitizedLocalTaskRunnerConfig `json:"local_task_runner"`
	RuntimeCredentials SanitizedRuntimeCredentialSet  `json:"runtime_credentials"`
}

// SanitizedRedisConfig omits Redis credentials.
type SanitizedRedisConfig struct {
	Enabled     bool   `json:"enabled"`
	Addr        string `json:"addr,omitempty"`
	DB          int    `json:"db"`
	TLS         bool   `json:"tls"`
	PasswordSet bool   `json:"password_set"`
}

// SanitizedAgentRuntimeConfig is the UI-safe default runtime selection.
type SanitizedAgentRuntimeConfig struct {
	Default string `json:"default"`
}

// SanitizedLocalTaskRunnerConfig exposes non-secret local runner settings.
type SanitizedLocalTaskRunnerConfig struct {
	OpenCodeModel string `json:"opencode_model,omitempty"`
}

// SanitizedRuntimeCredentialSet exposes status, never secret material.
type SanitizedRuntimeCredentialSet struct {
	Daytona SanitizedRuntimeCredential `json:"daytona"`
	GitHub  SanitizedRuntimeCredential `json:"github"`
}

// SanitizedRuntimeCredential is safe to return to the browser.
type SanitizedRuntimeCredential struct {
	Configured bool   `json:"configured"`
	Usable     bool   `json:"usable"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// Path returns the settings file path under the local data directory.
func Path(dataDir string) string {
	return filepath.Join(dataDir, fileName)
}

// Default returns empty local settings.
func Default() Settings {
	return Settings{
		Version:      1,
		AgentRuntime: AgentRuntimeConfig{Default: AgentRuntimeLocal},
	}
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
	settings.AgentRuntime.Default = normalizeAgentRuntime(settings.AgentRuntime.Default)
	settings.LocalTaskRunner.OpenCodeModel = strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel)
	return settings, nil
}

// Save validates and writes settings to dataDir with user-only permissions.
func Save(dataDir string, settings Settings) error {
	if dataDir == "" {
		return errors.New("local settings data dir is required")
	}
	settings.Version = 1
	settings.AgentRuntime.Default = normalizeAgentRuntime(settings.AgentRuntime.Default)
	settings.LocalTaskRunner.OpenCodeModel = strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel)
	if err := Validate(settings.FleetDBRedis); err != nil {
		return err
	}
	if err := ValidateAgentRuntime(settings.AgentRuntime); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("mkdir local settings dir: %w", err)
	}
	_ = os.Chmod(dataDir, 0700) //nolint:gosec // directory needs execute bit; rwx for owner only
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal local settings: %w", err)
	}
	path := Path(dataDir)
	temp, err := os.CreateTemp(dataDir, ".settings.json.publish-*")
	if err != nil {
		return fmt.Errorf("create local settings temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod local settings temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil { //nolint:gosec // contains local Redis credential
		_ = temp.Close()
		return fmt.Errorf("write local settings: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync local settings: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close local settings: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
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
	runtime := normalizeAgentRuntime(settings.AgentRuntime.Default)
	return SanitizedSettings{
		Version: settings.Version,
		FleetDBRedis: SanitizedRedisConfig{
			Enabled:     redis.Enabled,
			Addr:        redis.Addr,
			DB:          redis.DB,
			TLS:         redis.TLS,
			PasswordSet: redis.Password != "",
		},
		AgentRuntime: SanitizedAgentRuntimeConfig{
			Default: runtime,
		},
		LocalTaskRunner: SanitizedLocalTaskRunnerConfig{
			OpenCodeModel: strings.TrimSpace(settings.LocalTaskRunner.OpenCodeModel),
		},
		RuntimeCredentials: SanitizedRuntimeCredentialSet{
			Daytona: sanitizeRuntimeCredential(settings.RuntimeCredentials.Daytona),
			GitHub:  sanitizeRuntimeCredential(settings.RuntimeCredentials.GitHub),
		},
	}
}

// SanitizeWithRuntimeCredentialReadiness returns UI-safe settings and verifies
// that each configured credential can actually be opened with the current
// local vault key. It never exposes plaintext or the unseal error to callers.
func SanitizeWithRuntimeCredentialReadiness(dataDir string, settings Settings) SanitizedSettings {
	out := Sanitize(settings)
	out.RuntimeCredentials.Daytona = RuntimeCredentialReadiness(
		dataDir,
		settings,
		RuntimeCredentialProviderDaytona,
	)
	out.RuntimeCredentials.GitHub = RuntimeCredentialReadiness(
		dataDir,
		settings,
		RuntimeCredentialProviderGitHub,
	)
	return out
}

// RuntimeCredentialReadiness reports whether a credential is present and
// usable with the current local vault key without returning secret material.
func RuntimeCredentialReadiness(dataDir string, settings Settings, provider string) SanitizedRuntimeCredential {
	provider = normalizeRuntimeCredentialProvider(provider)
	var credential RuntimeCredentialConfig
	switch provider {
	case RuntimeCredentialProviderDaytona:
		credential = settings.RuntimeCredentials.Daytona
	case RuntimeCredentialProviderGitHub:
		credential = settings.RuntimeCredentials.GitHub
	default:
		return SanitizedRuntimeCredential{}
	}

	out := sanitizeRuntimeCredential(credential)
	if !out.Configured {
		return out
	}
	plaintext, err := UnsealRuntimeCredentialBytes(dataDir, settings, provider)
	if err == nil {
		zeroRuntimeCredentialBytes(plaintext)
		out.Usable = true
	}
	return out
}

// ValidateAgentRuntime rejects unknown runtime defaults.
func ValidateAgentRuntime(cfg AgentRuntimeConfig) error {
	switch normalizeAgentRuntime(cfg.Default) {
	case AgentRuntimeLocal, AgentRuntimeDaytona:
		return nil
	default:
		return fmt.Errorf("agent runtime default must be %q or %q", AgentRuntimeLocal, AgentRuntimeDaytona)
	}
}

// NormalizeAgentRuntime returns the public normalized default.
func NormalizeAgentRuntime(value string) string {
	return normalizeAgentRuntime(value)
}

func normalizeAgentRuntime(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", AgentRuntimeLocal:
		return AgentRuntimeLocal
	case AgentRuntimeDaytona:
		return AgentRuntimeDaytona
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func sanitizeRuntimeCredential(credential RuntimeCredentialConfig) SanitizedRuntimeCredential {
	out := SanitizedRuntimeCredential{Configured: strings.TrimSpace(credential.Sealed) != ""}
	if !credential.UpdatedAt.IsZero() {
		out.UpdatedAt = credential.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// SealRuntimeCredential seals plaintext for storage in local settings.
func SealRuntimeCredential(dataDir, provider, plaintext string, now time.Time) (RuntimeCredentialConfig, error) {
	provider = normalizeRuntimeCredentialProvider(provider)
	switch provider {
	case RuntimeCredentialProviderDaytona, RuntimeCredentialProviderGitHub:
	case "":
		return RuntimeCredentialConfig{}, errors.New("runtime credential provider required")
	default:
		return RuntimeCredentialConfig{}, fmt.Errorf("runtime credential provider %q is not supported", provider)
	}
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return RuntimeCredentialConfig{}, fmt.Errorf("%s credential is empty", provider)
	}
	vault, err := runtimeCredentialVault(dataDir)
	if err != nil {
		return RuntimeCredentialConfig{}, err
	}
	sealed, err := vault.Seal([]byte(plaintext), runtimeCredentialAAD(provider))
	if err != nil {
		return RuntimeCredentialConfig{}, fmt.Errorf("seal %s runtime credential: %w", provider, err)
	}
	if now.IsZero() {
		now = time.Now()
	}
	return RuntimeCredentialConfig{
		Sealed:    base64.StdEncoding.EncodeToString(sealed),
		UpdatedAt: now.UTC(),
	}, nil
}

// UnsealRuntimeCredential resolves a sealed local runtime credential.
func UnsealRuntimeCredential(dataDir string, settings Settings, provider string) (string, error) {
	plain, err := UnsealRuntimeCredentialBytes(dataDir, settings, provider)
	if err != nil {
		return "", err
	}
	defer zeroRuntimeCredentialBytes(plain)
	return string(plain), nil
}

// UnsealRuntimeCredentialBytes resolves a sealed local runtime credential
// without materializing an immutable string. Callers must overwrite the
// returned slice as soon as the credential has been consumed.
func UnsealRuntimeCredentialBytes(dataDir string, settings Settings, provider string) ([]byte, error) {
	provider = normalizeRuntimeCredentialProvider(provider)
	var credential RuntimeCredentialConfig
	switch provider {
	case RuntimeCredentialProviderDaytona:
		credential = settings.RuntimeCredentials.Daytona
	case RuntimeCredentialProviderGitHub:
		credential = settings.RuntimeCredentials.GitHub
	default:
		return nil, fmt.Errorf("runtime credential provider %q is not supported", provider)
	}
	if strings.TrimSpace(credential.Sealed) == "" {
		return nil, fmt.Errorf("%s runtime credential is not configured", provider)
	}
	sealed, err := base64.StdEncoding.DecodeString(credential.Sealed)
	if err != nil {
		return nil, fmt.Errorf("decode %s runtime credential: %w", provider, err)
	}
	vault, err := runtimeCredentialVault(dataDir)
	if err != nil {
		return nil, err
	}
	plain, err := vault.Unseal(sealed, runtimeCredentialAAD(provider))
	if err != nil {
		return nil, fmt.Errorf("unseal %s runtime credential: %w", provider, err)
	}
	plaintext := bytes.TrimSpace(plain)
	if len(plaintext) == 0 {
		zeroRuntimeCredentialBytes(plain)
		return nil, fmt.Errorf("%s runtime credential is empty", provider)
	}
	if len(plaintext) != len(plain) {
		trimmed := append([]byte(nil), plaintext...)
		zeroRuntimeCredentialBytes(plain)
		return trimmed, nil
	}
	return plain, nil
}

func zeroRuntimeCredentialBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func normalizeRuntimeCredentialProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case RuntimeCredentialProviderDaytona:
		return RuntimeCredentialProviderDaytona
	case RuntimeCredentialProviderGitHub, "gh":
		return RuntimeCredentialProviderGitHub
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func runtimeCredentialVault(dataDir string) (*connector.Vault, error) {
	key, err := runtimeCredentialKey(dataDir)
	if err != nil {
		return nil, err
	}
	return connector.NewVault(key)
}

func runtimeCredentialKey(dataDir string) ([]byte, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("local settings data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir local settings dir: %w", err)
	}
	_ = os.Chmod(dataDir, 0700) //nolint:gosec // dataDir is a private directory; execute bit is required for traversal
	path := filepath.Join(dataDir, runtimeCredentialKeyFileName)
	if key, err := readRuntimeCredentialKey(path); err == nil {
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read runtime credential key: %w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate runtime credential key: %w", err)
	}
	published, err := publishRuntimeCredentialKey(dataDir, path, key)
	if err != nil {
		zeroRuntimeCredentialBytes(key)
		return nil, err
	}
	if published {
		return key, nil
	}

	// Another process won the no-replace publication race. Discard this
	// candidate and return the exact persisted winner.
	zeroRuntimeCredentialBytes(key)
	winner, err := readRuntimeCredentialKey(path)
	if err != nil {
		return nil, fmt.Errorf("read concurrently published runtime credential key: %w", err)
	}
	return winner, nil
}

func publishRuntimeCredentialKey(dataDir, path string, key []byte) (bool, error) {
	temp, err := os.CreateTemp(dataDir, "."+runtimeCredentialKeyFileName+".publish-*")
	if err != nil {
		return false, fmt.Errorf("create runtime credential key temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("chmod runtime credential key temp file: %w", err)
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(key))+1)
	defer zeroRuntimeCredentialBytes(encoded)
	base64.StdEncoding.Encode(encoded, key)
	encoded[len(encoded)-1] = '\n'
	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("write runtime credential key: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return false, fmt.Errorf("sync runtime credential key: %w", err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("close runtime credential key: %w", err)
	}
	if err := os.Link(tempPath, path); err == nil {
		_ = os.Chmod(path, 0600)
		return true, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, fmt.Errorf("publish runtime credential key: %w", err)
	}
	return false, nil
}

func readRuntimeCredentialKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixed filename under the private settings directory
	if err != nil {
		return nil, err
	}
	defer zeroRuntimeCredentialBytes(data)
	encoded := bytes.TrimSpace(data)
	key := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	n, err := base64.StdEncoding.Decode(key, encoded)
	if err != nil {
		zeroRuntimeCredentialBytes(key)
		return nil, fmt.Errorf("decode runtime credential key: %w", err)
	}
	return key[:n], nil
}

func runtimeCredentialAAD(provider string) []byte {
	return []byte("loom-runtime-credential\x00" + provider)
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
