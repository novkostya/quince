package store

import "errors"

// Scope is a credential's confinement, and ITS ZERO VALUE IS INVALID ON PURPOSE.
//
// WHY A TYPE RATHER THAN A `*string` FIELD. 0015_passkey_scope.sql accepts `NULL means admin` for
// stored rows on the grounds that the layer above refuses to guess for new ones. A struct field
// cannot carry that refusal: `store.Passkey{CredentialID: …}` compiles with the field omitted and
// would write an ADMIN credential, which is the guess the acceptance says is impossible — and the
// silent lockout that migration's last paragraph describes, reached from the write path instead of
// a predicate.
//
// So scope is a POSITIONAL ARGUMENT to `InsertPasskey` of a type whose zero value means nothing.
// Forgetting it is a compile error, which is the only refusal that survives a caller written months
// from now by somebody who has not read this file. Passing a zero `Scope` is refused at runtime,
// which covers the one way a compile error cannot help.
//
// THE ADMIN CASE MUST BE SPELLED OUT. `AdminScope()` exists so that "this credential administers
// everything" is a sentence somebody wrote, not a field somebody omitted.
type Scope struct {
	// set distinguishes a stated scope from the zero value. Unexported so no caller outside this
	// package can construct a Scope except through the two constructors below.
	set bool
	// udid is empty for an admin scope and names the device for a scoped one.
	udid string
}

// ErrScopeUnset is returned when a credential is written without a stated scope.
var ErrScopeUnset = errors.New("store: credential scope not stated — use AdminScope() or DeviceScope()")

// ErrScopeConflict is returned when a caller sets Passkey.ScopeUDID instead of passing a Scope.
//
// REFUSED RATHER THAN IGNORED. `Passkey.ScopeUDID` exists for READS, so a caller that populates it
// and expects it to be written has made a reasonable mistake — and silently ignoring the field
// would write an admin credential where a scoped one was intended, which is this rung's worst
// failure wearing the shape of a no-op.
var ErrScopeConflict = errors.New("store: set the scope via the Scope argument, not Passkey.ScopeUDID")

// AdminScope is a credential that administers quince. Stated, never defaulted.
func AdminScope() Scope { return Scope{set: true} }

// DeviceScope confines a credential to one device.
func DeviceScope(udid string) Scope { return Scope{set: true, udid: udid} }

// IsAdmin reports whether this scope administers quince.
func (s Scope) IsAdmin() bool { return s.set && s.udid == "" }

// value returns what belongs in `scope_udid`: nil for admin, the udid otherwise.
func (s Scope) value() *string {
	if s.udid == "" {
		return nil
	}
	return &s.udid
}

// UDID returns the device a scoped credential is confined to, empty for an admin scope.
func (s Scope) UDID() string { return s.udid }
