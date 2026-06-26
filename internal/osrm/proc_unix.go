//go:build !windows

package osrm

import (
	"os"
	"os/exec"
)

func sendStop(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}
