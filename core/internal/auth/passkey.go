package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/novkostya/quince/core/internal/store"
)

// The WebAuthn ceremony — qn.6k slice 3b, registration only. Assertion is 3c.
//
// THE `*webauthn.WebAuthn` HANDLE IS BUILT PER CEREMONY, NOT AT STARTUP, and that is the one place
// this file departs from every example in the library. Those construct it once, because a normal
// deployment has one domain. quince does not: the access path is a user choice across qn.6f's four
// tiers (spec D2), and one quince can legitimately answer on more than one name at once — the
// staging stand currently does, proxied and direct. A startup singleton would pin whichever name
// arrived first and then either refuse every ceremony on any other, or mint credentials against a
// name the user never visits. Both are silent failures of exactly the kind D2 exists to prevent.

const (
	settingPasskeyUserHandle = "passkey_user_handle"

	// rpDisplayName is what the authenticator shows in the save sheet. A constant, because quince
	// is one product with one admin; the DEVICE's name is the per-credential label and lives on the
	// row, not here.
	rpDisplayName = "quince"

	// challengeTTL bounds how long a begun ceremony may sit unfinished. Short, because the user is
	// looking at a Face ID sheet: anything longer is not a slow human, it is an abandoned ceremony
	// or a replay.
	challengeTTL = 2 * time.Minute
)

// ErrNoChallenge — finish arrived with no matching begun ceremony, or one that expired.
var ErrNoChallenge = errors.New("auth: no passkey ceremony in progress")

// ErrUnsupportedRPID — the address this request arrived on cannot be a relying party.
//
// An rpId must be a DOMAIN. A bare IP cannot be one, and no amount of certificate work rescues it —
// which is the protocol half of why quince#657's tier table rules out self-signed-at-an-IP and the
// plain-http opt-in. Refusing at registration is story 4: better a stated refusal than a credential
// that can never be used.
type ErrUnsupportedRPID struct{ RPID string }

func (e ErrUnsupportedRPID) Error() string {
	return fmt.Sprintf("passkeys need a domain name, and this quince was reached at %q. "+
		"Reach it by a hostname over https — a reverse proxy or Tailscale — and try again.", e.RPID)
}

// PasskeyCeremonies holds the in-flight challenges.
//
// IN MEMORY, DELIBERATELY, AND THE COST IS ONE RETRY. A challenge is single-use and lives for two
// minutes; persisting it would buy surviving a restart mid-ceremony, which is a case the user
// resolves by tapping the button again. What it would cost is a table whose rows are secrets with a
// lifetime shorter than the time between backups — and quince restarts are not rare enough to make
// that trade worth it.
type PasskeyCeremonies struct {
	mu  sync.Mutex
	now func() time.Time
	in  map[string]pendingCeremony
}

// ceremonyKind is WHAT a ceremony was begun for, and it exists because THREE endpoints produce keys
// into this one store — quince#930 review.
//
// `passkeys/register/begin` and `setup/passkey/begin` are the two guarded producers. The third is
// `passkeys/login/begin`, which is PRE-AUTH by exact path in all three allowlists and callable by
// anyone. `put`/`take` were keyed on an opaque string with no notion of purpose, so *"a key in hand
// is evidence that a proof was presented"* — the sentence qn.6n slice 5b rests rule 1 on — was
// short by that entry.
//
// NOTHING WAS EXPLOITABLE, AND THAT IS THE POINT OF FIXING IT. Measured against `go-webauthn`
// v0.17.4: a login session has a nil `UserID`, so `CreateCredential` refuses it on *"ID mismatch for
// User and Session"*, and a registration session has a non-empty one, so `login.go:254` refuses the
// reverse as *"not initiated as a client-side discoverable login"*. Both hold — but they are an
// UPSTREAM INVARIANT in a dependency, not a property of this package, and a version bump could
// change them with nothing here to notice. Tagging makes the stated property locally true.
type ceremonyKind string

