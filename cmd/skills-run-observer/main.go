package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/cireport"
)

func main() {
	var config cireport.ObserverConfig
	flag.StringVar(&config.APIURL, "api-url", envOrDefault("GITHUB_API_URL", "https://api.github.com"), "GitHub API base URL")
	flag.StringVar(&config.Repository, "repository", "", "observed owner/repository")
	flag.Int64Var(&config.RunID, "run-id", 0, "observed workflow run ID")
	flag.StringVar(&config.HeadSHA, "head-sha", "", "expected observed workflow head SHA")
	flag.Parse()
	config.Token = os.Getenv("GITHUB_TOKEN")

	result, err := cireport.ObserveAndReport(context.Background(), config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("category=%s conclusion=%s\n", result.Category, result.Conclusion)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
