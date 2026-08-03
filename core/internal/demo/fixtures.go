package demo

import "github.com/novkostya/quince/core/internal/wire"

// Deterministic fixture identifiers and timestamps — fixed so golden contract tests are
// stable. UDIDs are invented, not real devices (privacy: fixtures never carry real data).
const (
	udidPhone = "00008140-000A1B2C3D4E001E"
	udidPad   = "00008101-0011223344550022"

	jobID    = "01JCZQ8XN0R7T4M2K9V3B6H8P1"
	intentID = "01JCZQ8XN0R7T4M2K9V3B6H8P1" // == jobID: first attempt

	verZFS  = "01JCZQ8XP0S8N5W2K7V4C9J2M3"
	verHL   = "01JCZQ8XR2T9P6X3M8W5D0K4N7"
	verADOP = "01JCZQ8XT4V2Q7Y4N9X6E1M5P8"
)

const (
	tPhoneUSB   = "2026-07-18T09:15:00Z"
	tPhoneWiFi  = "2026-07-18T09:10:00Z"
	tPhoneSeen  = "2026-07-18T09:15:00Z"
	tPadWiFi    = "2026-07-17T20:00:00Z"
	tPadSeen    = "2026-07-17T20:00:00Z"
	tBackupA    = "2026-07-18T02:30:11Z"
	tVerZFS     = "2026-07-18T02:30:11Z"
	tVerHL      = "2026-07-15T03:04:00Z"
	tVerAdopted = "2026-07-01T01:00:00Z"
	tJobStart   = "2026-07-18T09:14:02Z"
)

// demoDevice is the demo provider's counterpart to the registry's deviceShellLocked: the three
// contract ENUM fields land on the literal "unknown" when a construction site leaves them empty,
// never on Go's "" zero value — which is not in the contract enum at all (contracts §2 types all
// three as on|off|unknown, paired as yes|no|unknown).
//
// It exists because forgetting one is the easy mistake and it fails SILENTLY: four of five demo
// devices shipped `wifi_sync: ""`, the UI's `!== "unknown"` guards let it through, and a badge
// rendered with no value while WifiSyncControl treated the device as `off` and offered to turn ON
// a flag quince had never read (quince#361). The registry has defended this invariant since qn.3
// and the demo provider simply did not go through it — so this is the same guard, at the second
// door, rather than a new rule.
//
// Defaulting to "unknown" is the honest failure: it means "quince has not read this", which is
// exactly true of a field nobody set, and the UI already knows to show nothing rather than guess.
func demoDevice(d wire.Device) wire.Device {
	if d.Paired == "" {
		d.Paired = "unknown"
	}
	if d.BackupEncryption == "" {
		d.BackupEncryption = "unknown"
	}
	if d.WifiSync == "" {
		d.WifiSync = "unknown"
	}
	return d
}

// padDevice builds studio-ipad. ONE builder, because it is constructed twice — once in the static
// seed and once per deviceChurn re-attach — and the two copies DRIFTED: the churn copy omitted
// WifiSync, so the seeded "off" survived only until the first churn tick reattached the pad with
// "" (quince#361). Two constructions of one device is the defect; a shared builder is the fix.
func padDevice(wifiSeen, lastSeen string) wire.Device {
	return demoDevice(wire.Device{
		UDID:             udidPad,
		Name:             "studio-ipad",
		Model:            "iPad13,4",
		IOSVersion:       "18.5",
		Transports:       wire.Transports{WiFi: strptr(wifiSeen)}, // Wi-Fi only
		Paired:           "yes",
		BackupEncryption: "off", // exercises the unencrypted-device warning path
		WifiSync:         "off", // the case qn.7 exists for: paired, but Wi-Fi sync never ticked
		LastSeen:         lastSeen,
		LastBackup:       nil, // never backed up
	})
}