const (
	ceremonyRegister ceremonyKind = "register"
	ceremonyAssert   ceremonyKind = "assert"
)

type pendingCeremony struct {
	session webauthn.SessionData
	rpID    string
	kind    ceremonyKind
	expires time.Time
}

// NewPasskeyCeremonies builds the in-flight challenge store.
func NewPasskeyCeremonies() *PasskeyCeremonies {
	return &PasskeyCeremonies{now: time.Now, in: map[string]pendingCeremony{}}
}

// put records a begun ceremony under a fresh opaque key and sweeps anything expired.
//
// The sweep is here rather than on a timer because this map is bounded by how often a human taps
// "add a passkey": a goroutine to collect at most a handful of two-minute entries would be more
// machinery than the thing it manages.
func (p *PasskeyCeremonies) put(session *webauthn.SessionData, rpID string, kind ceremonyKind) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := base64.RawURLEncoding.EncodeToString(raw)

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for k, v := range p.in {
		if now.After(v.expires) {
			delete(p.in, k)
		}
	}
	p.in[key] = pendingCeremony{session: *session, rpID: rpID, kind: kind, expires: now.Add(challengeTTL)}
	return key, nil
}

// take consumes a ceremony of the kind the caller expects. SINGLE USE: the entry is removed whether
// or not what follows succeeds — and whether or not the KIND matched — so a challenge cannot be
// replayed against a second attempt, nor probed against the other finisher.
//
// THE KIND IS COMPARED HERE RATHER THAN BY THE CALLER, so no finisher can forget to. A `want` that
// the caller passes and this function ignores would be the same defect one layer up.
func (p *PasskeyCeremonies) take(key string, want ceremonyKind) (pendingCeremony, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.in[key]
	delete(p.in, key)
	if !ok || p.now().After(c.expires) || c.kind != want {
		return pendingCeremony{}, false
	}
	return c, true
}

// passkeyUser is the single admin, as WebAuthn's User.
//
// ONE ADMIN, NO ACCOUNTS — which is what permits DISCOVERABLE credentials, and therefore a login
// with no username field and no account picker. Every method here is constant except the credential
// list.
type passkeyUser struct {
	handle []byte
	creds  []webauthn.Credential
	// name is what the PLATFORM shows for this credential. Empty means the admin.
	//
	// IT EXISTS BECAUSE OF A MEASUREMENT (spec D2.1, taken 2026-08-20). iOS collapses credentials on
	// `(rpId, username)`, and every quince credential carried `adminUsername` — so three credentials
	// on one iPhone presented as ONE row, indistinguishable and unselectable, and sign-in silently
	// used one of them. Under this rung those credentials can carry DIFFERENT AUTHORITY, which makes
	// a silent choice between them the failure the rung must not ship.
	name string
}

func (u passkeyUser) WebAuthnID() []byte { return u.handle }

// WebAuthnName is what the sign-in sheet shows, and for a scoped credential it must not say `admin`.
//
// Operator, 2026-08-20: "household member must not be quince-admin, that's wild." A scoped
// credential's holder reaches one device; a sheet labelling it `quince-admin` tells them they hold
// the opposite. That is the state-honesty rule at the one place a user actually looks.
func (u passkeyUser) WebAuthnName() string {
	if u.name == "" {
		return adminUsername
	}
	return u.name
}

func (u passkeyUser) WebAuthnDisplayName() string                { return u.WebAuthnName() }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// adminUsername is the anchor quince#819 put on the login form, and for an ADMIN credential it must
// be THE SAME STRING. The keychain keys a credential on (origin, username), so an admin passkey
// registered under a different name would file itself as a second identity beside the password
// rather than beside it.
//
// A SCOPED CREDENTIAL IS THAT SECOND IDENTITY, DELIBERATELY, and this is a scoped EXCEPTION to
// quince#819 rather than a repeal of it — spec D2.1. It belongs to a different principal, on a
// different phone, with different authority, so filing it separately is what it should do. The
// constant stays correct for admin credentials and only for those; do not "restore" it for the
// scoped path.
//
// IT ALSO DISARMS THE COLLAPSE the measurement found: two credentials with different usernames do
// not merge into one unselectable row, so the platform cannot silently choose between authorities.
const adminUsername = "quince-admin"

