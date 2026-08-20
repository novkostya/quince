package deviceops

import (
	"testing"

	"github.com/novkostya/quince/core/internal/muxd"
)

// `idevicepair` prints SUCCESS whether or not the muxer saved the record — lockdownd_pair calls
// userpref_save_pair_record and discards its return value. So the op's verdict cannot come from
// the tool, and these are the cases that decide it (qn.6r D3).
func TestPairOutcomeSplitsNotRecordedFromCannotTell(t *testing.T) {
	present := muxd.PairRecord{State: muxd.PairRecordPresent, Digest: [32]byte{1}}
	other := muxd.PairRecord{State: muxd.PairRecordPresent, Digest: [32]byte{2}}
	absent := muxd.PairRecord{State: muxd.PairRecordAbsent}
	unknown := muxd.PairRecord{State: muxd.PairRecordUnknown}

	cases := []struct {
		name          string
		before, after muxd.PairRecord
		wantOK        bool
		wantCode      string
	}{
		{"first pairing", absent, present, true, ""},
		{"re-pair writes a new record", present, other, true, ""},
		{"STALE record, save refused", present, present, false, "pairing_not_recorded"},
		{"nothing written at all", absent, absent, false, "pairing_not_recorded"},
		{"muxer unreachable before", unknown, present, false, "pairing_unverified"},
		{"muxer unreachable after", absent, unknown, false, "pairing_unverified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, code, msg := pairOutcomeFromRecords(c.before, c.after)
			if ok != c.wantOK || code != c.wantCode {
				t.Fatalf("ok=%v code=%q, want ok=%v code=%q", ok, code, c.wantOK, c.wantCode)
			}
			if !ok && msg == "" {
				t.Fatal("a failure with no message — the user is told nothing")
			}
		})
	}
}

// Troubleshooting is ACTIONABLE: a true sentence covering both causes is still a defect, so the
// two failures must not read the same and each must name what to do.
func TestTheTwoFailureMessagesAreDistinctAndActionable(t *testing.T) {
	_, _, notRecorded := pairOutcomeFromRecords(
		muxd.PairRecord{State: muxd.PairRecordAbsent},
		muxd.PairRecord{State: muxd.PairRecordAbsent})
	_, _, unverified := pairOutcomeFromRecords(
		muxd.PairRecord{State: muxd.PairRecordUnknown},
		muxd.PairRecord{State: muxd.PairRecordUnknown})

	if notRecorded == unverified {
		t.Fatal("the two causes produce one sentence — they have different remedies")
	}
	if !contains(notRecorded, "--plist-storage") {
		t.Error("the not-recorded message does not name the muxer's store, so it is not actionable")
	}
	if !contains(unverified, "muxer") {
		t.Error("the unverified message does not name the muxer")
	}
	for _, m := range []string{notRecorded, unverified} {
		if !contains(m, "again") {
			t.Errorf("no next step for the user in: %q", m)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
