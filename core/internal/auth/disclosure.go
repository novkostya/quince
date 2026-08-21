package auth

import "errors"

// Disclosure decides whether a registration ceremony tells the authenticator WHAT IS ALREADY
// REGISTERED at this rpId, and ITS ZERO VALUE IS INVALID ON PURPOSE.
//
// TWO CEREMONIES, TWO ANSWERS, AND THE DIFFERENCE IS WHO IS STANDING AT THE OTHER END (spec D4.1).
//
// The admin's own registration excludes what exists, so a second attempt on a phone that already
// holds a passkey is refused by the device with *you already have a passkey here* rather than
// silently minting a duplicate the user cannot tell apart in the list. That is right when the
// person registering is the person who owns every credential in that list.
//
// The ENROLMENT ceremony is reached PRE-AUTHENTICATION by definition — that is the whole of the QR
// — so an exclusion list would hand every admin credential id to whoever scanned it, spent or not.
// `existingCredentials` already names this concern for its own case: *"offering them as exclusions
// would tell the authenticator about registrations it has no business knowing exist on this
// origin."* An unauthenticated scanner has less business still.
//
// AND IT WOULD FORBID A STATE D2.2 MAKES SUPPORTABLE. With the exclusion list, an admin cannot enrol
// a scoped credential on their own phone at all — the authenticator refuses. That was right while a
// second credential was unselectable; D2.1 and D2.2 removed both halves of that problem, so refusing
// now costs a real want for nothing. The duplicate the list would have prevented is prevented
// instead by the enrolment secret being single-use.
//
// WHY A TYPE RATHER THAN A BOOL. `BeginPasskeyRegistration(st, cer, rpID, scope, false)` reads as
// nothing at the call site and is exactly as easy to write as `true`; a caller who guesses wrong in
// the permissive direction discloses the admin's credential ids to an anonymous scanner. So this is
// a positional argument of a type whose zero value means nothing — forgetting it is a compile
// error, and passing an unstated one is refused at runtime. The same reasoning, and the same shape,
// as `store.Scope` (0015_passkey_scope.sql), which this rung already relies on one layer down.
type Disclosure struct {
	// set distinguishes a stated policy from the zero value. Unexported so no caller outside this
	// package can construct a Disclosure except through the two constructors below.
	set bool
	// exclude is true when the ceremony sends the exclusion list.
	exclude bool
}

// ErrDisclosureUnset is returned when a registration ceremony is begun without a stated policy.
var ErrDisclosureUnset = errors.New(
	"auth: registration disclosure not stated — use ExcludeRegistered() or DiscloseNothing()")

// ExcludeRegistered sends every credential at this rpId as an exclusion list. The ADMIN's own
// registration path, unchanged.
func ExcludeRegistered() Disclosure { return Disclosure{set: true, exclude: true} }

// DiscloseNothing sends no exclusion list. The ENROLMENT ceremony, which is pre-authentication.
//
// Stated as a sentence somebody wrote rather than reached by omitting an argument — the whole point
// of the type. Read D4.1 before changing a call site to this.
func DiscloseNothing() Disclosure { return Disclosure{set: true} }

// excludes reports whether this ceremony sends the exclusion list.
func (d Disclosure) excludes() bool { return d.exclude }
