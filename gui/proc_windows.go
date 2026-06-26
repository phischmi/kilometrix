//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func sendStop(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Signal(os.Interrupt) ist auf Windows nicht unterstützt.
	// taskkill /T beendet auch Child-Prozesse (z. B. osrm-routed).
	tk := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return tk.Run()
}
