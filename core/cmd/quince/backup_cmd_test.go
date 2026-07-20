package main

import (
	"testing"

	"github.com/novkostya/quince/core/internal/backup"
)

// TestParseBackupArgs covers flag placement around the single positional udid. The regression it
// guards: Go's flag package stops at the first non-flag token, so a flag placed AFTER the udid
// (`backup <udid> --transport usb`) was dropped and the command usage-errored (qn.4a finding (ii)).
func TestParseBackupArgs(t *testing.T) {
	const udid = "00008030-000A1B2C3D4E5F60" // synthetic — never a real device id

	cases := []struct {
		name          string
		args          []string
		wantUDID      string
		wantTransport string
		wantErr       bool
	}{
		{"flag after positional", []string{udid, "--transport", "usb"}, udid, "usb", false},
		{"flag after positional (equals)", []string{udid, "--transport=wifi"}, udid, "wifi", false},
		{"flag before positional", []string{"--transport", "wifi", udid}, udid, "wifi", false},
		{"no flag defaults to auto", []string{udid}, udid, backup.TransportAuto, false},
		{"missing udid", []string{"--transport", "usb"}, "", "", true},
		{"too many positionals", []string{udid, "extra"}, "", "", true},
		{"unknown flag", []string{udid, "--nope"}, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotUDID, gotTransport, err := parseBackupArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("args %v: want error, got udid=%q transport=%q", tc.args, gotUDID, gotTransport)
				}
				return
			}
			if err != nil {
				t.Fatalf("args %v: unexpected error: %v", tc.args, err)
			}
			if gotUDID != tc.wantUDID {
				t.Errorf("args %v: udid = %q, want %q", tc.args, gotUDID, tc.wantUDID)
			}
			if gotTransport != tc.wantTransport {
				t.Errorf("args %v: transport = %q, want %q", tc.args, gotTransport, tc.wantTransport)
			}
		})
	}
}
