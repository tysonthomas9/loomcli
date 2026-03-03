package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	backendListJSON   bool
	backendHealthJSON bool
	backendInfoJSON   bool
)

var backendCmd = &cobra.Command{
	Use:     "backend",
	Short:   "Manage AI backends",
	GroupID: "config",
	Long: `Manage AI backend registrations and health status.

Subcommands:
  list     List all registered backends
  health   Check health status of all backends
  info     Show detailed info for a specific backend

Examples:
  loom backend list              # List all backends
  loom backend list --json       # JSON output for scripting
  loom backend health            # Check health of all backends
  loom backend info claude       # Show details for claude backend`,
}

// --- list ---

var backendListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all registered backends",
	Args:    cobra.NoArgs,
	Run:     runBackendList,
}

type backendListEntry struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Installed   bool   `json:"installed"`
	Active      bool   `json:"active"`
}

func runBackendList(cmd *cobra.Command, args []string) {
	names := ListBackends()
	if len(names) == 0 {
		fmt.Println("No backends registered.")
		return
	}

	active := GetBackendName()
	entries := make([]backendListEntry, 0, len(names))

	for _, name := range names {
		e := backendListEntry{Name: name, Active: name == active}
		b, ok := GetBackendByName(name)
		if ok {
			if mp, ok := b.(MetadataProvider); ok {
				meta := mp.Meta()
				e.DisplayName = meta.DisplayName
				e.Version = meta.Version
			}
			if hc, ok := b.(HealthCheckableBackend); ok {
				hs := hc.HealthCheck()
				e.Installed = hs.Installed
				if e.Version == "" {
					e.Version = hs.Version
				}
			}
		}
		entries = append(entries, e)
	}

	if backendListJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
		return
	}

	// Column-aligned table output
	fmt.Printf("  %-12s %-16s %-20s %s\n", "NAME", "DISPLAY NAME", "VERSION", "INSTALLED")
	for _, e := range entries {
		marker := " "
		if e.Active {
			marker = "*"
		}
		displayName := e.DisplayName
		if displayName == "" {
			displayName = "-"
		}
		version := e.Version
		if version == "" {
			version = "-"
		}
		installed := boolSymbol(e.Installed)
		fmt.Printf("%s %-12s %-16s %-20s %s\n", marker, e.Name, displayName, version, installed)
	}
}

// --- health ---

var backendHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check health status of all backends",
	Args:  cobra.NoArgs,
	Run:   runBackendHealth,
}

