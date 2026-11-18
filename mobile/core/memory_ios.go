//go:build ios

package main

import "runtime/debug"

// Network Extensions are memory limited (~50 MB incl. FreeRDP).
func tuneMemory() {
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(28 << 20)
}
