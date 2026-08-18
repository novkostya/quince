// Package push implements the two IETF specifications a Web Push delivery needs: message
// encryption (RFC 8291, `aes128gcm`) and voluntary application server identification (RFC 8292,
// VAPID). It is the transport-independent half of qn.12 — it produces a body and a set of headers
// and knows nothing about subscriptions, storage, or the job engine.
//
// NO MODULE IS ADDED FOR THIS, AND THAT IS A DECISION (spec D3, quince#1127). The whole of it is
// well-specified transforms over `crypto/ecdh`, `crypto/hkdf`, `crypto/ecdsa` and `crypto/aes`,
// which the spec measured as present in the pinned toolchain before committing to this. Three
// reasons it is also right: `webpush-go`'s last release is 19 months old and this is a protocol
// quince must be able to fix on its own timetable; the RFCs publish test vectors, so the code can be
// tested against the standard rather than against itself (push_test.go); and a self-hosted daemon
// aimed at low-end NAS hardware pays for every dependency it takes.
//
// THE HONEST COST is cryptographic code we own. It is bounded by testing against RFC 8291 §5's
// worked example end to end — every intermediate value, not just the ciphertext, so a failure says
// which transform is wrong rather than that something is.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// b64 is base64url WITHOUT padding, which is what every field in both RFCs uses — the subscription
// keys a browser hands over, the `k` parameter, and the JWT segments. Decoding is strict about
// padding in the other direction, so `RawURLEncoding` for both halves rather than a permissive
// decoder that would accept a subscription no push service would.
var b64 = base64.RawURLEncoding

const (
	// recordSize is the `rs` parameter written into the aes128gcm header — the record length a
	// receiver should frame on, matching RFC 8291 §5's worked example. RFC 8291 §4 constrains it
	// from BELOW only: `rs` MUST exceed plaintext + padding delimiter + padding + the 16-octet tag.
	// quince always sends exactly ONE record, which is allowed to be shorter than this; see the
	// header assembly in Encrypt for why writing the record's actual length instead is a
	// near-invisible bug.
	recordSize = 4096

	// maxBodyOctets is the PAYLOAD BODY ceiling, and it is a different quantity from `rs`. RFC 8030
	// §7.2: "Push services MUST NOT return a 413 status code in responses to an entity body that is
	// 4096 bytes or less in size" — relayed by RFC 8291 §4 as "a push service is not required to
	// support more than 4096 octets of payload body". It bounds the WHOLE request body: header,
	// ciphertext and tag. Both sentences read from the RFCs, not recalled.
	//
	// IT IS A SEPARATE CONSTANT THAT HAPPENS TO EQUAL recordSize TODAY, and writing them as one is
	// the trap this exists to remove (quince#1149). Derive maxPayload from `rs` and raising `rs` to
	// 8192 — a perfectly legal widening — silently takes maxPayload to 8089, over the body ceiling,
	// with NOTHING local failing: the refusal test asserts against maxPayload, so it follows the
	// constant and stays green. What breaks is real deliveries, at push services, indistinguishable
	// from the dead-subscription case D8 was careful to make loud.
	maxBodyOctets = 4096

	// maxPayload is the plaintext ceiling. Working backwards from the BODY ceiling through the
	// aes128gcm framing — an 86-octet header (16 salt + 4 rs + 1 idlen + 65 keyid), one padding
	// delimiter and a 16-octet GCM tag — leaves 3993 octets.
	//
	// IT IS A REFUSAL, NEVER A TRUNCATION. Silently cutting a notification to fit is the *no silent
	// caps* failure in its purest form: the user gets a message that reads complete and is not.
	maxPayload = maxBodyOctets - 86 - 1 - 16
)

// Subscription is what a browser's PushSubscription serialises to, and what the client POSTs.
//
// EVERY FIELD IS CAPABILITY-GRADE. Anyone holding this triple can push to that device, so it is
// never logged, never served to another session, and never enters a fixture (design §6, spec D8).
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256DH   string `json:"p256dh"` // the UA's public key, X9.62 uncompressed, base64url
	Auth     string `json:"auth"`   // the 16-octet authentication secret, base64url
}

// Notification is the payload. It serialises to the Declarative Web Push envelope, which is what
// lets one message satisfy both mechanisms (spec D2).
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Navigate is where the tap lands. Required by Declarative Web Push, and for quince it is always
	// the device page, so a notification can never be a dead end.
	Navigate string `json:"navigate"`
	// Kind is quince's own routing tag. It rides inside the notification object rather than beside
	// it so the service worker and the declarative path read the same document.
	Kind string `json:"kind,omitempty"`
}