type backendHealthEntry struct {
	Name      string `json:"name"`
	Healthy   bool   `json:"healthy"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	APIKeySet bool   `json:"api_key_set"`
	Message   string `json:"message"`
}

func runBackendHealth(cmd *cobra.Command, args []string) {
	names := ListBackends()
	if len(names) == 0 {
		fmt.Println("No backends registered.")
		return
	}

	entries := make([]backendHealthEntry, 0, len(names))
	anyUnhealthy := false

	for _, name := range names {
		e := backendHealthEntry{Name: name}
		b, ok := GetBackendByName(name)
		if ok {
			if hc, ok := b.(HealthCheckableBackend); ok {
				hs := hc.HealthCheck()
				e.Healthy = hs.Healthy
				e.Installed = hs.Installed
				e.Version = hs.Version
				e.APIKeySet = hs.APIKeySet
				e.Message = hs.Message
			} else {
				e.Message = "no health check available"
			}
		}
		if !e.Healthy {
			anyUnhealthy = true
		}
		entries = append(entries, e)
	}

	if backendHealthJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
		if anyUnhealthy {
			os.Exit(1)
		}
		return
	}

	fmt.Printf("%-12s %-9s %-11s %-20s %-9s %s\n", "NAME", "HEALTHY", "INSTALLED", "VERSION", "API KEY", "MESSAGE")
	for _, e := range entries {
		version := e.Version
		if version == "" {
			version = "-"
		}
		fmt.Printf("%-12s %-9s %-11s %-20s %-9s %s\n",
			e.Name,
			boolSymbol(e.Healthy),
			boolSymbol(e.Installed),
			version,
			boolSymbol(e.APIKeySet),
			e.Message,
		)
	}

	if anyUnhealthy {
		os.Exit(1)
	}
}

// --- info ---

var backendInfoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show detailed info for a specific backend",
	Args:  cobra.ExactArgs(1),
	Run:   runBackendInfo,
}

type backendInfoOutput struct {
	Name         string              `json:"name"`
	Meta         *BackendMeta        `json:"meta,omitempty"`
	Health       *HealthStatus       `json:"health,omitempty"`
	Capabilities *capabilitiesOutput `json:"capabilities,omitempty"`
	Config       []BackendOption     `json:"config,omitempty"`
}

type capabilitiesOutput struct {
	Streaming bool `json:"streaming"`
	Sessions  bool `json:"sessions"`
	ToolCtrl  bool `json:"tool_control"`
	HealthChk bool `json:"health_check"`
	Config    bool `json:"config"`
	Metadata  bool `json:"metadata"`
}

func runBackendInfo(cmd *cobra.Command, args []string) {
	name := args[0]
	b, ok := GetBackendByName(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown backend %q; available: %s\n", name, ValidBackendNames())
		os.Exit(1)
	}

	caps := InspectCapabilities(b)
	out := backendInfoOutput{
		Name: name,
		Capabilities: &capabilitiesOutput{
			Streaming: caps.HasStreaming,
			Sessions:  caps.HasSessions,
			ToolCtrl:  caps.HasToolControl,
			HealthChk: caps.HasHealthCheck,
			Config:    caps.HasConfig,
			Metadata:  caps.HasMeta,
		},
	}

	if caps.HasMeta {
		meta := caps.Meta.Meta()
		out.Meta = &meta
	}
	if caps.HasHealthCheck {
		hs := caps.Health.HealthCheck()
		out.Health = &hs
	}
	if caps.HasConfig {
		out.Config = caps.Config.Options()
	}

	if backendInfoJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}

	// Human-readable sections
	fmt.Printf("Backend: %s\n", name)

	if out.Meta != nil {
		fmt.Println("\nMetadata:")
		printField("  Display Name", out.Meta.DisplayName)
		printField("  Description", out.Meta.Description)
		printField("  URL", out.Meta.URL)
		printField("  Binary", out.Meta.BinaryName)
		printField("  Version", out.Meta.Version)
	}

	if out.Health != nil {
		fmt.Println("\nHealth:")
		fmt.Printf("  Healthy:     %s\n", boolSymbol(out.Health.Healthy))
		fmt.Printf("  Installed:   %s\n", boolSymbol(out.Health.Installed))
		printField("  Version", out.Health.Version)
		fmt.Printf("  API Key Set: %s\n", boolSymbol(out.Health.APIKeySet))
		printField("  Message", out.Health.Message)
	}

	fmt.Println("\nCapabilities:")
	capsList := []struct {
		name string
		has  bool
	}{
		{"Streaming", caps.HasStreaming},
		{"Sessions", caps.HasSessions},
		{"Tool Control", caps.HasToolControl},
		{"Health Check", caps.HasHealthCheck},
		{"Configuration", caps.HasConfig},
		{"Metadata", caps.HasMeta},
	}
	for _, c := range capsList {
		fmt.Printf("  %-16s %s\n", c.name, boolSymbol(c.has))
	}

	if len(out.Config) > 0 {
		fmt.Println("\nConfiguration Options:")
		for _, opt := range out.Config {
			fmt.Printf("  %s: %s\n", opt.Key, opt.Description)
			parts := []string{}
			if opt.Default != "" {
				parts = append(parts, fmt.Sprintf("default=%s", opt.Default))
			}
			if opt.CurrentValue != "" {
				parts = append(parts, fmt.Sprintf("current=%s", opt.CurrentValue))
			}
			if len(parts) > 0 {
				fmt.Printf("    (%s)\n", strings.Join(parts, ", "))
			}
		}
	}
}

// --- helpers ---

func boolSymbol(v bool) string {
	if v {
		return "✓"
	}
	return "✗"
}

func printField(label, value string) {
	if value == "" {
		return
	}
	fmt.Printf("%-17s %s\n", label+":", value)
}

func init() {
	backendListCmd.Flags().BoolVar(&backendListJSON, "json", false, "Output as JSON")
	backendHealthCmd.Flags().BoolVar(&backendHealthJSON, "json", false, "Output as JSON")
	backendInfoCmd.Flags().BoolVar(&backendInfoJSON, "json", false, "Output as JSON")

	backendCmd.AddCommand(backendListCmd)
	backendCmd.AddCommand(backendHealthCmd)
	backendCmd.AddCommand(backendInfoCmd)

	rootCmd.AddCommand(backendCmd)
}
