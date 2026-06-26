//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func setProcAttr(cmd *exec.Cmd) {}

func sendStop(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}