// Envelope is the Declarative Web Push document (WebKit, Safari 18.4+).
//
// `web_push: 8030` IS THE MAGIC NUMBER THE SPEC DEFINES, not a version we chose — it is what signals
// declarative parsing to the user agent. A service worker reads `notification` out of the same
// document, so there is no second payload shape to keep in sync.
type Envelope struct {
	WebPush      int          `json:"web_push"`
	Notification Notification `json:"notification"`
}

// MarshalPayload renders a notification as the declarative envelope.
func MarshalPayload(n Notification) ([]byte, error) {
	if strings.TrimSpace(n.Title) == "" {
		// Declarative Web Push requires a non-empty title; a user agent drops the whole message
		// without one. Refused here so the failure is a Go error rather than a notification that
		// never arrives and leaves no trace anywhere.
		return nil, errors.New("push: notification title is empty")
	}
	return json.Marshal(Envelope{WebPush: 8030, Notification: n})
}

// Encrypt produces an RFC 8291 `aes128gcm` body for one subscription.
//
// `ephemeral` is the application server's per-message key pair. It is a parameter rather than
// generated inside so the RFC's worked example can be replayed exactly; every caller outside the
// test passes nil and gets a fresh one, which is the only correct behaviour in production — reusing
// it across messages would reuse the CEK and the nonce.
func Encrypt(sub Subscription, plaintext []byte, ephemeral *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > maxPayload {
		return nil, fmt.Errorf("push: payload is %d octets, over the %d the push service must accept", len(plaintext), maxPayload)
	}
	uaPublicRaw, err := b64.DecodeString(sub.P256DH)
	if err != nil {
		return nil, fmt.Errorf("push: p256dh is not base64url: %w", err)
	}
	uaPublic, err := ecdh.P256().NewPublicKey(uaPublicRaw)
	if err != nil {
		return nil, fmt.Errorf("push: p256dh is not a P-256 point: %w", err)
	}
	authSecret, err := b64.DecodeString(sub.Auth)
	if err != nil {
		return nil, fmt.Errorf("push: auth is not base64url: %w", err)
	}
	if ephemeral == nil {
		if ephemeral, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
			return nil, fmt.Errorf("push: generate ephemeral key: %w", err)
		}
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("push: generate salt: %w", err)
		}
	}
	asPublicRaw := ephemeral.PublicKey().Bytes()

	shared, err := ephemeral.ECDH(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("push: ecdh: %w", err)
	}

	_, cek, nonce, err := derive(shared, uaPublicRaw, asPublicRaw, authSecret, salt)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("push: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("push: gcm: %w", err)
	}

	// RFC 8188 §2: the record's plaintext carries a delimiter octet. `0x02` marks the LAST record,
	// and quince always sends exactly one — a payload that needed two would be over the size limit
	// refused above. `0x01` here would make the user agent wait for a record that never comes.
	record := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := aead.Seal(nil, nonce, record, nil)

	// RFC 8188 §2.1, the content-coding header:
	//   salt(16) || rs(4, big-endian) || idlen(1) || keyid(idlen)
	// where for Web Push the keyid IS the application server's ephemeral public key.
	//
	// `rs` IS THE RECORD-SIZE PARAMETER, NOT THIS RECORD'S LENGTH — and the difference is invisible
	// until you check against the standard. It declares how long each record IS, which lets a
	// receiver frame a multi-record stream; the FINAL record is allowed to be shorter. Writing the
	// actual length instead produces a body whose every other octet is byte-identical to the RFC's
	// worked example, so nothing but the vector test would have caught it (it did).
	header := make([]byte, 0, 16+4+1+len(asPublicRaw)+len(ciphertext))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPublicRaw)))
	header = append(header, asPublicRaw...)
	return append(header, ciphertext...), nil
}

// derive is RFC 8291 §3.4's key schedule — TWO HKDF PASSES, and the first is the Web-Push-specific
// part that RFC 8188 alone does not have:
//
//	IKM   = HKDF(salt: auth_secret, ikm: ecdh_secret,
//	             info: "WebPush: info" || 0x00 || ua_public || as_public, 32)
//	CEK   = HKDF(salt: salt, ikm: IKM, info: "Content-Encoding: aes128gcm" || 0x00, 16)
//	NONCE = HKDF(salt: salt, ikm: IKM, info: "Content-Encoding: nonce" || 0x00, 12)
//
// THE TWO SALTS ARE DIFFERENT VALUES WITH THE SAME NAME. `auth_secret` salts the first pass and
// `salt` salts the second; transposing them yields a body that encrypts cleanly and that nobody on
// earth can decrypt — a failure with no symptom at this layer and no error anywhere. That is why
// this is a named function the test drives directly against the RFC's intermediate values, rather
// than four lines inline in Encrypt where only the final ciphertext could be checked.
func derive(shared, uaPublic, asPublic, authSecret, salt []byte) (ikm, cek, nonce []byte, err error) {
	keyInfo := make([]byte, 0, len("WebPush: info")+1+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, "WebPush: info"...)
	keyInfo = append(keyInfo, 0x00)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)

	if ikm, err = hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32); err != nil {
		return nil, nil, nil, fmt.Errorf("push: derive ikm: %w", err)
	}
	if cek, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, nil, fmt.Errorf("push: derive cek: %w", err)
	}
	if nonce, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12); err != nil {
		return nil, nil, nil, fmt.Errorf("push: derive nonce: %w", err)
	}
	return ikm, cek, nonce, nil
}