// userHandle returns the admin's stable WebAuthn id, minting it once.
//
// A STORED RANDOM VALUE, NEVER DERIVED FROM THE PASSWORD. Deriving it would orphan every credential
// the moment the password changed — the authenticator holds this value and presents it back, so it
// has to outlive everything except the credentials themselves.
func userHandle(st *store.Store) ([]byte, error) {
	if v, ok, err := st.GetSetting(settingPasskeyUserHandle); err != nil {
		return nil, err
	} else if ok {
		return base64.RawURLEncoding.DecodeString(v)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	// SetSettingIfAbsent, not SetSetting: two concurrent first registrations must not each mint a
	// handle and have the second overwrite the first, which would orphan whatever the first stored.
	enc := base64.RawURLEncoding.EncodeToString(raw)
	inserted, err := st.SetSettingIfAbsent(settingPasskeyUserHandle, enc)
	if err != nil {
		return nil, err
	}
	if !inserted {
		v, _, err := st.GetSetting(settingPasskeyUserHandle)
		if err != nil {
			return nil, err
		}
		return base64.RawURLEncoding.DecodeString(v)
	}
	return raw, nil
}

// relyingParty builds the per-request handle. See the file header for why this is not a singleton.
//
// The origin is reconstructed as https://<rpID> rather than taken from the request, because that is
// the only origin a credential for this rpId may legitimately come from: WebAuthn requires a secure
// context, so http is not a candidate, and the port is excluded for the reason RPIDFromRequest
// gives. A mismatch here is caught by the library, loudly, at the ceremony rather than later.
func relyingParty(rpID string) (*webauthn.WebAuthn, error) {
	if !isUsableRPID(rpID) {
		return nil, ErrUnsupportedRPID{RPID: rpID}
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: rpDisplayName,
		RPID:          rpID,
		RPOrigins:     []string{"https://" + rpID},
		// DISCOVERABLE CREDENTIALS ARE REQUIRED, AND WITHOUT THIS THE WHOLE FEATURE IS INERT.
		//
		// The library's zero value requests no resident key, so the authenticator is free to create
		// a NON-discoverable credential — one that can only be used when the server already names
		// it in `allowCredentials`. quince never can: it has one admin, no account picker, and
		// `BeginDiscoverableLogin` deliberately sends an empty allow-list. So registration would
		// succeed, the credential would exist on the device, and login could never find it.
		//
		// MEASURED, not theorised: a passkey added on macOS was accepted and stored, and the login
		// form never offered it. That is exactly this — and it is a requirement the spec states in
		// as many words ("requires discoverable credentials, which this design wants anyway") that
		// the first implementation simply did not carry.
		//
		// `RequireResidentKey` is the legacy WebAuthn-1 spelling of the same thing and is set
		// alongside, because older authenticators read that and ignore `ResidentKey`.
		//
		// UserVerification PREFERRED rather than required: verification is what makes it Face ID
		// rather than a bare tap, and every platform authenticator this is built for does it — but
		// requiring it would refuse a security key that cannot, for a login that still has a
		// password beside it.
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationPreferred,
		},
	})
}

