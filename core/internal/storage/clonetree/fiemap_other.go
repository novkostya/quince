//go:build !linux

package clonetree

import "errors"

// Sharing — see fiemap_linux.go. Off Linux there is no FIEMAP and nothing to ask, so the only
// value ever produced here is SharingUnknown. Present so the package builds under a non-linux
// `go vet`/editor; quince ships only Linux containers.
type Sharing int

const (
	SharingUnknown Sharing = iota
	SharingShared
	SharingUnshared
)

func (s Sharing) String() string {
	switch s {
	case SharingShared:
		return "shared"
	case SharingUnshared:
		return "unshared"
	default:
		return "unknown"
	}
}

func extentSharing(path string) (Sharing, error) {
	return SharingUnknown, errors.New("clonetree: extent sharing is only observable on Linux (FIEMAP)")
}