// seed populates the deterministic fixture world. Called once by NewProvider.
func (p *Provider) seed() {
	phone := demoDevice(wire.Device{
		UDID:             udidPhone,
		Name:             "family-iphone",
		Model:            "iPhone17,2",
		IOSVersion:       "26.0.1",
		Transports:       wire.Transports{USB: strptr(tPhoneUSB), WiFi: strptr(tPhoneWiFi)},
		Paired:           "yes",
		BackupEncryption: "on",
		WifiSync:         "on", // set up already — the state a working Wi-Fi device is in
		LastSeen:         tPhoneSeen,
		LastBackup:       &wire.LastBackup{At: tBackupA, JobID: strptr(jobID), Status: "succeeded"},
	})
	pad := padDevice(tPadWiFi, tPadSeen)
	p.devices[phone.UDID] = phone
	p.devices[pad.UDID] = pad
	p.order = []string{phone.UDID, pad.UDID}

	// A scripted job, seeded mid-backup for a lively initial render (the timeline in
	// script.go re-drives it end to end).
	p.jobs[jobID] = wire.Job{
		ID:        jobID,
		UDID:      udidPhone,
		Kind:      "backup",
		Transport: "wifi",
		State:     "backing_up",
		Progress: wire.JobProgress{
			Phase:         "receiving",
			Percent:       f64ptr(63.0),
			BytesDone:     2_400_000_000,
			BytesTotal:    3_600_000_000,
			FilesReceived: 149,
			Liveness:      "active",
		},
		StartedAt:  tJobStart,
		FinishedAt: nil,
		Error:      nil,
		RetryOf:    nil,
		IntentID:   intentID,
		Attempt:    1,
		VersionID:  nil,
	}

	// Three versions across backends, one adopted (job_id: null).
	p.versions[verZFS] = wire.Version{
		ID:                  verZFS,
		UDID:                udidPhone,
		Backend:             "zfs",
		ZFSSnapshot:         strptr("tank/backups/iphone-backup/" + udidPhone + "@quince-2026-07-18T02-30-" + jobID),
		BrowseRoot:          "/backups/" + udidPhone + "/.zfs/snapshot/quince-2026-07-18T02-30-" + jobID + "/latest",
		CreatedAt:           tVerZFS,
		JobID:               strptr(jobID),
		Kind:                "full",
		Encrypted:           true,
		IsLatest:            true,
		StructureVerifiedAt: strptr(tVerZFS),
		ContentVerifiedAt:   strptr("2026-07-18T08:00:00Z"),
		LogicalBytes:        42_400_000_000,
		PhysicalBytes:       3_400_000_000,
	}
	p.versions[verHL] = wire.Version{
		ID:                  verHL,
		UDID:                udidPhone,
		Backend:             "hardlink",
		ZFSSnapshot:         nil,
		BrowseRoot:          "/backups/" + udidPhone + "/versions/2026-07-15T03-04-00Z",
		CreatedAt:           tVerHL,
		JobID:               strptr("01JCZ0000R7T4M2K9V3B6H8OLD"),
		Kind:                "incremental",
		Encrypted:           true,
		IsLatest:            false,
		StructureVerifiedAt: strptr(tVerHL),
		ContentVerifiedAt:   nil,
		LogicalBytes:        41_900_000_000,
		PhysicalBytes:       520_000_000,
	}
	p.versions[verADOP] = wire.Version{
		ID:                  verADOP,
		UDID:                udidPhone,
		Backend:             "zfs",
		ZFSSnapshot:         strptr("tank/backups/iphone-backup/" + udidPhone + "@quince-2026-07-01T00-00-adopted"),
		BrowseRoot:          "/backups/" + udidPhone + "/.zfs/snapshot/quince-2026-07-01T00-00-adopted/latest",
		CreatedAt:           tVerAdopted,
		JobID:               nil, // adopted: found on disk, no DB record
		Kind:                "unknown",
		Encrypted:           true,
		IsLatest:            false,
		StructureVerifiedAt: strptr(tVerAdopted),
		ContentVerifiedAt:   nil,
		LogicalBytes:        40_100_000_000,
		PhysicalBytes:       40_100_000_000,
	}
	p.verOrder = []string{verZFS, verHL, verADOP}
}
