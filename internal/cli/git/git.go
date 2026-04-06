package git

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

var gitRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_./-]*$`)

func validateGitRef(name string) error {
	if name == "" {
		return nil
	}
	if !gitRefPattern.MatchString(name) {
		return fmt.Errorf("invalid git ref %q: must match [a-zA-Z0-9][a-zA-Z0-9_./-]*", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid git ref %q: must not contain '..'", name)
	}
	return nil
}

func runGit(deps *cli.Deps, dir string, args ...string) (string, error) {
	return cli.RunGit(deps, dir, args...)
}

func runGitOutput(deps *cli.Deps, dir string, args ...string) error {
	return cli.RunGitOutput(deps, dir, args...)
}

var defaultDeps = cli.GetDeps(nil)

func resolveRemote(remote string) string {
	if remote == "" {
		return "origin"
	}
	return remote
}
