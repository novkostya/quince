package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// probeNonceTTL bounds how long a minted nonce answers.
//
// TWO MINUTES, AND THE RULING LEFT THIS TO THE SPEC — so this comment is where it is chosen and why
// (Operator ruling 2026-08-14, quince#908's CORS ruling, amended on quince#939).
//
// It is sized for a HUMAN, not for a round trip: the caller types a hostname, waits for DNS and a
// TLS handshake against a name that may not resolve, reads the answer, and may try a second name.
// `challengeTTL`'s scale is the right neighbourhood; a few seconds would turn a slow resolver into a
// failure that looks like a misconfiguration, which is the exact confusion the probe exists to end.
const probeNonceTTL = 2 * time.Minute

// probeNonceMax caps how many live nonces one daemon holds.
//
// THE MINT IS PRE-AUTH AND UNAUTHENTICATED, so without a cap it is an unbounded allocation any
// visitor can drive. The cap is generous against real use — a probe needs one — and the eviction is
// the oldest, so a flood degrades into "the flooder's own nonces expire early" rather than into a
// refusal that would break the legitimate caller standing behind it.
const probeNonceMax = 64

// probeNonces holds live probe nonces.
//
// MULTI-USE WITHIN THE TTL, WHICH IS THE ONE PLACE THIS DIVERGES FROM `ReauthCeremonies`, and the
// ruling flagged it as a real decision rather than a detail: *"it is NOT obviously single-use here,
// because one probe may legitimately try more than one name, and a challenge spent on the first
// attempt would make the second look like a failure."*
//
// THE DIFFERENCE IS WHAT THE TOKEN IS WORTH. A reauth challenge is worth a PROOF, and a proof
// authorises a mutation — so it is spent on first use, because one that survives a failed attempt can
// be replayed against a second. This one is worth an ANSWER to a question the holder could already
// ask this daemon same-origin: *did you echo my nonce, and what did you see on this connection?* A
// replay buys the replayer nothing they could not obtain by asking directly, which is the same
// argument the ruling makes for the endpoint disclosing `detected` at all.
//
// SO THE TTL IS THE WHOLE BOUND, and it is short for that reason rather than by habit.
type probeNonces struct {
	mu  sync.Mutex
	now func() time.Time
	in  map[string]time.Time // nonce → expiry
}

func newProbeNonces() *probeNonces {
	return &probeNonces{now: time.Now, in: map[string]time.Time{}}
}

// mint returns a fresh nonce, sweeping expired ones first.
func (p *probeNonces) mint() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for k, exp := range p.in {
		if now.After(exp) {
			delete(p.in, k)
		}
	}
	// EVICT THE OLDEST rather than refusing the new one. A refusal would let a flooder deny the mint
	// to everybody else, which is worse than letting the flooder's own oldest nonce die early.
	for len(p.in) >= probeNonceMax {
		var oldestKey string
		var oldestExp time.Time
		for k, exp := range p.in {
			if oldestKey == "" || exp.Before(oldestExp) {
				oldestKey, oldestExp = k, exp
			}
		}
		delete(p.in, oldestKey)
	}
	p.in[nonce] = now.Add(probeNonceTTL)
	return nonce, nil
}

// valid reports whether this daemon minted the nonce and it has not expired. It does NOT consume it
// — see the type comment for why this is the one respect in which it differs from a ceremony.
//
// AN EXPIRED NONCE IS DELETED ON THE WAY OUT, so a probe that keeps presenting a dead token does not
// keep it alive in the map.
func (p *probeNonces) valid(nonce string) bool {
	if nonce == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	exp, ok := p.in[nonce]
	if !ok {
		return false
	}
	if p.now().After(exp) {
		delete(p.in, nonce)
		return false
	}
	return true
}
