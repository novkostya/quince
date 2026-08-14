package auth

import (
	"errors"
	"testing"
)

// qn.6o slice 2 gates G1–G4. `accepts` is what the server says WOULD satisfy a refusal it has
// already decided; these fix the four properties the rung rests on.

// G1 — `accepts` IS COMPUTED PER OPERATION, ON THE CREDENTIALS THIS INSTALL ACTUALLY HOLDS.
//
// Table-driven over all four operations × the four install shapes, because the interesting failures
// are cells rather than rows: a lookup table keyed on the operation alone passes every
// password-and-passkey case and is wrong everywhere else.
//
// THE `want` COLUMN IS WRITTEN OUT IN FULL rather than derived, deliberately. A `want` computed by
// the same rules as the code would agree with the code by construction, which is the way a
// table-driven test becomes a restatement of the implementation.
//
// THE CREDENTIAL IDS HERE ARE FOUR CHARACTERS, AND THE LENGTH IS LOAD-BEARING — do not "tidy" them
// to match the `cred-1` used elsewhere in this package.
//
// `allowedForRemoval` compares `base64url(decode(stored))` against the target, so an id that does
// not round-trip cannot be excluded. `cred-1` does not: it decodes to four bytes and re-encodes to
// `cred-w`, AND SO DOES `cred-2` — the two alias, to each other and to a third string. A 4-character
// id is exactly 3 bytes with no leftover bits, so it round-trips and stays distinct.
//
// Production is unaffected: a real credential id is stored as the canonical encoding of the bytes
// the authenticator produced. It is the FIXTURES that cannot express what this gate asserts, which
// is why the test carries the constraint rather than the code.
func TestAcceptsIsComputedPerOperationAndInstall(t *testing.T) {
	const target = "cre1"

	cases := []struct {
		name        string
		password    bool
		passkeyHere bool // a credential at `here`, id `cre1` — the removal target
		passkeyAlso bool // a SECOND credential at `here`, so a removal has something else to use
		op          ProofOperation
		want        []string
	}{
		// ADD A PASSKEY — the regression's own operation. Both factors count.
		{"add, password only", true, false, false, OpAddPasskey, []string{FactorPassword}},
		{"add, passkey only", false, true, false, OpAddPasskey, []string{FactorPasskey}},
		{"add, both", true, true, false, OpAddPasskey, []string{FactorPassword, FactorPasskey}},
		{"add, neither", false, false, false, OpAddPasskey, nil},

		// SET A PASSWORD — same shape; changing the password takes any present credential (rule 3).
		{"set, password only", true, false, false, OpSetPassword, []string{FactorPassword}},
		{"set, passkey only", false, true, false, OpSetPassword, []string{FactorPasskey}},
		{"set, both", true, true, false, OpSetPassword, []string{FactorPassword, FactorPasskey}},
		{"set, neither", false, false, false, OpSetPassword, nil},

		// REMOVE THE PASSWORD — rule 2: the password can never authorise its own removal, so it is
		// absent from every row here however the install is shaped.
		{"remove password, password only", true, false, false, OpRemovePassword, nil},
		{"remove password, passkey only", false, true, false, OpRemovePassword, []string{FactorPasskey}},
		{"remove password, both", true, true, false, OpRemovePassword, []string{FactorPasskey}},
		{"remove password, neither", false, false, false, OpRemovePassword, nil},

		// REMOVE A PASSKEY — rule 2 again, from the other side: the target does not count itself, so
		// `passkey` appears only when a SECOND credential exists at this address.
		{"remove passkey, password + the target only", true, true, false, OpRemovePasskey, []string{FactorPassword}},
		{"remove passkey, the target only", false, true, false, OpRemovePasskey, nil},
		{"remove passkey, the target and another", false, true, true, OpRemovePasskey, []string{FactorPasskey}},
		{"remove passkey, both, and another", true, true, true, OpRemovePasskey, []string{FactorPassword, FactorPasskey}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, st := newConfiguredService(t)
			if tc.password {
				if err := svc.SetPassword("old-one", ip); err != nil {
					t.Fatalf("seed password: %v", err)
				}
			}
			if tc.passkeyHere {
				seedPasskey(t, st, target, here)
			}
			if tc.passkeyAlso {
				seedPasskey(t, st, "cre2", here)
			}

			wantTarget := ""
			if tc.op == OpRemovePasskey {
				wantTarget = target
			}
			got, err := svc.Accepts(tc.op, here, wantTarget)
			if err != nil {
				t.Fatalf("Accepts: %v", err)
			}
			if !sameFactors(got, tc.want) {
				t.Fatalf("Accepts(%s) = %v, want %v", tc.op, got, tc.want)
			}
		})
	}
}

