package defs

import "strings"

func durableAgentRepos(repos []string) []string {
	out := make([]string, 0, len(repos))
	for _, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" || repo == "." {
			continue
		}
		out = append(out, repo)
	}
	return compactStrings(out)
}

func ptrSlice(values []string) *[]string {
	return &values
}
