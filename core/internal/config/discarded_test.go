package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// quince#849 — THE OBLIGATION THE RULING ATTACHED TO THE BOOLEAN.
//
// `discarded` carries the FATALITY and `warnings` carries the CAUSE, and the screen renders one
// while branching on the other. That split only works if **every** discard path records its cause in
// `warnings` — which all three do today, deliberately: the validation branch copies each error in an
// explicit loop, and the other two write their own sentence.
//
// Deliberate is not the same as pinned. This rung has already paid for one unpinned dependency —
// quince#852's guard keyed on `Errors`, which only one of the three paths fills, and it survived a
// ruling, an implementation and a review. **A boolean whose companion detail can go silently empty is
// the same shape**: the screen would name a problem and show nothing to fix.
func TestEveryDiscardPathIsDiscardedAndCarriesItsCauseInWarnings(t *testing.T) {
	cases := []struct {
		name string
		// setup writes whatever makes Load discard, and returns the config path.
		setup func(t *testing.T) string
	}{
		{
			// `os.Stat` succeeds so Load gets past the no-file case; `os.ReadFile` fails. A mode of
			// 000 would not do it — the suite runs as root, where that is still readable.
			name: "the file cannot be read at all",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "config.yml")
				if err := os.Mkdir(p, 0o700); err != nil {
					t.Fatal(err)
				}
				return p
			},
		},
		{
			name: "the file is not valid YAML",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "config.yml")
				if err := os.WriteFile(p, []byte("storage:\n  - name: one\n    path: [/backups\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
		},
		{
			// The only path that also fills `Errors` — and the only one quince#852's first guard
			// covered. `mode: exec` was retired by quince#793 and is refused by name.
			name: "the file parses and fails validation",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "config.yml")
				body := "storage:\n  - name: one\n    path: /backups-a\n    default: true\n" +
					"    backend: zfs\n    zfs:\n      parent_dataset: tank/one\n      mode: exec\n" +
					"      hook_cmd: ssh h\n"
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := c.setup(t)
			l := Load(path)

			if l.OK {
				t.Fatalf("Load reported OK — this case is meant to be a discard")
			}
			// THE CAUSE, WITHOUT WHICH THE BOOLEAN IS A PROBLEM NOBODY CAN ACT ON.
			if len(l.Warnings) == 0 {
				t.Fatalf("a discarded config carries NO warning — the screen would name a problem and "+
					"show nothing to fix (Errors: %v)", l.Errors)
			}
			for i, w := range l.Warnings {
				if w.Message == "" {
					t.Fatalf("warning %d has an empty message — a path with no message is a code, not an answer", i)
				}
			}

			// AND THE SERVICE AGREES WITH THE LOAD. `discarded` is `!OK` and must not drift from it.
			svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if !svc.Discarded() {
				t.Fatalf("Service.Discarded() is false for a config Load refused")
			}
			_, warns, _ := svc.Snapshot()
			if len(warns) == 0 {
				t.Fatalf("the snapshot serves no warnings for a discarded config — the cause never reaches a client")
			}
		})
	}
}

// AND THE ORDINARY CASES ARE NOT DISCARDED, which is what stops the above from passing on a service
// that simply always says true.
func TestDiscardedIsFalseWhenTheFileWasAccepted(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"a clean config", "storage:\n  - name: one\n    path: /backups-a\n    default: true\n"},
		// A WARNING IS NOT A DISCARD — §6 makes an unknown key a warning and never an error, the
		// config is kept, and `discarded` must say so or the screen inverts its headline for a typo.
		{"a config with an unknown key", "storage:\n  - name: one\n    path: /backups-a\n    default: true\ntotally_unknown_key: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if svc.Discarded() {
				t.Fatalf("Discarded() is true for a config that was accepted")
			}
		})
	}
}

// THERE IS NO FILE AT ALL — a fresh install, which is NOT a discard. `Load` returns `OK: true` with
// defaults, and this is the state the first-run screen exists for: the headline must stay
// "Add your first storage" rather than claiming the config could not be read.
func TestDiscardedIsFalseOnAFreshInstallWithNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.Discarded() {
		t.Fatalf("Discarded() is true when there is no config file — a fresh install is not a discard")
	}
}
