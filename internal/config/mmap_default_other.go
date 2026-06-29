//go:build !windows

package config

// mmapDefault ist auf Linux/macOS true: mmap spart Arbeitsspeicher im Leerlauf
// (wichtig auf RAM-knappen Servern). Windows hat denselben Default (siehe
// mmap_default_windows.go).
const mmapDefault = true
