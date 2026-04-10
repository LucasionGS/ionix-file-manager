package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ion/ionix-file-manager/internal/ui"
)

func main() {
	startDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	if len(os.Args) > 1 {
		startDir = os.Args[1]
	}

	info, err := os.Stat(startDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "not a valid directory: %s\n", startDir)
		os.Exit(1)
	}

	model := ui.New(startDir)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
