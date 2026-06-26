//go:build windows

package binutil

import (
	"os/exec"
	"syscall"
)

// HideWindow verhindert ein sichtbares Konsolenfenster für den Subprozess.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
