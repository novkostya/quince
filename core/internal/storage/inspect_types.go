package storage

// InspectOutcome is the verdict on a candidate storage path: what the form does next.
//
// The three branches the spec names — adopt, recommend, refuse — are five outcomes here, because
// collapsing the refusals loses the only thing that makes a refusal actionable. "That path does not
// exist" and "that path is read-only" have different remedies, and a form that says "unusable" to
// both has told the user nothing they could not see.
type InspectOutcome string

const (
	// InspectAdopt — a valid storage marker is present. The backend is decided and immutable; the
	// form shows what this storage IS and offers no selector.
	InspectAdopt InspectOutcome = "adopt"
	// InspectNew — reachable, writable, no marker. The form recommends and lets the user override.
	InspectNew InspectOutcome = "new"
	// InspectMissing — nothing at that path. NOT created; "create it" is an explicit second click.
	InspectMissing InspectOutcome = "missing"
	// InspectNotDir — something is there and it is not a directory.
	InspectNotDir InspectOutcome = "not_a_directory"
	// InspectUnwritable — the directory exists and quince cannot write in it.
	InspectUnwritable InspectOutcome = "unwritable"
	// InspectCorruptMarker — a marker is present and failed its checksum.
	InspectCorruptMarker InspectOutcome = "corrupt_marker"
	// InspectInvalidPath — empty, or not absolute. The same rule config/validate.go enforces, so
	// the form's refusal and the config's refusal cannot disagree.
	InspectInvalidPath InspectOutcome = "invalid_path"
	// InspectUnreadable — stat failed for a reason that is neither absence nor a wrong type.
	InspectUnreadable InspectOutcome = "unreadable"
)

// OK reports whether this outcome can proceed to a declaration — adopt or new.
func (o InspectOutcome) OK() bool { return o == InspectAdopt || o == InspectNew }

// ZFSSignal is what quince could observe about zfs, in three tiers.
type ZFSSignal string

const (
	// ZFSPath — statfs says THIS PATH is on ZFS. Tier 1: recommend zfs.
	ZFSPath ZFSSignal = "path"
	// ZFSHost — the path is not ZFS, but the running kernel knows the zfs filesystem. Tier 2: a
	// hint worth showing, because snapshot versioning with no copy at commit is the largest
	// difference between backends and nothing else in the product would ever mention it.
	ZFSHost ZFSSignal = "host"
	// ZFSNone — no signal.
	//
	// IT MUST NEVER BE RENDERED AS "ZFS NOT SUPPORTED", and this is a hard constraint rather than
	// a wording preference. In hook mode the container holds no zfs userland at all and zfs works
	// perfectly through the host helper, so a negative reading is a GUARANTEED FALSE NEGATIVE for
	// an entire supported topology — in fact for the supported containerised one. "Not detected",
	// or silence. Never a capability claim.
	//
	// This is the no-silent-caps rule pointed the other way: do not assert an absence you cannot
	// observe.
	ZFSNone ZFSSignal = "none"
)

// Report is everything Inspect learned. Facts and verdict are separate on purpose: Marker,
// FreeBytes and ZFS are filled in whenever they could be read, whatever Outcome says, so a caller
// can tell a user "this IS storage X, and the path is read-only" rather than only the second half.
type Report struct {
	// Path is what the caller passed, verbatim; CleanPath is filepath.Clean of it. Both are kept
	// because the user should see their own typing back, and quince must act on the cleaned form.
	Path      string
	CleanPath string

	Outcome InspectOutcome
	// Reason is a plain-language sentence and ALWAYS NAMES THE PATH IT IS ABOUT (quince#514).
	// With storage plural, "the probe passed" without a path named a different, healthy disk.
	Reason string

	// Backend is the recommendation (new) or the marker's recorded value (adopt); empty otherwise.
	// BackendReason is the sentence explaining it, likewise path-naming.
	Backend       string
	BackendReason string

	// Marker is set whenever one was present and passed its checksum, on any outcome.
	Marker *StorageMarker

	// NonEmpty reports data already at the path that is not quince's own. Reported, never a
	// refusal — see dirHasEntries.
	NonEmpty bool

	ZFS ZFSSignal

	// FreeBytes/TotalBytes are the FILESYSTEM's, never the storage's — two storages that are two
	// directories on one disk return identical figures, exactly as FilesystemSpace documents.
	FreeBytes  uint64
	TotalBytes uint64
}

// InspectOptions injects the two host readings so they can be exercised without a ZFS filesystem
// or a particular kernel. Both are nil in production and every field is optional.
//
// They are seams rather than configuration: gate G4 has to prove that a ZFS f_type yields a zfs
// recommendation WITH NO zfs BINARY PRESENT, and no CI box can stage that for real. Injecting the
// syscall is how that claim gets a test at all — and the alternative, asserting it only on a box
// that happens to run ZFS, is a gate that silently does not run.
type InspectOptions struct {
	// FSType reports statfs f_type for a path.
	FSType func(string) (int64, error)
	// KernelHasZFS reports whether the running kernel knows the zfs filesystem.
	KernelHasZFS func() bool
}

func (o InspectOptions) withDefaults() InspectOptions {
	if o.FSType == nil {
		o.FSType = statfsType
	}
	if o.KernelHasZFS == nil {
		o.KernelHasZFS = kernelHasZFS
	}
	return o
}
