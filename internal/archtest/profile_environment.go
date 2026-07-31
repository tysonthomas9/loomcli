package archtest

import (
	"os"
	"strconv"
	"strings"
)

// repositoryPackageBuildParallelism bounds compiler subprocess fan-out inside
// each packages.Load call. profileEnvironment deliberately clears inherited
// GOFLAGS, so the analyzer owns this limit instead of relying on the caller's
// machine or CI configuration.
const repositoryPackageBuildParallelism = 2

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

func profileEnvironmentWithCache(profile AnalysisProfile, cacheDir string) []string {
	base := profileEnvironment(profile)
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, "GOCACHE=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GOCACHE="+cacheDir)
}

func profileBuildFlags(profile AnalysisProfile) []string {
	tags := append([]string(nil), profile.Tags...)
	if profile.Race {
		// The matrix calls for race source selection, not execution. The implicit
		// race build tag selects the same files without requiring cross-CGO builds.
		tags = append(tags, "race")
	}
	flags := []string{"-p=" + strconv.Itoa(repositoryPackageBuildParallelism)}
	if len(tags) > 0 {
		flags = append(flags, "-tags="+strings.Join(tags, ","))
	}
	return flags
}
