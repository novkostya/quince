package deviceops

import (
	"context"
	"testing"
)

func TestValidatePaired(t *testing.T) {
	paired, err := fakeTools("DEVICEOPS_FAKE=paired").Validate(context.Background(), fakeUDID, TransportUSB)
	if err != nil || !paired {
		t.Fatalf("validate paired = %v, err %v (want true, nil)", paired, err)
	}
}

func TestValidateUnpaired(t *testing.T) {
	paired, err := fakeTools("DEVICEOPS_FAKE=unpaired").Validate(context.Background(), fakeUDID, TransportUSB)
	if err != nil || paired {
		t.Fatalf("validate unpaired = %v, err %v (want false, nil)", paired, err)
	}
}

func TestValidateLockedNotConfirmed(t *testing.T) {
	// "passcode is set" is returned for any locked device regardless of pairing, so it is NOT a
	// confirmation — Validate reports false honestly (lab finding 2026-07-20).
	paired, err := fakeTools("DEVICEOPS_FAKE=locked").Validate(context.Background(), fakeUDID, TransportUSB)
	if err != nil || paired {
		t.Fatalf("validate locked = %v, err %v (want false, nil)", paired, err)
	}
}

func TestInfoLockedUsesSimpleReadNoAutoPair(t *testing.T) {
	// A locked device: paired is unknown, encryption undetermined, and the read is the simple
	// (-s) one — NEVER the auto-pairing full read (guards against enrichment surfacing an
	// unexpected Trust prompt; lab finding 2026-07-20). The fake omits DeviceName for -s, so an
	// empty name here proves the simple path was taken.
	id, err := fakeTools("DEVICEOPS_FAKE=locked").Info(context.Background(), fakeUDID, TransportUSB)
	if err != nil {
		t.Fatalf("Info err = %v", err)
	}
	if id.Paired != "unknown" {
		t.Fatalf("paired = %q (want unknown for a locked device)", id.Paired)
	}
	if id.BackupEncryption != "" {
		t.Fatalf("encryption must stay undetermined while locked, got %q", id.BackupEncryption)
	}
	if id.Name != "" {
		t.Fatalf("locked device must use the simple read (no DeviceName), got %q", id.Name)
	}
}

func TestValidateRejectsBadUDID(t *testing.T) {
	if _, err := fakeTools().Validate(context.Background(), "bad udid; rm -rf", TransportUSB); err == nil {
		t.Fatal("expected a bad-udid rejection before any subprocess")
	}
}

func TestInfoPairedFull(t *testing.T) {
	id, err := fakeTools("DEVICEOPS_FAKE=paired").Info(context.Background(), fakeUDID, TransportUSB)
	if err != nil {
		t.Fatalf("Info err = %v", err)
	}
	if id.Name != "synthetic-iphone" || id.Model != "iPhone17,2" || id.IOSVersion != "26.0.1" {
		t.Fatalf("identity = %+v", id)
	}
	if id.Paired != "yes" || id.BackupEncryption != "on" {
		t.Fatalf("paired/enc = %q/%q (want yes/on)", id.Paired, id.BackupEncryption)
	}
}

func TestInfoUnpairedSimple(t *testing.T) {
	// Unpaired: validate says no → simple read (model/ios, no name); encryption stays unknown
	// (never guessed, and the full read that would auto-pair is not run).
	id, err := fakeTools("DEVICEOPS_FAKE=unpaired").Info(context.Background(), fakeUDID, TransportUSB)
	if err != nil {
		t.Fatalf("Info err = %v", err)
	}
	if id.Paired != "no" {
		t.Fatalf("paired = %q (want no)", id.Paired)
	}
	if id.Model != "iPhone17,2" || id.IOSVersion != "26.0.1" {
		t.Fatalf("simple read lost model/ios: %+v", id)
	}
	if id.Name != "" {
		t.Fatalf("simple read should not carry DeviceName pre-pairing: %+v", id)
	}
	if id.BackupEncryption != "" {
		t.Fatalf("encryption must stay undetermined for an unpaired device, got %q", id.BackupEncryption)
	}
}

func TestInfoEncryptionOff(t *testing.T) {
	// scenario "enc_off": validate → paired (default), WillEncrypt → false → "off".
	id, err := fakeTools("DEVICEOPS_FAKE=enc_off").Info(context.Background(), fakeUDID, TransportUSB)
	if err != nil {
		t.Fatalf("Info err = %v", err)
	}
	if id.Paired != "yes" {
		t.Fatalf("paired = %q (want yes)", id.Paired)
	}
	if id.BackupEncryption != "off" {
		t.Fatalf("encryption = %q (want off)", id.BackupEncryption)
	}
}
