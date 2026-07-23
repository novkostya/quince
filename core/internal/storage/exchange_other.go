//go:build !linux

package storage

import "fmt"

// exchange is Linux-only: renameat2(RENAME_EXCHANGE) is a Linux syscall. This stub
// lets non-Linux editor tooling compile (the workstation is macOS); quince runs
// only on Linux and the gate ladder executes in the Linux toolchain container, so
// this path is never taken in production or CI.
func exchange(a, b string) error {
	return fmt.Errorf("storage: atomic directory exchange requires Linux (renameat2 RENAME_EXCHANGE): %s <-> %s", a, b)
}
