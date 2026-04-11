//go:build !linux && !darwin

package ui

import (
	"os"
	"syscall"
)

var (
	sigStop os.Signal = syscall.SIGINT // fallback: just interrupt
	sigCont os.Signal = syscall.SIGCONT
)
