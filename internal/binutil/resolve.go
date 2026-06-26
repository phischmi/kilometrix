// Package binutil enthält plattformübergreifende Helfer für externe Prozesse.
package binutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolve sucht ein Binary: erst im PATH, dann neben dem eigenen Executable.
// Auf Windows wird ggf. ".exe" ergänzt. Gibt "" zurück wenn nichts gefunden.
func Resolve(name string) string {
	if filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator) {
		if _, err := os.Stat(name); err == nil {
			return name
		}
		return ""
	}
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		candidates = append(candidates, name+".exe")
	}
	for _, n := range candidates {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
		if exe, err := os.Executable(); err == nil {
			p := filepath.Join(filepath.Dir(exe), n)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
