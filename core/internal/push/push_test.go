package push

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

// RFC 8291 §5's worked example, transcribed from the RFC rather than produced by this code.
//
// THIS IS THE GATE THE `no new dependency` DECISION IS PAID FOR WITH (spec D3, G2). A crypto
// implementation tested against its own output proves only that it is deterministic. These values
// come from the standard, so passing them means quince and every push service agree.
//
// Provenance: RFC 8291, section 5, fetched from rfc-editor.org 2026-08-17. Nothing here came from a
// device, and nothing here is a secret — the RFC publishes the private keys precisely so this test
// can exist.
const (
	rfcPlaintext  = "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24"
	rfcUAPublic   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcUAPrivate  = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcASPublic   = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcASPrivate  = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt       = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcAuthSecret = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcCiphertext = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	if err != nil {
		t.Fatalf("fixture %q is not base64url: %v", s, err)
	}
	return b
}

// THE WHOLE OF RFC 8291 §5, END TO END. If this passes, quince's ciphertext is byte-identical to
// what the standard says it must be for these inputs.
func TestEncryptReproducesTheRFC8291Example(t *testing.T) {
	ephemeral, err := ecdh.P256().NewPrivateKey(mustDecode(t, rfcASPrivate))
	if err != nil {
		t.Fatalf("the RFC's application-server private key did not load: %v", err)
	}
	// A sanity check on the fixture BEFORE the claim: if the RFC's private key does not yield the
	// RFC's public key, the transcription is wrong and every later failure would be misattributed.
	if got := b64.EncodeToString(ephemeral.PublicKey().Bytes()); got != rfcASPublic {
		t.Fatalf("transcription error: as_private yields %s, RFC says %s", got, rfcASPublic)
	}

	body, err := Encrypt(
		Subscription{P256DH: rfcUAPublic, Auth: rfcAuthSecret},
		mustDecode(t, rfcPlaintext),
		ephemeral,
		mustDecode(t, rfcSalt),
	)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if got := b64.EncodeToString(body); got != rfcCiphertext {
		t.Errorf("the encrypted body does not match RFC 8291 §5.\n got: %s\nwant: %s", got, rfcCiphertext)
	}
}

// THE INTERMEDIATE VALUES, so a failure says WHICH transform is wrong.
//
// The two HKDF passes both take something called a salt, and they are different values: the first
// takes `auth_secret`, the second takes `salt`. Transposing them produces a body that encrypts
// cleanly and that nobody on earth can decrypt — a failure with no symptom at this layer. Asserting
// the derived CEK and nonce against the RFC is what turns that into a named test failure.
func TestTheKeyDerivationMatchesTheRFCStepByStep(t *testing.T) {
	const (
		wantSecret = "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs"
		wantIKM    = "S4lYMb_L0FxCeq0WhDx813KgSYqU26kOyzWUdsXYyrg"
		wantCEK    = "oIhVW04MRdy2XN9CiKLxTg"
		wantNonce  = "4h_95klXJ5E_qnoN"
	)
	as, err := ecdh.P256().NewPrivateKey(mustDecode(t, rfcASPrivate))
	if err != nil {
		t.Fatalf("as_private: %v", err)
	}
	ua, err := ecdh.P256().NewPublicKey(mustDecode(t, rfcUAPublic))
	if err != nil {
		t.Fatalf("ua_public: %v", err)
	}
	shared, err := as.ECDH(ua)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	if got := b64.EncodeToString(shared); got != wantSecret {
		t.Fatalf("ecdh secret = %s, RFC says %s", got, wantSecret)
	}

	ikm, cek, nonce := deriveForTest(t, shared, mustDecode(t, rfcUAPublic), as.PublicKey().Bytes(),
		mustDecode(t, rfcAuthSecret), mustDecode(t, rfcSalt))
	if got := b64.EncodeToString(ikm); got != wantIKM {
		t.Errorf("IKM = %s, RFC says %s — the `WebPush: info` pass is wrong", got, wantIKM)
	}
	if got := b64.EncodeToString(cek); got != wantCEK {
		t.Errorf("CEK = %s, RFC says %s", got, wantCEK)
	}
	if got := b64.EncodeToString(nonce); got != wantNonce {
		t.Errorf("NONCE = %s, RFC says %s", got, wantNonce)
	}
}

