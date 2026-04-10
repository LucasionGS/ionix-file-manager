package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	_ "image/gif"
	_ "image/jpeg"

	"github.com/LucasionGS/ionix-file-manager/internal/ui"
)

func main() {
	startDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot determine working directory: %v\n", err)
		os.Exit(1)
	}
	selectName := ""

	if len(os.Args) > 1 {
		arg := os.Args[1]
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no such file or directory: %s\n", arg)
			os.Exit(1)
		}
		if info.IsDir() {
			startDir = arg
		} else {
			// Argument is a file — open its parent and pre-select the file.
			startDir = filepath.Dir(arg)
			selectName = filepath.Base(arg)
		}
	}

	model := ui.New(startDir, selectName)
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
