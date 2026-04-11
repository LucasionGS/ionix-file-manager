//go:build linux || darwin

package ui

import (
	"os"
	"syscall"
)

var (
	sigStop os.Signal = syscall.SIGSTOP
	sigCont os.Signal = syscall.SIGCONT
)
