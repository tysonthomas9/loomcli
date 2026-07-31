package archtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// withRepositoryProfileCache gives one repository-scale package analysis an
// isolated build cache and removes it before the next profile starts. Cross
// target and tag-profile export data otherwise accumulates in the caller's
// GOCACHE for the entire eleven-profile matrix. Reuse is semantically safe,
// but retaining every content-addressed variant makes peak disk usage scale
// with the number of profiles instead of the largest individual profile.
func withRepositoryProfileCache(profile AnalysisProfile, analyze func([]string) error) (resultErr error) {
	if cacheDir, ok := reusableNativeProfileCache(profile); ok {
		return analyze(profileEnvironmentWithCache(profile, cacheDir))
	}
	cacheDir, err := os.MkdirTemp("", "loom-archcheck-gocache-")
	if err != nil {
		return fmt.Errorf("create build cache for profile %s: %w", profile.Name, err)
	}
	absoluteCacheDir, err := filepath.Abs(cacheDir)
	if err != nil {
		_ = os.RemoveAll(cacheDir)
		return fmt.Errorf("resolve build cache for profile %s: %w", profile.Name, err)
	}
	cacheDir = absoluteCacheDir
	defer func() {
		if err := os.RemoveAll(cacheDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove build cache for profile %s: %w", profile.Name, err))
		}
	}()

	return analyze(profileEnvironmentWithCache(profile, cacheDir))
}

// reusableNativeProfileCache avoids holding two full native compilation
// caches at once during make gate. The preceding vet/build/lint stages have
// already populated the caller-owned cache for this exact untagged target, so
// reuse adds no cross-target variants. Tagged, race, and cross-compiled
// profiles remain isolated and are removed after each analysis.
func reusableNativeProfileCache(profile AnalysisProfile) (string, bool) {
	if profile.GOOS != runtime.GOOS || profile.GOARCH != runtime.GOARCH || profile.Race || len(profile.Tags) != 0 {
		return "", false
	}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name != "GOCACHE" || !filepath.IsAbs(value) {
			continue
		}
		info, err := os.Stat(value)
		if err == nil && info.IsDir() {
			return filepath.Clean(value), true
		}
		return "", false
	}
	return "", false
}
