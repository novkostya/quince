// Package preunlock reads what an iOS backup yields WITHOUT the backup password.
//
// It exists because qn.9 D1 rules the tier by the FORMAT rather than by a policy — Operator,
// 2026-08-22: "what a session sees without presenting the backup password — whatever is
// possible technically". Three plists are readable with no password, and this package is the
// whole of that read.
//
// WHY QUINCE PARSES Manifest.plist ITSELF RATHER THAN CALLING THE LIBRARY'S DeviceInfo.
// `iosbackup.Open` refuses an unencrypted backup with ErrNotEncrypted, so there is no
// *Backup to call DeviceInfo on for one — the same structural reason ReadStatus and
// ReadDeviceExtras are package-level functions on a directory rather than methods
// (ios-backup-crypt#16). Routing the Lockdown fields through the library would make this
// tier work on encrypted versions and return nothing on unencrypted ones.
//
// THAT IS NOT A HYPOTHETICAL SHAPE. Measured on the Operator's iPad (spec D1): the head
// reads IsEncrypted=false while twelve older snapshots read true. So the asymmetry would
// land on the NEWEST version of the device the rung gate uses, and story 9 requires the tier
// to look the same across a device whose history holds both. Reading the plist here makes
// that true by construction rather than by luck.
//
// It is a small duplication of a parse the library also does, and it is the cheaper half of
// the trade: the alternative is an upstream API plus a release tag to reach data that is
// four lines of plist decoding, on a file quince already reads for the unencrypted path.
package preunlock

import (
	"fmt"
	"os"
	"path/filepath"

	iosbackup "github.com/novkostya/ios-backup-crypt"
	"howett.net/plist"
)

// Lockdown is Manifest.plist's Lockdown dict — the device as it was when the backup ran.
//
// ALL SEVEN FIELDS, where quince previously read two. They cost no extra I/O: the file is
// read and parsed either way, and the other five were being discarded at the point of parse.
type Lockdown struct {
	DeviceName     string
	ProductVersion string // iOS version
	DeviceClass    string // "iPhone", "iPad", …
	ProductType    string // model identifier, e.g. the raw "iPadN,M" — never a marketing name
	BuildVersion   string
	SerialNumber   string
	UniqueDeviceID string
}

// Tier is everything one version discloses before it is unlocked.
//
// EACH SOURCE REPORTS ITS OWN PRESENCE, because a backup may legitimately lack Status.plist
// or Info.plist and "absent" is a different fact from "read it, and the fields were empty".
// That is the library's own discipline for these two files and this type keeps it rather
// than flattening the two into one zero value.
type Tier struct {
	// ManifestPresent is false when the backup has no Manifest.plist at all. Lockdown and
	// Encrypted are then zero and mean nothing.
	ManifestPresent bool
	Lockdown        Lockdown

	// Encrypted is read from THIS VERSION's Manifest.plist, never inherited from the device.
	// One version's encryption state does not generalise to its device (spec D1).
	Encrypted bool

	Status iosbackup.Status
	Extras iosbackup.DeviceExtras
}

// manifestPlist is the subset of Manifest.plist this tier reads. The keybag and the wrapped
// manifest key are deliberately absent: nothing here decrypts anything.
type manifestPlist struct {
	IsEncrypted bool `plist:"IsEncrypted"`
	Lockdown    struct {
		DeviceName     string `plist:"DeviceName"`
		ProductVersion string `plist:"ProductVersion"`
		DeviceClass    string `plist:"DeviceClass"`
		ProductType    string `plist:"ProductType"`
		BuildVersion   string `plist:"BuildVersion"`
		SerialNumber   string `plist:"SerialNumber"`
		UniqueDeviceID string `plist:"UniqueDeviceID"`
	} `plist:"Lockdown"`
}

// Read reads the three no-password plists in dir.
//
// A MISSING FILE IS NOT AN ERROR — it reports its own absence, per Tier. A file that exists
// and will not parse IS an error, because that is a broken backup rather than an old one.
// Same split the library draws for Status.plist and Info.plist, applied to the third file so
// all three behave alike.
//
// IT NEVER TAKES A PASSWORD AND MUST NOT GROW ONE. The tier is defined by what is readable
// without one; a parameter here would be a parameter nothing could honestly use.
func Read(dir string) (Tier, error) {
	var t Tier

	mp, present, err := readManifest(dir)
	if err != nil {
		return Tier{}, err
	}
	if present {
		t.ManifestPresent = true
		t.Encrypted = mp.IsEncrypted
		t.Lockdown = Lockdown{
			DeviceName:     mp.Lockdown.DeviceName,
			ProductVersion: mp.Lockdown.ProductVersion,
			DeviceClass:    mp.Lockdown.DeviceClass,
			ProductType:    mp.Lockdown.ProductType,
			BuildVersion:   mp.Lockdown.BuildVersion,
			SerialNumber:   mp.Lockdown.SerialNumber,
			UniqueDeviceID: mp.Lockdown.UniqueDeviceID,
		}
	}

	if t.Status, err = iosbackup.ReadStatus(dir); err != nil {
		return Tier{}, fmt.Errorf("preunlock: Status.plist: %w", err)
	}
	// THE EXPENSIVE ONE — 10 ms on a tablet and 99 ms on a phone, scaling with the app
	// count, because Info.plist is XML and carries per-app metadata. It is read once per
	// version and memoised by the caller (spec D2c), never per request.
	if t.Extras, err = iosbackup.ReadDeviceExtras(dir); err != nil {
		return Tier{}, fmt.Errorf("preunlock: Info.plist: %w", err)
	}
	return t, nil
}

func readManifest(dir string) (manifestPlist, bool, error) {
	var mp manifestPlist
	b, err := os.ReadFile(filepath.Join(dir, "Manifest.plist"))
	if os.IsNotExist(err) {
		return mp, false, nil
	}
	if err != nil {
		return mp, false, fmt.Errorf("preunlock: Manifest.plist: %w", err)
	}
	if _, err := plist.Unmarshal(b, &mp); err != nil {
		return mp, false, fmt.Errorf("preunlock: Manifest.plist does not parse: %w", err)
	}
	return mp, true, nil
}
