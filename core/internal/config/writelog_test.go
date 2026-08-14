package config

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// EVERY WRITE TO config.yml SAYS SO (quince#967).
//
// A storage's declaration disappeared on a lab rig and the only trace was the APPLIER reacting to
// the consequence, forty-four seconds after a restart. The file had at least five writers and no
// write record, so the change could not be attributed afterwards — and `config.yml` is not an
// implementation detail here: D12 makes it the product's headline promise.
//
// THE TEST IS ABOUT ATTRIBUTION, so it asserts the SOURCE rather than merely that something was
// logged. A line saying "config written" with no door named would be the same defect one step
// quieter.
func newLoggingService(t *testing.T) (*Service, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	svc := NewService(filepath.Join(t.TempDir(), "config.yml"),
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return svc, &buf
}

func TestEveryWriteNamesTheDoorItCameThrough(t *testing.T) {
	one := StorageEntry{Name: "local", Path: "/backups", Default: true}

	spare := StorageEntry{Name: "second", Path: "/spare"}

	for _, tc := range []struct {
		name string
		// seed is what the config holds before the write. It is per case rather than shared,
		// because the forget case needs something to remove AND a survivor — the storage
		// requirement refuses a write that reduces the count to zero (quince#942), so forgetting
		// the only entry is a refusal rather than the write this asserts.
		seed   []StorageEntry
		write  func(*Service) error
		source string
	}{
		{
			name: "a full-document replace",
			seed: []StorageEntry{one},
			write: func(s *Service) error {
				_, _, err := s.Replace(withStorages(one), SourcePutConfig)
				return err
			},
			source: SourcePutConfig,
		},
		{
			name: "an add",
			seed: []StorageEntry{one},
			write: func(s *Service) error {
				_, _, _, err := s.AddStorage(StorageEntry{Name: "second", Path: "/spare", Backend: "copy"})
				return err
			},
			source: SourceAddStorage,
		},
		{
			// THE INCIDENT'S OWN SHAPE. A declaration that vanishes is what quince#967 is about, and
			// this is the door that removes one on purpose — so it is the one whose line a reader
			// will be looking for when the next entry goes missing.
			name: "a forget",
			seed: []StorageEntry{one, spare},
			write: func(s *Service) error {
				_, _, _, err := s.ForgetStorage("second", func(string) string { return "" })
				return err
			},
			source: SourceForgetStorage,
		},
		{
			name: "the transport opt-in",
			seed: []StorageEntry{one},
			write: func(s *Service) error {
				_, _, err := s.SetAllowInsecureTransport(true)
				return err
			},
			source: SourceInsecureTransport,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, buf := newLoggingService(t)
			// SEEDED THROUGH `Replace` FIRST, so every case starts from a config that already holds a
			// storage — an add is then a second entry and a forget has something to remove.
			if _, _, err := svc.Replace(withStorages(tc.seed...), SourcePutConfig); err != nil {
				t.Fatalf("seed: %v", err)
			}
			buf.Reset()

			if err := tc.write(svc); err != nil {
				t.Fatalf("write: %v", err)
			}

			got := buf.String()
			if !strings.Contains(got, "config written") {
				t.Fatalf("the write logged nothing:\n%s", got)
			}
			if !strings.Contains(got, tc.source) {
				t.Errorf("the write did not name its door %q — a line nobody can attribute is the "+
					"defect quince#967 filed:\n%s", tc.source, got)
			}
		})
	}
}

// THE STORAGE COUNT IS CALLED OUT SEPARATELY from the changed key paths, because a disappearing
// storage is the incident this was filed for and `storage` in a list of changed paths does not
// distinguish an edit from a deletion.
func TestTheWriteRecordsTheStorageCountAcrossTheChange(t *testing.T) {
	svc, buf := newLoggingService(t)
	two := []StorageEntry{
		{Name: "local", Path: "/backups", Default: true},
		{Name: "spare", Path: "/spare"},
	}
	if _, _, err := svc.Replace(withStorages(two...), SourcePutConfig); err != nil {
		t.Fatalf("seed: %v", err)
	}
	buf.Reset()

	// 2 → 1: exactly the shape of the incident, and permitted, because it does not reduce to zero.
	if _, _, _, err := svc.ForgetStorage("spare", func(string) string { return "" }); err != nil {
		t.Fatalf("forget: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"storages_before=2", "storages_after=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("the write does not record %q, so a vanished declaration is still not "+
				"attributable from the log alone:\n%s", want, got)
		}
	}
}

// A REFUSED WRITE LOGS NOTHING. The line means *the file changed*; emitting it for a rejected write
// would make the record actively misleading — a reader chasing a disappearance would find a write
// that never happened at the moment it did not happen.
func TestARefusedWriteIsNotRecordedAsAWrite(t *testing.T) {
	svc, buf := newLoggingService(t)
	if _, _, err := svc.Replace(withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true}), SourcePutConfig); err != nil {
		t.Fatalf("seed: %v", err)
	}
	buf.Reset()

	// 1 → 0 is refused (the storage requirement is a transition, quince#942).
	if errs, _, err := svc.Replace(withStorages(), SourcePutConfig); err != nil || len(errs) == 0 {
		t.Fatalf("precondition: removing the last storage must be refused; errs=%+v err=%v", errs, err)
	}

	if strings.Contains(buf.String(), "config written") {
		t.Errorf("a REFUSED write was logged as a write:\n%s", buf.String())
	}
}
