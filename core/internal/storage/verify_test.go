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
	r := Verify(dir)
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
	r := Verify(dir)
	if !r.OK {
		t.Fatalf("unencrypted should verify: %s", r.Detail)
	}
	if r.Encrypted {
		t.Fatal("got encrypted=true, want false")
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
			if r := Verify(dir); r.OK {
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
	if r := Verify(dir); r.OK {
		t.Fatal("missing Manifest.db should fail verification")
	}
}
