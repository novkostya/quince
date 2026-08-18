package pushsvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// THE `sub` CLAIM MUST BE ROUTABLE, AND THIS TEST EXISTS BECAUSE A PLAUSIBLE ONE WAS NOT.
//
// The default was `mailto:quince@localhost`, chosen on the argument that a mailbox nobody owns is
// more honest than inventing an address. Apple refused it: the first real delivery this project ever
// attempted, to an iPhone on 2026-08-18, came back `403`. RFC 8292 §2.2 says `sub` SHOULD be a
// `mailto:` or `https:` URI, and Apple enforces that the mailto have a real domain.
//
// ASSERTED AS A PROPERTY, NOT AS A STRING. Pinning the literal would pass for any value somebody
// typed, including the next unroutable one. What must hold is that it is a form a push service
// accepts.
func TestTheDefaultSubjectIsAFormAPushServiceAccepts(t *testing.T) {
	switch {
	case strings.HasPrefix(DefaultSubject, "https://"):
		host := strings.SplitN(strings.TrimPrefix(DefaultSubject, "https://"), "/", 2)[0]
		if !strings.Contains(host, ".") {
			t.Errorf("the https subject %q has no dotted host, so it is not routable", DefaultSubject)
		}
	case strings.HasPrefix(DefaultSubject, "mailto:"):
		domain := strings.TrimPrefix(DefaultSubject, "mailto:")
		if _, d, ok := strings.Cut(domain, "@"); !ok || !strings.Contains(d, ".") {
			// `localhost` is the measured failure — no dot, not a domain Apple will accept.
			t.Errorf("the mailto subject %q has no routable domain; Apple answers 403", DefaultSubject)
		}
	default:
		t.Errorf("subject %q is neither a mailto: nor an https: URI (RFC 8292 §2.2)", DefaultSubject)
	}
}

// AND IT REACHES THE WIRE. A correct constant that never gets signed into the token would fail
// against Apple exactly as the old one did, and every test above would still pass.
func TestTheSubjectReachesTheVAPIDToken(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client())

	if _, err := s.SendTest(context.Background()); err != nil {
		t.Fatalf("send test: %v", err)
	}
	if staged.got == nil {
		t.Fatal("nothing reached the push service")
	}
	if got := claimOf(t, staged.got.Header.Get("Authorization"), "sub"); got != DefaultSubject {
		t.Errorf("the token carries sub=%q, want %q", got, DefaultSubject)
	}
}

// AN OPERATOR'S OWN CONTACT WINS. Apple's requirement is that the claim be reachable; whose contact
// it is remains the operator's choice, and the default exists only because there must be one.
func TestAnOperatorSubjectOverridesTheDefault(t *testing.T) {
	staged := &stagedPush{status: http.StatusCreated}
	srv := staged.server(t)
	s, _ := senderWith(t, srv.URL+"/push/token")
	s = s.WithHTTPClient(srv.Client()).WithSubject("mailto:someone@example.com")

	if _, err := s.SendTest(context.Background()); err != nil {
		t.Fatalf("send test: %v", err)
	}
	if got := claimOf(t, staged.got.Header.Get("Authorization"), "sub"); got != "mailto:someone@example.com" {
		t.Errorf("the token carries sub=%q, want the operator's contact", got)
	}
}

// AN EMPTY OVERRIDE IS NOT AN OVERRIDE. An unset config key must not sign a token with `sub: ""`,
// which is the same class of refusal as the one this whole file is about.
func TestAnEmptySubjectLeavesTheDefaultStanding(t *testing.T) {
	s, _ := svc(t)
	if got := s.WithSubject("").subject; got != DefaultSubject {
		t.Errorf("an empty contact replaced the default with %q", got)
	}
}

// claimOf pulls one claim out of the `vapid t=<jwt>, k=<key>` credential.
func claimOf(t *testing.T, authorization, name string) string {
	t.Helper()
	_, rest, ok := strings.Cut(authorization, "t=")
	if !ok {
		t.Fatalf("Authorization has no token: %q", authorization)
	}
	token, _, _ := strings.Cut(rest, ",")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token is not three JWT segments: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	s, _ := claims[name].(string)
	return s
}
