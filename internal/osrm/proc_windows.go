//go:build windows

package osrm

import "os/exec"

// sendStop ist auf Windows ein No-op: Ctrl+C wird von Windows an alle Prozesse der
// Konsolen-Gruppe gesendet, also hat osrm-routed das Signal bereits empfangen und
// fährt selbst herunter. Ein sofortiges Kill() würde das laufende mmap-Teardown
// unterbrechen und den Prozess hängen lassen. Stop() killt nach dem Timeout hart.
func sendStop(_ *exec.Cmd) error { return nil }
