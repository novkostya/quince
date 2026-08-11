package auth

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// Every rpId below is fictional. A real domain is Operator-private under the privacy rule, and the
// gate does not catch one — so the fixture discipline written into the qn.6k spec's Rule check is
// the control here, not the gate.
const (
	rpHome  = "quince.example.com"
	rpOther = "quince.example.net"
)

func seedCredential(t *testing.T, st *store.Store, credID, rpID string) {
	t.Helper()
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: credID,
		PublicKey:    []byte("cose"),
		RPID:         rpID,
		Name:         "phone",
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
}

// THE POINT OF STORING rp_id AT ALL — spec D2. A credential presented on the wrong domain must
// produce a message naming the domain it belongs to, because the user's phone still lists it and
// "authentication failed" reads as "quince is broken".
func TestResolveCredentialNamesBothDomainsOnMismatch(t *testing.T) {
	st := newResetStore(t)
	seedCredential(t, st, "cred-1", rpHome)

	_, err := ResolveCredential(st, "cred-1", rpOther)

	var mm ErrRPIDMismatch
	if !errors.As(err, &mm) {
		t.Fatalf("got %v, want ErrRPIDMismatch", err)
	}
	if mm.Registered != rpHome || mm.Presented != rpOther {
		t.Errorf("got registered=%q presented=%q, want %q / %q", mm.Registered, mm.Presented, rpHome, rpOther)
	}
	// The message is the whole value of this error, so it is asserted rather than assumed to be
	// derived from the fields above.
	if !strings.Contains(err.Error(), rpHome) || !strings.Contains(err.Error(), rpOther) {
		t.Errorf("message names only one domain: %q", err.Error())
	}
}

// The matching case must simply work, or the guard above is a wall rather than a check.
func TestResolveCredentialAcceptsTheRegisteredDomain(t *testing.T) {
	st := newResetStore(t)
	seedCredential(t, st, "cred-1", rpHome)

	pk, err := ResolveCredential(st, "cred-1", rpHome)
	if err != nil {
		t.Fatalf("ResolveCredential on the registered domain: %v", err)
	}
	if pk.CredentialID != "cred-1" || pk.RPID != rpHome {
		t.Errorf("got %+v, want cred-1 on %s", pk, rpHome)
	}
}

// EXISTENCE IS CHECKED BEFORE rpId, and that ordering is a security property rather than a style
// choice: if the mismatch fired first, an unknown credential id would be distinguishable from a
// known-but-foreign one, and the mismatch message — which names a domain — would leak for
// credentials this quince does not hold.
func TestResolveCredentialHidesUnknownIDsBehindTheSameError(t *testing.T) {
	st := newResetStore(t)
	seedCredential(t, st, "cred-1", rpHome)

	_, err := ResolveCredential(st, "cred-does-not-exist", rpOther)
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("got %v, want ErrNoCredential", err)
	}
	var mm ErrRPIDMismatch
	if errors.As(err, &mm) {
		t.Error("an unknown credential produced an rpId mismatch — the message would name a domain " +
			"for a credential this quince does not hold")
	}
}

// The rpId is the HOST, never the authority. A credential registered behind one port must work
// behind another under the same name — that is WebAuthn's rule, and it is also the difference
// between quince's own TLS tier (:443, or any port) and a reverse proxy in front of it.
func TestRPIDFromRequestDropsThePortAndTheRootDot(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"quince.example.com", "quince.example.com"},
		{"quince.example.com:8443", "quince.example.com"},
		{"quince.example.com:443", "quince.example.com"},
		// A fully-qualified name with the root label spelled out is the same domain.
		{"quince.example.com.", "quince.example.com"},
		// An IP is returned unchanged: it cannot be an rpId, and refusing that tier is the
		// registration surface's job (story 4), not this function's.
		{"192.0.2.10:8080", "192.0.2.10"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = tc.host
		if got := RPIDFromRequest(r); got != tc.want {
			t.Errorf("host %q → %q, want %q", tc.host, got, tc.want)
		}
	}
}
