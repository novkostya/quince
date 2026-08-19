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
	// notifications_enabled IS NOT AN ENUM AND CANNOT BE NORMALISED THE SAME WAY: it is a bool, so
	// its zero value is a legitimate answer rather than an unset one. The default is applied
	// unconditionally, matching deviceShellLocked, and a demo device that wants to be MUTED sets it
	// AFTER this call — `d := demoDevice(...); d.NotificationsEnabled = false` — where the intent is
	// visible, instead of inside the literal, where this line would silently undo it.
	d.NotificationsEnabled = true
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
	}
	p.verOrder = []string{verZFS, verHL, verADOP}

	// THE THREE ABOVE LIVE ON `internal`, and every version below exists so the storage counts can
	// be DERIVED rather than written down three times (quince#624).
	//
	// Before this, no demo version carried a `storage_id` at all. Anything computed per
	// (device, storage) therefore found nothing and rendered the empty case, while the storage
	// cards simultaneously asserted hardcoded totals — a storage page reading "0 backups here" for
	// every device under a header claiming 14. Three surfaces, three answers, one fixture set.
	//
	// It also left qn.6d's G3 unfalsifiable: that gate asserts a storage page's lists are SCOPED to
	// their storage, and with every version unattributed each device read zero whether the filter
	// worked or not. A gate that cannot fail for the reason it exists is not yet a gate.
	p.versions[verZFS] = withStorage(p.versions[verZFS], demoStorageInternal)
	p.versions[verHL] = withStorage(p.versions[verHL], demoStorageInternal)
	p.versions[verADOP] = withStorage(p.versions[verADOP], demoStorageInternal)

	// 11 more on `internal` and 3 on `shuttle`, so the seeded world derives to internal = 14
	// backups / 1 device and shuttle = 3 / 1 — the figures the fixture used to assert.
	//
	// ALL ON ONE DEVICE, BECAUSE studio-ipad HAS NEVER BEEN BACKED UP and stays that way (Operator,
	// 2026-08-04). That is a state the demo exists to show — the empty card, and the unencrypted
	// warning beside it — and inventing a history for it to satisfy a fabricated `device_count: 2`
	// would trade a real demo state for a number nobody measured. The count now reads 1 because 1
	// is true.
	p.seedStorageHistory(udidPhone, demoStorageInternal, "reflink", 11, 26)
	p.seedStorageHistory(udidPhone, demoStorageShuttle, "hardlink", 3, 12)
}

// withStorage attributes an existing fixture version to a storage.
func withStorage(v wire.Version, storageID string) wire.Version {
	v.StorageID = strptr(storageID)
	return v
}

// seedStorageHistory appends n plausible committed backups for one device on one storage.
//
// Deterministic ids and timestamps, because the httpapi golden fixtures render this list and a
// generated id would churn them on every run.
//
// `backend` is the VERSION's backend — how those bytes were written — which is a different field
// from the storage's. They agree here, but they are ALLOWED to differ: `shuttle` reports backend
// "unknown" because it is unreachable and its marker cannot be read right now, while the versions
// the database remembers on it were written as hardlink. That is the same asymmetry gap A ruled for
// capacity-versus-counts — the disk is out, the database is not.
func (p *Provider) seedStorageHistory(udid, storageID, backend string, n, startDay int) {
	for i := 0; i < n; i++ {
		vid := histID("V", storageID, i)
		at := histTimestamp(startDay - i) // walking backwards in time
		p.versions[vid] = wire.Version{
			ID:      vid,
			UDID:    udid,
			Backend: backend,
			// No snapshot: reflink and hardlink store a version as a directory, not a zfs
			// snapshot, and a `zfs_snapshot` here would contradict the backend beside it.
			ZFSSnapshot:         nil,
			BrowseRoot:          "/backups/" + udid + "/versions/" + at,
			CreatedAt:           at,
			JobID:               strptr(histID("J", storageID, i)),
			Kind:                "incremental",
			Encrypted:           true,
			IsLatest:            false, // the newest for this device is verZFS, seeded above
			StructureVerifiedAt: strptr(at),
			ContentVerifiedAt:   nil,
			LogicalBytes:        41_000_000_000 + int64(i)*17_000_000,
			StorageID:           strptr(storageID),
		}
		p.verOrder = append(p.verOrder, vid)
	}
}

// histID mints a stable 26-character ULID-shaped id. The storage is folded in so the two runs
// cannot collide: "01JCZQ8XH" + kind + storage tag + 15 digits.
func histID(kind, storageID string, i int) string {
	tag := "I"
	if storageID == demoStorageShuttle {
		tag = "S"
	}
	return "01JCZQ8XH" + kind + tag + padDigits(i, 15)
}

func padDigits(i, width int) string {
	out := make([]byte, width)
	for k := width - 1; k >= 0; k-- {
		out[k] = byte('0' + i%10)
		i /= 10
	}
	return string(out)
}

// histTimestamp renders a fixed instant on a day of 2026-06, so the generated history is stable
// across runs and orders the way a real history would.
func histTimestamp(day int) string {
	return "2026-06-" + padDigits(day, 2) + "T04:12:00Z"
}
