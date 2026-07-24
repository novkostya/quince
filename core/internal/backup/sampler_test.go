package backup

import (
	"testing"
	"time"
)

// TestLivenessBackstopExceedsToolTimeout is the qn.6b coupling guard (story 7): the sampler's
// zero-activity backstop MUST out-wait the patched idevicebackup2 receive timeout. If it does not,
// a receive blocked across a Wi-Fi flap — silent for up to toolReceiveTimeout — is SIGKILLed by the
// sampler before the tool can recover, undoing patch 0001. Fails loudly if anyone lowers the
// backstop below the tool's patience.
func TestLivenessBackstopExceedsToolTimeout(t *testing.T) {
	if DefaultConfig().LivenessTimeout <= toolReceiveTimeout {
		t.Fatalf("LivenessTimeout (%s) must be > toolReceiveTimeout (%s): a sampler kill inside the "+
			"tool's own receive patience would cut a backup the tool was about to complete",
			DefaultConfig().LivenessTimeout, toolReceiveTimeout)
	}
}

// TestSamplerRidesOutLegitimatePause is story 5: a legitimate multi-minute app_limited pause (iOS
// doing its own file-prep, (ct)) must NOT be killed. The sampler stages the silence honestly
// (silent_but_connected → suspected_stall) but never trips the kill inside the tool's patience, and
// recovers to active the moment the tree churns again.
func TestSamplerRidesOutLegitimatePause(t *testing.T) {
	cfg := DefaultConfig() // LivenessTimeout 18m
	start := time.Unix(1_700_000_000, 0).UTC()
	smp := newSampler(cfg, t.TempDir(), nil, start)

	// First sign of life ends the startup grace.
	if lv, kill, _ := smp.sample(start, false, true); lv != LivenessActive || kill {
		t.Fatalf("first active sample = (%s, kill=%v), want (active, false)", lv, kill)
	}

	at := func(d time.Duration, out bool) (string, bool) {
		lv, kill, _ := smp.sample(start.Add(d), false, out)
		return lv, kill
	}

	// A 14-minute silence: staged, never killed (14m < 18m backstop).
	cases := []struct {
		after    time.Duration
		wantLive string
	}{
		{2 * time.Minute, LivenessActive},          // < 18m/6 = 3m
		{3 * time.Minute, LivenessSilentConnected}, // >= 3m
		{9 * time.Minute, LivenessSuspectedStall},  // >= 18m/2 = 9m
		{14 * time.Minute, LivenessSuspectedStall}, // still < 18m: no kill
	}
	for _, c := range cases {
		lv, kill := at(c.after, false)
		if lv != c.wantLive {
			t.Errorf("at %s idle: liveness = %s, want %s", c.after, lv, c.wantLive)
		}
		if kill {
			t.Fatalf("at %s idle: killed a legitimate pause (backstop is %s)", c.after, cfg.LivenessTimeout)
		}
	}

	// The phone resumes: activity arrives → back to active, and the idle clock resets so a short
	// silence right after is not still "stalled".
	if lv, kill := at(14*time.Minute, true); lv != LivenessActive || kill {
		t.Fatalf("on resume = (%s, kill=%v), want (active, false)", lv, kill)
	}
	if lv, kill := at(16*time.Minute, false); lv != LivenessActive || kill {
		t.Fatalf("2m after resume = (%s, kill=%v), want (active, false) — clock did not reset", lv, kill)
	}
}

// TestSamplerClassifiesDeadLink is story 6: a genuinely dead link (the tool loops -5 forever and
// never exits, so the sampler is the sole authority) is classified honestly and eventually — the
// kill fires at the backstop, becoming connection_lost with the dirty working/ kept for resume.
func TestSamplerClassifiesDeadLink(t *testing.T) {
	cfg := DefaultConfig()
	start := time.Unix(1_700_000_000, 0).UTC()
	smp := newSampler(cfg, t.TempDir(), nil, start)
	smp.sample(start, false, true) // end startup grace

	// Just before the backstop: staged, not killed.
	if _, kill, _ := smp.sample(start.Add(cfg.LivenessTimeout-time.Second), false, false); kill {
		t.Fatalf("killed 1s before the %s backstop", cfg.LivenessTimeout)
	}
	// At the backstop: kill.
	lv, kill, _ := smp.sample(start.Add(cfg.LivenessTimeout), false, false)
	if !kill {
		t.Fatalf("did not kill at the %s backstop on a dead link", cfg.LivenessTimeout)
	}
	if lv != LivenessSuspectedStall {
		t.Errorf("liveness at kill = %s, want suspected_stall", lv)
	}
}

// TestSamplerPauseFreezesTheClock guards that a waiting_for_passcode pause never accrues idle: the
// user may take minutes to enter the passcode, and that is not a stall.
func TestSamplerPauseFreezesTheClock(t *testing.T) {
	cfg := DefaultConfig()
	start := time.Unix(1_700_000_000, 0).UTC()
	smp := newSampler(cfg, t.TempDir(), nil, start)
	smp.sample(start, false, true)

	// 20 minutes paused (> the backstop) must not kill — the clock is frozen while paused.
	if lv, kill, _ := smp.sample(start.Add(20*time.Minute), true /*paused*/, false); kill || lv != LivenessActive {
		t.Fatalf("paused sample = (%s, kill=%v), want (active, false) — passcode wait must freeze the clock", lv, kill)
	}
}
