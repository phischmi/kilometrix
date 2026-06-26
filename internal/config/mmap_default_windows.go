//go:build windows

package config

// mmapDefault ist auf Windows false: Windows-mmap ist beim ersten Laden eines großen
// OSRM-Graphen deutlich langsamer als auf Linux/macOS — der Graph wird vollständig in
// RAM geladen, was den Start beschleunigt (auf Kosten von mehr Arbeitsspeicher).
// Mit OSRM_ROUTED_MMAP=true kann mmap explizit erzwungen werden.
const mmapDefault = false
