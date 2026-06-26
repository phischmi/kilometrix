//go:build !windows

package binutil

import "os/exec"

func HideWindow(cmd *exec.Cmd) {}