// VAPIDKey is the application server's identity key pair.
//
// IT LIVES IN THE APP DB — Operator ruling 2026-08-17, quince#1128, design §6. Two constraints
// travel with that ruling and are enforced by the callers rather than here, because this package
// holds no storage: **never regenerate a key silently** when subscriptions exist, and **never offer
// rotation** — it is destructive by construction, since the public half is baked into every
// subscription a phone has ever created.
type VAPIDKey struct {
	private *ecdsa.PrivateKey
}

// GenerateVAPIDKey mints a new P-256 key pair. Called once, on first use.
func GenerateVAPIDKey() (*VAPIDKey, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("push: generate vapid key: %w", err)
	}
	return &VAPIDKey{private: k}, nil
}

// PrivateBytes is the 32-octet big-endian scalar, which is what the app DB stores.
//
// A RAW SCALAR RATHER THAN PEM OR SEC1. The ruling puts this in a database column, not in a file an
// operator inspects, so an envelope format buys nothing and adds a parser. Fixed width, so a
// truncated read is a length error rather than a key that is quietly wrong.
func (k *VAPIDKey) PrivateBytes() []byte {
	b := make([]byte, 32)
	k.private.D.FillBytes(b)
	return b
}

// VAPIDKeyFromBytes reconstructs a key pair from what PrivateBytes wrote.
func VAPIDKeyFromBytes(b []byte) (*VAPIDKey, error) {
	if len(b) != 32 {
		return nil, fmt.Errorf("push: vapid private key is %d octets, want 32", len(b))
	}
	// `crypto/ecdh` is used here purely as the SCALAR VALIDATOR AND POINT MULTIPLIER, not because this
	// is an ECDH key — it rejects zero and out-of-range scalars, and yields the public point, which
	// is otherwise only reachable through `elliptic.ScalarBaseMult` (deprecated since Go 1.21 as a
	// low-level unsafe API). Same curve, same arithmetic, no deprecated call.
	ek, err := ecdh.P256().NewPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("push: vapid private key is not a valid P-256 scalar: %w", err)
	}
	point := ek.PublicKey().Bytes() // X9.62 uncompressed: 0x04 || X(32) || Y(32)
	priv := &ecdsa.PrivateKey{D: new(big.Int).SetBytes(b)}
	priv.Curve = elliptic.P256()
	priv.X = new(big.Int).SetBytes(point[1:33])
	priv.Y = new(big.Int).SetBytes(point[33:])
	return &VAPIDKey{private: priv}, nil
}

// PublicKey is the X9.62 uncompressed point — the `k` parameter of the Authorization header, and
// the `applicationServerKey` every subscription is created against.
//
// RFC 8292 §3.2 chooses X9.62 over JWK deliberately: *"A JWK does not have a canonical form, so
// X9.62 encoding makes it easier for the push service to handle comparison of keys from different
// sources."* Do not serve a JWK here to be helpful.
// The point is assembled by hand rather than with `elliptic.Marshal`, which is deprecated: X9.62
// uncompressed is `0x04 || X || Y` with both coordinates left-padded to the curve's 32 octets, and
// FillBytes is what guarantees the padding. A coordinate with a leading zero byte is the case that
// makes a naive `X.Bytes()` produce a 64-byte point roughly one time in 256 — accepted by nothing,
// and rare enough to ship.
func (k *VAPIDKey) PublicKey() []byte {
	out := make([]byte, 65)
	out[0] = 0x04
	k.private.X.FillBytes(out[1:33])
	k.private.Y.FillBytes(out[33:])
	return out
}

// PublicKeyBase64 is what GET /api/notifications serves and what the browser passes to subscribe().
func (k *VAPIDKey) PublicKeyBase64() string { return b64.EncodeToString(k.PublicKey()) }