// THE UA CAN ACTUALLY DECRYPT IT, which is a different claim from "the bytes match the RFC".
//
// The vector test proves quince agrees with the standard for ONE input. This proves the round trip
// for a freshly generated ephemeral key and a random salt — the production path, where no fixture
// exists to compare against. Written as the receiver, using the RFC's user-agent PRIVATE key.
func TestAUserAgentCanDecryptWhatWeSend(t *testing.T) {
	uaPriv, err := ecdh.P256().NewPrivateKey(mustDecode(t, rfcUAPrivate))
	if err != nil {
		t.Fatalf("ua_private: %v", err)
	}
	plaintext := []byte(`{"web_push":8030,"notification":{"title":"quince","navigate":"/devices/x"}}`)
	body, err := Encrypt(Subscription{P256DH: rfcUAPublic, Auth: rfcAuthSecret}, plaintext, nil, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := decryptAsUserAgent(t, uaPriv, mustDecode(t, rfcAuthSecret), body)
	if err != nil {
		t.Fatalf("a user agent could not decrypt our body: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("round trip changed the payload:\n got %q\nwant %q", got, plaintext)
	}
}

// EVERY MESSAGE MUST GET A FRESH EPHEMERAL KEY AND SALT. Reusing either reuses the CEK and the
// nonce, which is the one catastrophic misuse of AES-GCM. `Encrypt(nil, nil)` is the production
// call, so this asserts the default rather than a parameter.
func TestTwoMessagesNeverShareAKeyOrSalt(t *testing.T) {
	sub := Subscription{P256DH: rfcUAPublic, Auth: rfcAuthSecret}
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		body, err := Encrypt(sub, []byte("same plaintext every time"), nil, nil)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		// The salt is the first 16 octets and the ephemeral public key is the keyid — both are in
		// the header, so identical headers across two messages is the failure, visible without
		// reaching inside.
		head := string(body[:16+4+1+65])
		if seen[head] {
			t.Fatalf("two messages shared a salt and ephemeral key — the CEK and nonce repeat")
		}
		seen[head] = true
	}
}

// AN OVERSIZE PAYLOAD IS REFUSED, NOT TRUNCATED. A notification cut to fit reads as complete and is
// not, which is the *no silent caps* rule at its most literal.
func TestAnOversizePayloadIsRefused(t *testing.T) {
	_, err := Encrypt(Subscription{P256DH: rfcUAPublic, Auth: rfcAuthSecret},
		make([]byte, maxPayload+1), nil, nil)
	if err == nil {
		t.Fatalf("an oversize payload was accepted")
	}
	if !strings.Contains(err.Error(), "over the") {
		t.Errorf("the refusal does not say what the limit was: %v", err)
	}
	// And the largest legal payload still goes through, so the boundary is not off by one.
	if _, err := Encrypt(Subscription{P256DH: rfcUAPublic, Auth: rfcAuthSecret},
		make([]byte, maxPayload), nil, nil); err != nil {
		t.Errorf("the largest legal payload was refused: %v", err)
	}
}

// THE BODY A PUSH SERVICE MUST ACCEPT IS 4096 OCTETS, AND THIS IS THE ONE ASSERTION THAT DOES NOT
// FOLLOW THE CONSTANTS (RFC 8030 §7.2, relayed by RFC 8291 §4; quince#1149).
//
// Every other size test here compares against `maxPayload`, so it moves with whatever `maxPayload`
// is defined as and cannot see a derivation that has drifted. `rs` and the body ceiling are
// different quantities from different RFCs that coincide at 4096 today: raise `recordSize` to 8192,
// a legal `rs`, and a maxPayload derived from it goes to 8089 with the whole suite still green.
//
// So this measures the ACTUAL ENCRYPTED BODY of the largest accepted payload against the literal
// ceiling. It is the only thing here that fails if the derivation is re-attached to the wrong limit.
func TestTheLargestAcceptedPayloadStillFitsTheBodyCeiling(t *testing.T) {
	const rfc8030BodyCeiling = 4096 // §7.2, written out rather than referenced on purpose

	body, err := Encrypt(Subscription{P256DH: rfcUAPublic, Auth: rfcAuthSecret},
		make([]byte, maxPayload), nil, nil)
	if err != nil {
		t.Fatalf("the largest legal payload was refused: %v", err)
	}
	if len(body) > rfc8030BodyCeiling {
		t.Fatalf("the largest accepted payload encrypts to %d octets, over the %d a push service "+
			"must accept — maxPayload is derived from the wrong limit", len(body), rfc8030BodyCeiling)
	}
	// And it is not needlessly small either: the ceiling is meant to be reached, so a maxPayload
	// left far under it would be a silent cap of its own.
	if len(body) != rfc8030BodyCeiling {
		t.Errorf("the largest accepted payload encrypts to %d octets, not the full %d — the framing "+
			"arithmetic and the ceiling disagree", len(body), rfc8030BodyCeiling)
	}
}

// ---------------------------------------------------------------------------------------------
// RFC 8292 — VAPID.
//
// ES256 IS NON-DETERMINISTIC, so there is no signature to compare against a fixture: two correct
// signings of one input differ. What CAN be asserted is everything else — the header, the claims,
// the encodings the RFC mandates, and that the signature verifies under the key the header advertises.
// Stated because "no vector" reads like a gap, and it is a property of the algorithm.
// ---------------------------------------------------------------------------------------------

func TestTheVAPIDHeaderIsWellFormedAndVerifies(t *testing.T) {
	k, err := GenerateVAPIDKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	now := time.Unix(1453523768, 0)
	got, err := k.AuthorizationHeader("https://push.example.net/push/JzLQ3raZJfFBR0aqvOMsLrt54w4rJUsV", "mailto:push@example.com", now)
	if err != nil {
		t.Fatalf("authorization header: %v", err)
	}
	if !strings.HasPrefix(got, "vapid t=") || !strings.Contains(got, ", k=") {
		t.Fatalf("the credential is not RFC 8292's `vapid t=..., k=...` shape: %s", got)
	}
	token := strings.TrimPrefix(strings.SplitN(got, ", k=", 2)[0], "vapid t=")
	keyParam := strings.SplitN(got, ", k=", 2)[1]

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the JWT does not have three segments: %s", token)
	}
	var hdr map[string]string
	if err := json.Unmarshal(mustDecode(t, parts[0]), &hdr); err != nil {
		t.Fatalf("jwt header: %v", err)
	}
	if hdr["typ"] != "JWT" || hdr["alg"] != "ES256" {
		t.Errorf(`header is %v, RFC 8292 requires {"typ":"JWT","alg":"ES256"}`, hdr)
	}

	var claims map[string]any
	if err := json.Unmarshal(mustDecode(t, parts[1]), &claims); err != nil {
		t.Fatalf("jwt claims: %v", err)
	}
	// `aud` IS THE ORIGIN, NOT THE ENDPOINT. Sending the full endpoint is the common mistake and it
	// leaks a capability into a token that transits differently from the body.
	if claims["aud"] != "https://push.example.net" {
		t.Errorf("aud = %v, want the origin alone", claims["aud"])
	}
	if claims["sub"] != "mailto:push@example.com" {
		t.Errorf("sub = %v", claims["sub"])
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatalf("exp is missing or not a number: %v", claims["exp"])
	}
	// RFC 8292 §2: *"An 'exp' claim MUST NOT be more than 24 hours from the time of the request."*
	if d := time.Unix(int64(exp), 0).Sub(now); d <= 0 || d > 24*time.Hour {
		t.Errorf("exp is %v from now; the RFC allows (0, 24h]", d)
	}

	// THE SIGNATURE VERIFIES UNDER THE ADVERTISED KEY — and is the fixed-width r||s pair rather than
	// ASN.1 DER. Both are valid ECDSA signatures and only one is a valid JWT, and a push service
	// rejects the other with an authentication error that says nothing about encoding.
	sig := mustDecode(t, parts[2])
	if len(sig) != 64 {
		t.Fatalf("the signature is %d octets, want 64 (r||s); DER would be ~70 and variable", len(sig))
	}
	pub := mustDecode(t, keyParam)
	if len(pub) != 65 || pub[0] != 0x04 {
		t.Fatalf("the k parameter is not an X9.62 uncompressed point: %d octets, first %#x", len(pub), pub[0])
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	x := new(big.Int).SetBytes(pub[1:33])
	y := new(big.Int).SetBytes(pub[33:])
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: k.private.Curve, X: x, Y: y}, digest[:],
		new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Errorf("the signature does not verify under the key the header advertises")
	}
}

