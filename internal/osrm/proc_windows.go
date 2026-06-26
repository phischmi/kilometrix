//go:build windows

package osrm

import "os/exec"

// sendStop killt den Prozess sofort: osrm-routed hängt auf Windows beim graceful
// shutdown (mmap-Teardown), daher direkter Kill ohne Wartezeit.
func sendStop(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
