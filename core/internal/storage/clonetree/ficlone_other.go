//go:build !linux

package clonetree

// reflinkFile is unavailable off Linux (quince ships only Linux containers). Present so the
// package builds under a non-linux `go vet`/editor; the probe reports reflink unsupported and
// the storage auto-selection falls through to hardlink/copy.
func reflinkFile(dst, src string) error { return ErrReflinkUnsupported }
