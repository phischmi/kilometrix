//go:build windows

package config

// mmapDefault ist auf Windows true: mmap mappt den Graph lazy von der Platte,
// der Start ist schnell. Ohne mmap lädt osrm-routed den kompletten Germany-Graph
// (~4 GB) vorab in RAM — auf Windows dauert das viele Minuten.
const mmapDefault = true
