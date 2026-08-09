//go:build lab

// The filesystem matrix for the storage auto-selection probe — NOT compiled or run in CI (build
// tag `lab`), because it needs one real filesystem per backend tier and a CI runner has one.
//
// It exists for quince#747: the sharing probe's whole point is that it behaves differently on
// filesystems a single test machine cannot present at once. Point it at a directory on each tier
// in turn, DECLARING what that tier must answer, and it asserts rather than prints — a run that
// only printed what it found could be read generously afterwards.
//
//	QUINCE_LAB_FS_DIR             the directory to probe (a fresh subdirectory is made under it)
//	QUINCE_LAB_FS_EXPECT          reflink | hardlink | copy — the backend this tier must select
//	QUINCE_LAB_FS_EXPECT_SHARING  shared | unverifiable | none — what the reflink probe must find
//
// Build it once and run the binary on a host that has no Go toolchain, which is how the lab rig
// was measured:
//
//	go test -c -tags lab -o /tmp/fsmatrix ./internal/storage/clonetree
//	QUINCE_LAB_FS_DIR=<tier-mount> QUINCE_LAB_FS_EXPECT=reflink \
//	  QUINCE_LAB_FS_EXPECT_SHARING=shared /tmp/fsmatrix -test.run TestLabFilesystemMatrix -test.v
//
// It hardcodes NO infrastructure (privacy rule): every path comes from the environment.
//
// AN EXTERNAL TEST PACKAGE, deliberately. It asserts the BACKEND the storage package selects, so
// it imports storage — and storage imports clonetree, so an in-package test would be a cycle.
package clonetree_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

func TestLabFilesystemMatrix(t *testing.T) {
	root := os.Getenv("QUINCE_LAB_FS_DIR")
	if root == "" {
		t.Skip("set QUINCE_LAB_FS_DIR to a directory on the filesystem tier under test")
	}
	// A FRESH directory per run. Storage adoption reads a marker and deliberately does not
	// re-probe, so a reused directory would answer about the last run rather than this one.
	dir, err := os.MkdirTemp(root, "quince-fsmatrix-")
	if err != nil {
		t.Fatalf("cannot create a probe dir under %s: %v", root, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	res, detail := clonetree.ReflinkProbeDetail(dir)
	t.Logf("dir=%s\n  reflink probe : %s — %s", dir, reflinkResultName(res), detail)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, backend, reason := storage.Select(context.Background(), storage.Options{
		Backend: "auto", Backups: dir, AppVersion: "lab",
	}, log)
	t.Logf("  backend       : %s\n  reason        : %s", backend, reason)

	if want := os.Getenv("QUINCE_LAB_FS_EXPECT"); want != "" && backend != want {
		t.Errorf("backend = %q, declared %q", backend, want)
	}
	if want := os.Getenv("QUINCE_LAB_FS_EXPECT_SHARING"); want != "" {
		if got := reflinkResultName(res); got != want {
			t.Errorf("reflink probe = %q, declared %q", got, want)
		}
	}
}

func reflinkResultName(r clonetree.ReflinkResult) string {
	switch r {
	case clonetree.ReflinkSharing:
		return "shared"
	case clonetree.ReflinkSharingUnverifiable:
		return "unverifiable"
	case clonetree.ReflinkUnsupported:
		return "none"
	}
	return fmt.Sprintf("unknown(%d)", int(r))
}