// BeginPasskeyRegistration starts the ceremony for the single admin, returning the options the
// browser needs and an opaque key the finish call must present.
// The `scope` argument decides what the platform will SHOW for this credential — the constant
// for an admin one, the device's name for a scoped one (spec D2.1). It is a positional
// argument for the same reason `store.InsertPasskey`'s is: a ceremony that forgot it would
// label a household member's phone `quince-admin`, and a forgotten argument is a compile error
// where a forgotten field is a silent wrong answer.
func BeginPasskeyRegistration(st *store.Store, cer *PasskeyCeremonies, rpID string,
	scope store.Scope) (any, string, error) {
	wa, err := relyingParty(rpID)
	if err != nil {
		return nil, "", err
	}
	handle, err := userHandle(st)
	if err != nil {
		return nil, "", err
	}
	username, err := scopeUsername(st, scope)
	if err != nil {
		return nil, "", err
	}
	existing, err := existingCredentials(st, rpID)
	if err != nil {
		return nil, "", err
	}

	// EXCLUDE WHAT IS ALREADY REGISTERED, so a second attempt on the same authenticator is refused
	// by the device with "you already have a passkey here" instead of silently minting a duplicate
	// the user cannot tell apart in the list.
	creation, session, err := wa.BeginRegistration(
		passkeyUser{handle: handle, creds: existing, name: username},
		webauthn.WithExclusions(credentialDescriptors(existing)),
	)
	if err != nil {
		return nil, "", err
	}
	key, err := cer.put(session, rpID, ceremonyRegister)
	if err != nil {
		return nil, "", err
	}
	return creation, key, nil
}

// FinishPasskeyRegistration verifies the authenticator's response and stores the credential.
func FinishPasskeyRegistration(st *store.Store, cer *PasskeyCeremonies, key, name, rpID string,
	r *http.Request, now time.Time, scope store.Scope) (store.Passkey, error) {
	pending, ok := cer.take(key, ceremonyRegister)
	if !ok {
		return store.Passkey{}, ErrNoChallenge
	}
	// THE CEREMONY'S OWN rpId WINS, not the one this request derived. They are normally identical;
	// where they are not, the user moved between access tiers mid-ceremony, and finishing against
	// the new one would store a credential the authenticator signed for the old.
	if pending.rpID != rpID {
		return store.Passkey{}, ErrRPIDMismatch{Registered: pending.rpID, Presented: rpID}
	}

	wa, err := relyingParty(pending.rpID)
	if err != nil {
		return store.Passkey{}, err
	}
	handle, err := userHandle(st)
	if err != nil {
		return store.Passkey{}, err
	}
	username, err := scopeUsername(st, scope)
	if err != nil {
		return store.Passkey{}, err
	}
	existing, err := existingCredentials(st, pending.rpID)
	if err != nil {
		return store.Passkey{}, err
	}

	// THE SAME USER OBJECT AS `Begin`, name included. The library compares what it built the
	// challenge from against what it verifies, so a name that differed between the two halves
	// of one ceremony would be a mismatch found at the worst moment — after Face ID.
	cred, err := wa.FinishRegistration(
		passkeyUser{handle: handle, creds: existing, name: username},
		pending.session, r)
	if err != nil {
		return store.Passkey{}, err
	}

	pk := store.Passkey{
		CredentialID: base64.RawURLEncoding.EncodeToString(cred.ID),
		PublicKey:    cred.PublicKey,
		RPID:         pending.rpID,
		SignCount:    cred.Authenticator.SignCount,
		// THE FLAGS, WITHOUT WHICH NO SYNCED PASSKEY CAN EVER SIGN IN. The library compares
		// BackupEligible on every assertion and refuses a mismatch; a credential reconstructed
		// without it reports false, while any iCloud- or Google-synced passkey asserts true.
		// Measured on hardware: registration, autofill and Face ID all succeeded and the server
		// then refused with "Backup Eligible flag inconsistency".
		BackupEligible: &cred.Flags.BackupEligible,
		BackupState:    &cred.Flags.BackupState,
		AAGUID:         cred.Authenticator.AAGUID,
		Name:           name,
		CreatedAt:      now,
	}
	// THE ADMIN CEREMONY STATES ITS SCOPE, rather than being correct by falling through a
	// default. This is the only registration path today; the scoped one arrives with
	// enrolment, and when it does the compiler will require it to answer this same question.
	if err := st.InsertPasskey(pk, scope); err != nil {
		return store.Passkey{}, err
	}
	return pk, nil
}