// G2 — RULE 2's EXCLUSIONS ARE IN THE LIST, asserted on their own rather than only as cells above.
//
// The table proves the whole grid; this proves the two sentences the rung actually promises, so a
// future edit that widens the table cannot quietly drop them.
func TestAcceptsAppliesRuleTwosExclusions(t *testing.T) {
	t.Run("remove_password never offers the password", func(t *testing.T) {
		svc, st := newConfiguredService(t)
		if err := svc.SetPassword("old-one", ip); err != nil {
			t.Fatalf("seed: %v", err)
		}
		seedPasskey(t, st, "cre1", here)

		got, err := svc.Accepts(OpRemovePassword, here, "")
		if err != nil {
			t.Fatalf("Accepts: %v", err)
		}
		for _, f := range got {
			if f == FactorPassword {
				t.Fatalf("remove_password offered the password: %v", got)
			}
		}
	})

	t.Run("remove_passkey never offers the target as its own proof", func(t *testing.T) {
		svc, st := newConfiguredService(t)
		seedPasskey(t, st, "cre1", here)

		// The ONLY credential at this address is the one being removed. Nothing can prove it.
		got, err := svc.Accepts(OpRemovePasskey, here, "cre1")
		if err != nil {
			t.Fatalf("Accepts: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("the target was offered as proof of its own removal: %v", got)
		}
	})
}

// G3 — GUIDANCE, NEVER A CONTROL (D2). THIS IS THE TEST THAT KEEPS THE GUARD ON THE SERVER.
//
// A client that ignores `accepts` and presents a factor it did NOT list is still refused by the
// rule itself. If this ever passes by accident — if presenting the unlisted factor starts to
// succeed — acceptability has moved to the client, which is the failure the whole shape exists to
// prevent.
func TestAcceptsIsGuidanceAndTheRuleStillRefuses(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, st, "cre1", here)

	// `remove_password` does not list the password — G2 just fixed that.
	got, err := svc.Accepts(OpRemovePassword, here, "")
	if err != nil {
		t.Fatalf("Accepts: %v", err)
	}
	for _, f := range got {
		if f == FactorPassword {
			t.Fatalf("precondition: the password was listed after all: %v", got)
		}
	}

	// Present it anyway, and CORRECTLY — the value is the real password, so nothing but rule 2 can
	// refuse this.
	err = svc.RemovePassword(NewProofs(), Presented{Password: "old-one"}, here, sess, ip)
	if err == nil {
		t.Fatal("removing the password with the password succeeded — rule 2 is not being enforced")
	}
	var self ErrSelfRemoval
	if !errors.As(err, &self) {
		t.Fatalf("RemovePassword with the password = %v, want ErrSelfRemoval", err)
	}
	// And it really did not happen.
	if _, _, err := svc.Login("old-one", ip, ""); err != nil {
		t.Fatalf("the password went despite the refusal: %v", err)
	}
}

// G4 — A CREDENTIAL AT ANOTHER ADDRESS IS NOT A FACTOR HERE.
//
// A passkey signs for the rpId it was registered against, so one at `example.net` cannot assert at
// `example.com`. Listing it would send a user to a sheet with nothing in it — the same rule
// `provable` enforces one layer down, asserted here so the two cannot drift.
func TestAcceptsIgnoresCredentialsAtAnotherAddress(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cre1", elsewhere)

	got, err := svc.Accepts(OpAddPasskey, here, "")
	if err != nil {
		t.Fatalf("Accepts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a credential at %s was offered at %s: %v", elsewhere, here, got)
	}

	// AND THE SAME CREDENTIAL AT ITS OWN ADDRESS IS ONE, so this pins the rpId comparison rather
	// than merely an empty result.
	got, err = svc.Accepts(OpAddPasskey, elsewhere, "")
	if err != nil {
		t.Fatalf("Accepts: %v", err)
	}
	if !sameFactors(got, []string{FactorPasskey}) {
		t.Fatalf("Accepts at the credential's own address = %v, want [passkey]", got)
	}
}

// D4 — NEVER EMPTY, ALWAYS nil. The distinction is what keeps `accepts: []` off the wire, and
// `omitempty` only helps if the value really is nil rather than a zero-length slice.
func TestAcceptsIsNilRatherThanEmpty(t *testing.T) {
	svc, _ := newConfiguredService(t)

	got, err := svc.Accepts(OpAddPasskey, here, "")
	if err != nil {
		t.Fatalf("Accepts: %v", err)
	}
	if got != nil {
		t.Fatalf("a dead end returned %#v, want nil so the field is omitted entirely", got)
	}
}

// An unknown operation degrades to the field-less body rather than to an error — see `Accepts`.
func TestAcceptsSaysNothingAboutAnUnknownOperation(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cre1", here)

	got, err := svc.Accepts(ProofOperation("rename_passkey"), here, "")
	if err != nil {
		t.Fatalf("Accepts on an unknown operation = %v, want no error", err)
	}
	if got != nil {
		t.Fatalf("an unknown operation was answered %v, want nil", got)
	}
}

func sameFactors(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
