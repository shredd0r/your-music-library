// Command ymlib opens the terminal UI. It takes no arguments.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"your-music-library/internal/app"
	"your-music-library/internal/platform"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ymlib:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		return errors.New("ymlib takes no arguments: everything happens in the terminal UI")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("ymlib is a terminal UI; run it in a terminal")
	}
	dirs, err := platform.UserDirs()
	if err != nil {
		return err
	}
	return app.Run(context.Background(), dirs, os.Stdout)
}
