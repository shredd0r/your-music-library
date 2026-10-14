package main

import (
	"fmt"
	"os"

	"your-music-library/internal/cli"
	"your-music-library/internal/platform"
)

func main() {
	dirs, err := platform.UserDirs()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ymlib:", err)
		os.Exit(1)
	}
	if err := cli.NewRootCommand(dirs).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ymlib:", err)
		os.Exit(1)
	}
}
