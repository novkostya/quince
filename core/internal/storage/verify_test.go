package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// Story 9: structural verification, both encryption variants (amendment A1).

func TestVerifyEncryptedFullPasses(t *testing.T) {
	dir := t.TempDir()
	goodEncryptedFull(t, dir)
	r := Verify(dir, "full") // kind is the authoritative seed-derived value (qn.5b)
	if !r.OK {
		t.Fatalf("encrypted full should verify: %s", r.Detail)
	}
	if !r.Encrypted || r.Kind != "full" {
		t.Fatalf("got encrypted=%v kind=%q, want true/full", r.Encrypted, r.Kind)
	}
}

func TestVerifyUnencryptedPasses(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir, treeOpts{encrypted: false, full: true})
	r := Verify(dir, "full")
	if !r.OK {
		t.Fatalf("unencrypted should verify: %s", r.Detail)
	}
	if r.Encrypted {
		t.Fatal("got encrypted=true, want false")
	}
}

// Story 7: the shard check fires on a genuine full backup (kind="full") — a shard-less "full"
// FAILS — but is skipped on an incremental (kind="incremental"), where few/no new blobs is
// legitimate. This is finding #9(a): the kind is authoritative from the seed decision, not the
// lying Status.plist.IsFullBackup.
func TestVerifyShardCheckGatedByKind(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir, treeOpts{encrypted: true, full: true, emptyShards: true})
	if r := Verify(dir, "full"); r.OK {
		t.Fatal("a shard-less encrypted FULL backup must fail verification")
	}
	if r := Verify(dir, "incremental"); !r.OK {
		t.Fatalf("the shard check must be skipped on an incremental: %s", r.Detail)
	}
}

func TestVerifyFailures(t *testing.T) {
	cases := map[string]treeOpts{
		"torn status":                     {encrypted: true, full: true, tornStatus: true},
		"bad manifest plist":              {encrypted: true, full: true, badManifestPlist: true},
		"encrypted but plaintext sqlite":  {encrypted: true, full: true, encPlaintextManifest: true},
		"encrypted full with no shards":   {encrypted: true, full: true, emptyShards: true},
		"unencrypted with a missing blob": {encrypted: false, full: true, missingBlob: true},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			buildTree(t, dir, o)
			if r := Verify(dir, "full"); r.OK {
				t.Fatalf("%s should FAIL verification, but passed", name)
			}
		})
	}
}

func TestVerifyMissingManifestDB(t *testing.T) {
	dir := t.TempDir()
	goodEncryptedFull(t, dir)
	if err := os.Remove(filepath.Join(dir, "Manifest.db")); err != nil {
		t.Fatal(err)
	}
	if r := Verify(dir, "full"); r.OK {
		t.Fatal("missing Manifest.db should fail verification")
	}
}
