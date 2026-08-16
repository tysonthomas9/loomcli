package archtest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type profileTargetGroup struct {
	indices []int
}

func groupProfileIndices(profiles []AnalysisProfile) []profileTargetGroup {
	groups := make([]profileTargetGroup, 0)
	groupByTarget := map[string]int{}
	for index, profile := range profiles {
		key := profile.GOOS + "/" + profile.GOARCH
		groupIndex, ok := groupByTarget[key]
		if !ok {
			groupIndex = len(groups)
			groupByTarget[key] = groupIndex
			groups = append(groups, profileTargetGroup{})
		}
		groups[groupIndex].indices = append(groups[groupIndex].indices, index)
	}
	return groups
}

// withRepositoryProfileCaches analyzes profiles serially while sharing one
// content-addressed build cache across profiles with the same GOOS/GOARCH.
// Tagged and race variants reuse their target's compiled dependencies; the
// disposable cache is removed before the next cross target starts, so disk
// remains bounded by one target graph rather than the whole matrix.
func withRepositoryProfileCaches(
	profiles []AnalysisProfile,
	analyze func(index int, profile AnalysisProfile, environment []string) error,
) (resultErr error) {
	for _, group := range groupProfileIndices(profiles) {
		first := profiles[group.indices[0]]
		cacheDir, shared := reusableNativeProfileCache(first)
		if !shared {
			var err error
			cacheDir, err = os.MkdirTemp("", "loom-archcheck-gocache-")
			if err != nil {
				return fmt.Errorf("create build cache for target %s/%s: %w", first.GOOS, first.GOARCH, err)
			}
			cacheDir, err = filepath.Abs(cacheDir)
			if err != nil {
				_ = os.RemoveAll(cacheDir)
				return fmt.Errorf("resolve build cache for target %s/%s: %w", first.GOOS, first.GOARCH, err)
			}
		}

		var analysisErr error
		for _, index := range group.indices {
			profile := profiles[index]
			if err := analyze(index, profile, profileEnvironmentWithCache(profile, cacheDir)); err != nil {
				analysisErr = fmt.Errorf("analyze profile %s: %w", profile.Name, err)
				break
			}
		}
		if !shared {
			if err := os.RemoveAll(cacheDir); err != nil {
				analysisErr = errors.Join(analysisErr, fmt.Errorf("remove build cache for target %s/%s: %w", first.GOOS, first.GOARCH, err))
			}
		}
		if analysisErr != nil {
			return analysisErr
		}
	}
	return nil
}

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

// reusableNativeProfileCache avoids recompiling the repository and standard
// library from an empty cache for every native tag/race profile. Go's build
// cache is content addressed, so profile-specific variants cannot be confused
// with one another and the toolchain applies its normal bounded cache
// maintenance. Cross-compiled profiles remain isolated and are removed after
// each analysis so foreign-target variants do not accumulate locally.
func reusableNativeProfileCache(profile AnalysisProfile) (string, bool) {
	if profile.GOOS != runtime.GOOS || profile.GOARCH != runtime.GOARCH {
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
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	cacheDir := filepath.Join(userCacheDir, "go-build")
	info, err := os.Stat(cacheDir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return filepath.Clean(cacheDir), true
}