// existingCredentials loads this rpId's credentials in the library's shape.
//
// SCOPED TO THE rpId, because credentials for another domain are not candidates for this ceremony
// and offering them as exclusions would tell the authenticator about registrations it has no
// business knowing exist on this origin.
func existingCredentials(st *store.Store, rpID string) ([]webauthn.Credential, error) {
	rows, err := st.ListPasskeys()
	if err != nil {
		return nil, err
	}
	return credentialsFrom(rows, rpID)
}

// credentialsFrom converts stored rows to the library's shape, keeping only this rpId's.
//
// SHARED BY BOTH LOADERS SO THE CONVERSION CANNOT DRIFT. The rpId filter and the flag
// restoration below are correctness-critical for assertion — 0009 exists because a credential
// rebuilt without its backup flags can never sign in — and two copies of that logic would be
// two places for it to rot. Which ROWS to convert is the scope decision; how to convert them
// is not, and only the first belongs to the caller.
func credentialsFrom(rows []store.Passkey, rpID string) ([]webauthn.Credential, error) {
	out := make([]webauthn.Credential, 0, len(rows))
	for _, p := range rows {
		if p.RPID != rpID {
			continue
		}
		id, err := base64.RawURLEncoding.DecodeString(p.CredentialID)
		if err != nil {
			// A row we cannot decode is a row we cannot exclude. Skipping is right and silent is
			// not: it would let a duplicate be minted with no trace of why.
			return nil, fmt.Errorf("passkey %q: undecodable credential id: %w", p.Name, err)
		}
		c := webauthn.Credential{ID: id, PublicKey: p.PublicKey}
		// Restored, or the assertion is validated against zero flags — see InsertPasskey above.
		if p.BackupEligible != nil {
			c.Flags.BackupEligible = *p.BackupEligible
		}
		if p.BackupState != nil {
			c.Flags.BackupState = *p.BackupState
		}
		c.Authenticator.SignCount = p.SignCount
		c.Authenticator.AAGUID = p.AAGUID
		out = append(out, c)
	}
	return out, nil
}

// credentialDescriptors is the exclusion list in the protocol's shape.
func credentialDescriptors(creds []webauthn.Credential) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(creds))
	for i := range creds {
		out = append(out, creds[i].Descriptor())
	}
	return out
}

// adminCredentials loads this rpId's ADMIN credentials in the library's shape.
//
// THE SCOPE-AWARE SIBLING OF `existingCredentials`, and the pair is deliberate. This one answers
// "which credentials speak for the admin"; the other answers "which credentials exist at this
// origin". Before qn.13 those were the same question, and every caller of the second was really
// asking the first (spec D6).
//
// A CALLER THAT WANTS EVERY CREDENTIAL STILL HAS ONE. The exclusion list at registration genuinely
// means "every credential this authenticator might already hold", so it keeps `existingCredentials`
// — narrowing it would let a duplicate be minted on an authenticator that already has a scoped one.
func adminCredentials(st *store.Store, rpID string) ([]webauthn.Credential, error) {
	rows, err := st.ListAdminPasskeys()
	if err != nil {
		return nil, err
	}
	return credentialsFrom(rows, rpID)
}

