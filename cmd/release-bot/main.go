// Command release-bot releases packages from conventional commits.
// It tags merged releases, and otherwise prepares a release branch with the next versions and changelogs.
package main

import (
	"os"
	"time"

	"github.com/CallumKerson/release-bot/internal/cli"
)

func main() {
	if err := cli.NewRootCommand(time.Now, os.Getenv).Execute(); err != nil {
		os.Exit(1)
	}
}