// AuthorizationHeader builds the RFC 8292 `vapid` credential for one endpoint.
//
// `now` is a parameter so the expiry is testable without waiting; every caller passes time.Now().
func (k *VAPIDKey) AuthorizationHeader(endpoint, subject string, now time.Time) (string, error) {
	aud, err := audienceOf(endpoint)
	if err != nil {
		return "", err
	}
	// RFC 8292 §2: *"An 'exp' claim MUST NOT be more than 24 hours from the time of the request."*
	// Twelve hours rather than the maximum, so a small clock skew on either side cannot turn a valid
	// token into a rejected one — a push service refusing every delivery over an expiry is a
	// failure that looks like a dead subscription.
	exp := now.Add(12 * time.Hour).Unix()

	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{"aud": aud, "exp": exp, "sub": subject})
	if err != nil {
		return "", fmt.Errorf("push: marshal vapid claims: %w", err)
	}
	signingInput := header + "." + b64.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil {
		return "", fmt.Errorf("push: sign vapid token: %w", err)
	}
	// JWS ES256 is the FIXED-WIDTH r||s pair, not the ASN.1 DER that `ecdsa.SignASN1` produces.
	// Both are valid ECDSA signatures and only one is a valid JWT, so a push service rejects the DER
	// form with an authentication error that says nothing about encoding.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return fmt.Sprintf("vapid t=%s.%s, k=%s", signingInput, b64.EncodeToString(sig), k.PublicKeyBase64()), nil
}

// audienceOf reduces an endpoint URL to the origin RFC 8292 wants as `aud`.
//
// SCHEME AND AUTHORITY ONLY. Sending the full endpoint is a common mistake and it leaks the
// subscription — a capability — into a token that transits differently from the request body.
func audienceOf(endpoint string) (string, error) {
	scheme, rest, ok := strings.Cut(endpoint, "://")
	if !ok || scheme == "" || rest == "" {
		return "", fmt.Errorf("push: endpoint %q has no scheme", redactEndpoint(endpoint))
	}
	host, _, _ := strings.Cut(rest, "/")
	if host == "" {
		return "", fmt.Errorf("push: endpoint %q has no host", redactEndpoint(endpoint))
	}
	return scheme + "://" + host, nil
}

// redactEndpoint is what any error, log line or metric may say about an endpoint.
//
// AN ENDPOINT IS A CAPABILITY (design §6, spec D8): its path carries an opaque token, and anyone
// holding the whole URL plus the subscription keys can push to that phone. So the origin is the most
// that may ever be printed — and errors are the path this leaks by, because an error string built
// from a URL ends up in a log without anybody deciding it should.
func redactEndpoint(endpoint string) string {
	scheme, rest, ok := strings.Cut(endpoint, "://")
	if !ok {
		return "<endpoint>"
	}
	host, _, _ := strings.Cut(rest, "/")
	if host == "" {
		return "<endpoint>"
	}
	return scheme + "://" + host + "/<redacted>"
}

// RedactEndpoint is redactEndpoint for callers outside this package — the delivery client and
// anything that logs about a subscription.
func RedactEndpoint(endpoint string) string { return redactEndpoint(endpoint) }

// ParseSubscriptionKeys validates the two keys a browser hands over, returning the decoded UA public
// point and auth secret.
//
// IT EXISTS SO A BAD SUBSCRIPTION IS REFUSED AT THE DOOR. `Encrypt` would reject the same input, but
// it runs at SEND time — days later, on a schedule, with nobody watching — where the only symptom is
// a notification that never arrives. Validating when the subscription is created turns that into an
// error on the request that caused it.
func ParseSubscriptionKeys(p256dh, auth string) (uaPublic []byte, authSecret []byte, err error) {
	uaPublic, err = b64.DecodeString(p256dh)
	if err != nil {
		return nil, nil, fmt.Errorf("push: p256dh is not base64url: %w", err)
	}
	if _, err := ecdh.P256().NewPublicKey(uaPublic); err != nil {
		return nil, nil, fmt.Errorf("push: p256dh is not a P-256 point: %w", err)
	}
	authSecret, err = b64.DecodeString(auth)
	if err != nil {
		return nil, nil, fmt.Errorf("push: auth is not base64url: %w", err)
	}
	// RFC 8291 §3.2 fixes the authentication secret at 16 octets. A shorter one changes the key
	// derivation and yields a body the device cannot decrypt — silently, at send time.
	if len(authSecret) != 16 {
		return nil, nil, fmt.Errorf("push: auth secret is %d octets, want 16", len(authSecret))
	}
	return uaPublic, authSecret, nil
}

// EndpointFingerprint identifies a subscription to the browser that owns it, without anyone sending
// an endpoint.
//
// THE ENDPOINT IS A CAPABILITY AND ITS DIGEST IS NOT. Anyone holding endpoint + keys can push to that
// phone, which is why `GET /api/notifications` returns labels and states and never an endpoint (spec
// D8). A SHA-256 is one-way, and a push endpoint carries a long random token, so there is nothing to
// brute-force back. The browser hashes the endpoint it already holds and compares.
//
// base64url WITHOUT PADDING, matching every other encoded field in this package.
func EndpointFingerprint(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return b64.EncodeToString(sum[:])
}