// scopeUsername is what the platform will SHOW for a credential with this scope.
//
// EMPTY MEANS THE ADMIN, and `passkeyUser.WebAuthnName` turns that into `adminUsername`. A scoped
// credential gets its DEVICE'S NAME (spec D2.1) — the only name that is both meaningful to its
// holder and true.
//
// AN UNKNOWN DEVICE IS AN ERROR, NOT A FALLBACK, and the reason is the measurement this rule came
// from. iOS collapses credentials on `(rpId, username)`, so a generic fallback like "quince device"
// would make every unknown-device credential collapse into ONE unselectable row — reintroducing the
// exact defect D2.1 exists to remove, at the moment quince is least sure what it is looking at. The
// udid itself is not an option either: it is Operator-private and would be displayed on a screen.
//
// It is also not a state that should arise. Enrolment is reached from a device's own page, so the
// device is known by construction; if it is not, refusing is the honest answer.
func scopeUsername(st *store.Store, scope store.Scope) (string, error) {
	if scope.IsAdmin() {
		return "", nil
	}
	udid := scope.UDID()
	rows, err := st.ListDeviceIdentities()
	if err != nil {
		return "", err
	}
	// AMBIGUITY IS REFUSED FOR THE SAME REASON AS AN UNKNOWN DEVICE, and quince can see it
	// coming (quince#1368 review). `device_identity.name` has no UNIQUE constraint — it is
	// whatever the device calls itself, refreshed on Enrich — so two devices CAN share a name.
	// Two scoped credentials would then carry the same `user.name`, and iOS collapses on
	// `(rpId, username)`: one unselectable row granting two different devices. That is the
	// defect D2.1 removes, reached by a shorter path than the fallback this function already
	// refuses — and the device list is right here, so not looking would be a choice.
	var match string
	for _, d := range rows {
		if d.Name == "" {
			continue
		}
		if d.UDID == udid {
			match = d.Name
		}
	}
	if match == "" {
		return "", ErrUnknownScopeDevice
	}
	for _, d := range rows {
		if d.UDID != udid && d.Name == match {
			return "", ErrAmbiguousScopeDevice{Name: match}
		}
	}
	// A DEVICE NAMED `quince-admin` IS DELIBERATELY NOT REFUSED — Operator, 2026-08-21, given in
	// session and relayed here; it is not on the forge, and this comment is the whole of its
	// provenance.
	//
	// It would collide with the admin credential's username and collapse the same way two same-named
	// devices do. It is refused nowhere, and that is a DECISION rather than the gap it looks like:
	// "I'd do nothing, I don't think it's worth our attention."
	//
	// Recorded because the absence is the kind a later reader corrects. The ambiguity check directly
	// above refuses two devices sharing a name, so the obvious next thought is that the admin anchor
	// should be in that set — and adding it would be undoing a ruling, not closing a hole.
	return match, nil
}

// ErrUnknownScopeDevice — a credential was scoped to a device quince cannot name.
//
// Named rather than generic because the remedy is specific: the device has to be known to quince
// before a credential can be confined to it, and a caller meeting this has issued the QR for
// something that is not on the Devices list.
var ErrUnknownScopeDevice = errors.New("auth: cannot name the device this credential is scoped to")

// ErrAmbiguousScopeDevice — two devices share the name this credential would be labelled with.
//
// A STRUCT RATHER THAN A SENTINEL, because the remedy is only actionable if the message names the
// name. "Two devices share a name" sends the operator to look; "two devices are called `iPad`" tells
// them what to look for.
//
// THE REMEDY IS ON THE DEVICE, NOT IN quince, and that is why this error carries it. quince has no
// rename endpoint — `device_identity.name` is whatever the device calls itself, refreshed on Enrich
// (0004) — so "rename it and retry" means renaming the iPhone or iPad in its own Settings. A message
// that said "rename it here" would name a control that does not exist, which is the *troubleshooting
// is actionable* rule failing in the most frustrating way available.
type ErrAmbiguousScopeDevice struct {
	Name string
}

func (e ErrAmbiguousScopeDevice) Error() string {
	return fmt.Sprintf("auth: two devices are called %q, so a credential scoped to one could not be "+
		"told from the other on your phone — rename one on the device itself "+
		"(Settings → General → About → Name), then issue the code again", e.Name)
}
