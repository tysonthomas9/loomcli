package archtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// withRepositoryProfileCache gives one repository-scale package analysis an
// isolated build cache and removes it before the next profile starts. Cross
// target and tag-profile export data otherwise accumulates in the caller's
// GOCACHE for the entire eleven-profile matrix. Reuse is semantically safe,
// but retaining every content-addressed variant makes peak disk usage scale
// with the number of profiles instead of the largest individual profile.
func withRepositoryProfileCache(profile AnalysisProfile, analyze func([]string) error) (resultErr error) {
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
