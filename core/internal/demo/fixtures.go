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

// seed populates the deterministic fixture world. Called once by NewProvider.
func (p *Provider) seed() {
	phone := wire.Device{
		UDID:             udidPhone,
		Name:             "family-iphone",
		Model:            "iPhone17,2",
		IOSVersion:       "26.0.1",
		Transports:       wire.Transports{USB: strptr(tPhoneUSB), WiFi: strptr(tPhoneWiFi)},
		Paired:           "yes",
		BackupEncryption: "on",
		LastSeen:         tPhoneSeen,
		LastBackup:       &wire.LastBackup{At: tBackupA, JobID: strptr(jobID), Status: "succeeded"},
	}
	pad := wire.Device{
		UDID:             udidPad,
		Name:             "studio-ipad",
		Model:            "iPad13,4",
		IOSVersion:       "18.5",
		Transports:       wire.Transports{WiFi: strptr(tPadWiFi)}, // Wi-Fi only
		Paired:           "yes",
		BackupEncryption: "off", // exercises the unencrypted-device warning path
		LastSeen:         tPadSeen,
		LastBackup:       nil, // never backed up
	}
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
		ZFSSnapshot:         strptr("tank/backups/iphone-backup/" + udidPhone + "@quince-" + jobID + "-2026-07-18"),
		BrowseRoot:          "/backups/" + udidPhone + "/.zfs/snapshot/quince-" + jobID + "/working",
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
		ZFSSnapshot:         strptr("tank/backups/iphone-backup/" + udidPhone + "@quince-adopted-2026-07-01"),
		BrowseRoot:          "/backups/" + udidPhone + "/.zfs/snapshot/quince-adopted/working",
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