// THE KEY SURVIVES THE APP DB ROUND TRIP. The ruling (quince#1128) puts this in a column, so what
// goes in must come back as the same public key — a mismatch would invalidate every subscription
// silently, which is the exact failure the ruling was taken to make impossible.
func TestAVAPIDKeyRoundTripsThroughItsStoredForm(t *testing.T) {
	k, err := GenerateVAPIDKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	stored := k.PrivateBytes()
	if len(stored) != 32 {
		t.Fatalf("stored form is %d octets, want a fixed 32 so a short read is a length error", len(stored))
	}
	back, err := VAPIDKeyFromBytes(stored)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if back.PublicKeyBase64() != k.PublicKeyBase64() {
		t.Errorf("the reloaded key has a different public half:\n got %s\nwant %s",
			back.PublicKeyBase64(), k.PublicKeyBase64())
	}
}

// A CORRUPT OR TRUNCATED STORED KEY IS REFUSED RATHER THAN COERCED. Under the ruling, "subscriptions
// exist but the key does not" means the DB was tampered with or partially restored — so this layer
// must not invent a usable key from bad bytes and let the caller look healthy.
func TestABadStoredKeyIsRefused(t *testing.T) {
	for name, in := range map[string][]byte{
		"empty":     {},
		"truncated": make([]byte, 31),
		"zero":      make([]byte, 32),
	} {
		if _, err := VAPIDKeyFromBytes(in); err == nil {
			t.Errorf("%s stored key was accepted", name)
		}
	}
}

