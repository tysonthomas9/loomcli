package cleanup

import "github.com/tysonthomas9/loomcli/internal/cli/monitor"

func renderBoxTop() string                { return monitor.RenderBoxTop() }
func renderBoxLine(content string) string { return monitor.RenderBoxLine(content) }
func displayWidth(s string) int           { return monitor.DisplayWidth(s) }
