//go:build linux

package clonetree

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// The whole of quince#790's first half: EAGAIN is FICLONE saying "not this clone, not right now",
// and it used to be reported as "unsupported on this filesystem" — a permanent claim about a
// filesystem that supports reflinks.
func TestClassifyFICLONE(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"EAGAIN is transient — ZFS block cloning on an unsynced txg", unix.EAGAIN, ErrReflinkUnavailable},
		{"EOPNOTSUPP — the filesystem has no FICLONE", unix.EOPNOTSUPP, ErrReflinkUnsupported},
		{"ENOTTY — measured on ext4 and exFAT", unix.ENOTTY, ErrReflinkUnsupported},
		{"EINVAL — the pair of files cannot be cloned", unix.EINVAL, ErrReflinkUnsupported},
		{"EXDEV — cross-device, settled, never retryable", unix.EXDEV, ErrReflinkUnsupported},
		{"an errno nobody has characterised stays unsupported", unix.EIO, ErrReflinkUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyFICLONE(tc.err); !errors.Is(got, tc.want) {
				t.Errorf("classifyFICLONE(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// THE TWO SENTINELS MUST NOT MATCH EACH OTHER. A caller distinguishing them with errors.Is gets no
// warning if one is ever defined in terms of the other, and every branch in this change is such a
// caller.
func TestReflinkSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrReflinkUnavailable, ErrReflinkUnsupported) {
		t.Error("ErrReflinkUnavailable matches ErrReflinkUnsupported — the distinction is unobservable")
	}
	if errors.Is(ErrReflinkUnsupported, ErrReflinkUnavailable) {
		t.Error("ErrReflinkUnsupported matches ErrReflinkUnavailable — the distinction is unobservable")
	}
}

// ReflinkProbe answered `res != ReflinkUnsupported`, which was correct for three values and
// inverted for the fourth: a transient refusal would have reported reflink support.
func TestReflinkUsable(t *testing.T) {
	for _, tc := range []struct {
		res  ReflinkResult
		want bool
	}{
		{ReflinkSharing, true},
		{ReflinkSharingUnverifiable, true},
		{ReflinkUnsupported, false},
		{ReflinkUnavailable, false},
	} {
		if got := reflinkUsable(tc.res); got != tc.want {
			t.Errorf("reflinkUsable(%d) = %v, want %v", tc.res, got, tc.want)
		}
	}
}
