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

type pendingCeremony struct {
	session webauthn.SessionData
	rpID    string
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
func (p *PasskeyCeremonies) put(session *webauthn.SessionData, rpID string) (string, error) {
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
	p.in[key] = pendingCeremony{session: *session, rpID: rpID, expires: now.Add(challengeTTL)}
	return key, nil
}

// take consumes a ceremony. SINGLE USE: the entry is removed whether or not what follows succeeds,
// so a challenge cannot be replayed against a second attempt.
func (p *PasskeyCeremonies) take(key string) (pendingCeremony, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.in[key]
	delete(p.in, key)
	if !ok || p.now().After(c.expires) {
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
}

func (u passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u passkeyUser) WebAuthnName() string                       { return adminUsername }
func (u passkeyUser) WebAuthnDisplayName() string                { return adminUsername }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// adminUsername is the anchor quince#819 put on the login form, and it must be THE SAME STRING.
// The keychain keys a credential on (origin, username), so a passkey registered under a different
// name would file itself as a second identity beside the password rather than beside it.
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
	})
}

// BeginPasskeyRegistration starts the ceremony for the single admin, returning the options the
// browser needs and an opaque key the finish call must present.
func BeginPasskeyRegistration(st *store.Store, cer *PasskeyCeremonies, rpID string) (any, string, error) {
	wa, err := relyingParty(rpID)
	if err != nil {
		return nil, "", err
	}
	handle, err := userHandle(st)
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
		passkeyUser{handle: handle, creds: existing},
		webauthn.WithExclusions(credentialDescriptors(existing)),
	)
	if err != nil {
		return nil, "", err
	}
	key, err := cer.put(session, rpID)
	if err != nil {
		return nil, "", err
	}
	return creation, key, nil
}

// FinishPasskeyRegistration verifies the authenticator's response and stores the credential.
func FinishPasskeyRegistration(st *store.Store, cer *PasskeyCeremonies, key, name, rpID string,
	r *http.Request, now time.Time) (store.Passkey, error) {
	pending, ok := cer.take(key)
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
	existing, err := existingCredentials(st, pending.rpID)
	if err != nil {
		return store.Passkey{}, err
	}

	cred, err := wa.FinishRegistration(passkeyUser{handle: handle, creds: existing}, pending.session, r)
	if err != nil {
		return store.Passkey{}, err
	}

	pk := store.Passkey{
		CredentialID: base64.RawURLEncoding.EncodeToString(cred.ID),
		PublicKey:    cred.PublicKey,
		RPID:         pending.rpID,
		SignCount:    cred.Authenticator.SignCount,
		AAGUID:       cred.Authenticator.AAGUID,
		Name:         name,
		CreatedAt:    now,
	}
	if err := st.InsertPasskey(pk); err != nil {
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
