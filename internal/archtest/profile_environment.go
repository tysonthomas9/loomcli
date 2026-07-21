package archtest

import (
	"os"
	"strings"
)

func profileEnvironment(profile AnalysisProfile) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOOS=") || strings.HasPrefix(entry, "GOARCH=") ||
			strings.HasPrefix(entry, "CGO_ENABLED=") || strings.HasPrefix(entry, "GOFLAGS=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GOOS="+profile.GOOS, "GOARCH="+profile.GOARCH, "CGO_ENABLED=0", "GOFLAGS=")
}
