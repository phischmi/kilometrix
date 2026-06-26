//go:build !windows

package config

// mmapDefault ist auf Linux/macOS true: mmap spart Arbeitsspeicher im Leerlauf
// (wichtig auf der RAM-knappen NAS). Auf Windows ist der Default false (siehe
// mmap_default_windows.go).
const mmapDefault = true
