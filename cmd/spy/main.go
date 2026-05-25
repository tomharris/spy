package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tomharris/spy/internal/auth"
)

func main() {
	var forceRefresh bool
	flag.BoolVar(&forceRefresh, "refresh", false, "skip the on-disk token cache and re-extract from Slack")
	flag.Parse()

	src, err := auth.DefaultSource()
	if err != nil {
		die(err)
	}

	creds, err := auth.GetCredentials(src, forceRefresh)
	if err != nil {
		die(err)
	}

	res, err := auth.Test(creds)
	if err != nil {
		die(err)
	}
	if !res.Ok {
		die(fmt.Errorf("auth.test failed: %s", res.Error))
	}

	fmt.Printf("authenticated as %s @ %s\n", res.User, res.Team)
	fmt.Printf("  team id: %s\n", res.TeamID)
	fmt.Printf("  user id: %s\n", res.UserID)
	fmt.Printf("  url:     %s\n", res.URL)
	fmt.Printf("  source:  %s%s\n", src.SlackDir, appStoreMarker(src.IsAppStore))
}

func appStoreMarker(b bool) string {
	if b {
		return " (App Store)"
	}
	return ""
}

func die(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
