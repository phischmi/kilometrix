//go:build windows

package osrm

import (
	"os/exec"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func sendStop(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Signal(os.Interrupt) ist auf Windows nicht unterstützt; wir beenden hart.
	// osrm-routed hat keinen relevanten Cleanup, Kill ist hier vertretbar.
	return cmd.Process.Kill()
}