// AN ENDPOINT NEVER REACHES AN ERROR STRING WHOLE. Errors are how a capability leaks into a log
// without anybody deciding it should — the log line is built from the error, and the error was built
// from the URL.
func TestErrorsRedactTheEndpointPath(t *testing.T) {
	k, err := GenerateVAPIDKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	const secret = "JzLQ3raZJfFBR0aqvOMsLrt54w4rJUsV"
	_, err = k.AuthorizationHeader("push.example.net/push/"+secret, "mailto:a@b", time.Now())
	if err == nil {
		t.Fatalf("a schemeless endpoint was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error carries the endpoint's secret path: %v", err)
	}
	if got := RedactEndpoint("https://push.example.net/push/" + secret); strings.Contains(got, secret) {
		t.Errorf("RedactEndpoint leaked the path: %s", got)
	}
}

// A MALFORMED SUBSCRIPTION IS A NAMED ERROR, not a panic. These arrive over the API from a browser,
// so every field is untrusted input.
func TestAMalformedSubscriptionIsRefusedByField(t *testing.T) {
	for name, sub := range map[string]Subscription{
		"p256dh not base64":  {P256DH: "!!!!", Auth: rfcAuthSecret},
		"p256dh not a point": {P256DH: b64.EncodeToString(make([]byte, 65)), Auth: rfcAuthSecret},
		"auth not base64":    {P256DH: rfcUAPublic, Auth: "!!!!"},
	} {
		if _, err := Encrypt(sub, []byte("x"), nil, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// THE DECLARATIVE ENVELOPE IS WHAT GOES ON THE WIRE (spec D2). `web_push: 8030` is the magic number
// WebKit defines; a payload without it is parsed by a service worker and by nothing else, which is
// the iOS 18.4+ fallback this rung deliberately keeps.
func TestThePayloadIsTheDeclarativeEnvelope(t *testing.T) {
	body, err := MarshalPayload(Notification{Title: "Backup available", Navigate: "/devices/abc", Kind: "backup_available"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.WebPush != 8030 {
		t.Errorf("web_push = %d, WebKit's declarative marker is 8030", got.WebPush)
	}
	if got.Notification.Navigate == "" {
		t.Errorf("navigate is empty — a notification with nowhere to go is a dead end")
	}
	// A NON-EMPTY TITLE IS REQUIRED by Declarative Web Push; without one the user agent drops the
	// whole message and nothing anywhere records that it did.
	if _, err := MarshalPayload(Notification{Navigate: "/devices/abc"}); err == nil {
		t.Errorf("a titleless notification was accepted")
	}
}
