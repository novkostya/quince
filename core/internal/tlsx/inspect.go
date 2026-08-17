package tlsx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Inspection outcomes. FROZEN in the sense `StorageProbe.Outcome` is: a client renders different
// prose and a different next action for each, so adding one is a contract change.
const (
	OutcomeUsable      = "usable"     // the pair loads, matches, is in date and covers the name
	OutcomeUnreadable  = "unreadable" // absent, or the process cannot read it
	OutcomeMalformed   = "malformed"  // not PEM, or not a certificate/key
	OutcomeMismatched  = "mismatched" // both parse; the key does not belong to the certificate
	OutcomeNotYetValid = "not_yet_valid"
	OutcomeExpired     = "expired"
	OutcomeWrongHost   = "wrong_host" // valid, but does not cover the name they typed
)

// Report is what an offline inspection can say about a candidate pair, WITHOUT touching the network.
//
// IT MIRRORS `StorageProbe` DELIBERATELY, because it answers the same kind of question — *what is
// this thing I am about to declare?* — and that object already settled the shape: every refusal is
// carried IN the answer rather than as an HTTP status, because "that certificate is expired" is the
// ANSWER to the question asked, not a failure to answer it.
//
// `Reason` ALWAYS NAMES THE FILE OR THE HOST, for quince#514's reason: quince knows which path and
// which name, and a client composing its own sentence from an enum cannot.
type Report struct {
	Outcome string
	Reason  string

	// Names is every DNS name and IP the leaf covers. It is populated even on `wrong_host` — ESPECIALLY
	// then, because "does not cover quince.example" is a status and "covers quince.lan, not
	// quince.example" is something a person can act on.
	Names []string

	// NotBefore/NotAfter are RFC3339 UTC, empty when the leaf never parsed. A UI shows them for
	// `usable` too: a certificate that expires in nine days is not a refusal and is worth seeing.
	NotBefore string
	NotAfter  string

	// ChainLength is how many certificates the file held. ONE IS NOT AN ERROR AND IS OFTEN A PROBLEM:
	// a leaf without its intermediate validates on a machine that happens to cache the issuer and
	// fails on a phone that does not, which is the single hardest TLS failure for a person to
	// diagnose. It is reported rather than judged, because whether it matters depends on the issuer.
	ChainLength int

	// CoversCurrentHost answers the SECOND coverage question: not *does it cover the name they
	// typed* — that is `Outcome` — but *does it cover the address this request arrived at*. They are
	// different questions and the second one is the one nobody was asking.
	//
	// IT NEVER CHANGES THE OUTCOME, and that separation is the whole design. A pair that loads,
	// matches and is in date IS usable; whether it covers the address the caller happens to be
	// standing on is a fact about the caller, and a certificate reached by IP is a legitimate
	// install — self-signed, or a LAN with no names — that this product must not refuse. Reported,
	// so a client can warn. Never promoted to a refusal.
	CoversCurrentHost bool
}

// Inspect reads a candidate certificate pair and reports what it is. NO NETWORK, EVER — it answers
// what a machine can know from two files, and the reachability half belongs to the browser
// (quince#908 §5).
//
// THIS IS THE CHECK NOTHING ELSE PERFORMS BEFORE THE PAIR GOES LIVE. `validateTLS` is explicit that
// it checks "well-formedness and nothing else" — both keys set, or neither — and says so at its own
// definition. `CheckTLS` catches the rest, but only at STARTUP and only for the pair already written
// to `config.yml`. Between typing a path and restarting, nothing tells a user their key belongs to a
// different certificate.
//
// `now` IS INJECTED so the validity window is testable without waiting for a certificate to expire.
//
// `currentHost` IS THE ADDRESS IN PLAY AND IS NOT THE SAME ARGUMENT AS `hostname`. `hostname` is the
// name the user typed — the one they are moving TO — and it decides `wrong_host`. `currentHost` is
// where the caller is standing, or where a confirmation link is about to point, and it decides only
// `CoversCurrentHost`. Pass it empty to ask nothing.
func Inspect(certFile, keyFile, hostname, currentHost string, now time.Time) Report {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// BOTH FILES ARE NAMED, AND THE FIRST VERSION PASSED THE LIBRARY'S MESSAGE THROUGH VERBATIM.
		// Its own test caught that: `tls: failed to find any PEM data in certificate input` contains
		// no path at all, so the reason broke the rule this file states three lines above it — the
		// one `Keeper.classify` already exists because of.
		//
		// BOTH RATHER THAN THE GUILTY ONE, deliberately. Working out which file failed means matching
		// on the library's wording, which is the fragile classification `classifyLoad` is already
		// forced into once; naming the pair and letting the library's own sentence say `certificate
		// input` or `key input` disambiguates without a second guess.
		return Report{
			Outcome: classifyLoad(err),
			Reason:  fmt.Sprintf("%s with %s: %v", certFile, keyFile, err),
		}
	}

	// THE LEAF IS PARSED AGAIN RATHER THAN READ FROM `pair.Leaf`. `LoadX509KeyPair` leaves `Leaf` nil
	// on every Go version this project has run on — it is documented as populated only by
	// `X509KeyPair` in newer releases and the zero value is easy to mistake for "no certificate".
	// Parsing the first DER block is what the TLS stack itself does at handshake time.
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return Report{
			Outcome: OutcomeMalformed,
			Reason:  fmt.Sprintf("%s parsed as PEM but its first certificate is not usable: %v", certFile, err),
		}
	}

	r := Report{
		Names:       leafNames(leaf),
		NotBefore:   leaf.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:    leaf.NotAfter.UTC().Format(time.RFC3339),
		ChainLength: len(pair.Certificate),
	}

	// ANSWERED HERE, BEFORE THE DATE AND NAME BRANCHES RETURN, so it is reported on every outcome
	// that got as far as a leaf — including `expired` and `wrong_host`. Those are exactly the cases
	// where a user is deciding what to type next, and "the address you are on is not covered either"
	// is part of that picture.
	//
	// `VerifyHostname` RATHER THAN A STRING COMPARE against `Names`, because wildcard matching is
	// RFC 6125 and a hand-rolled `*.` check gets `a.b.example` wrong. The library already does it,
	// and it is the same call the browser will make.
	if currentHost != "" {
		r.CoversCurrentHost = leaf.VerifyHostname(currentHost) == nil
	}

	// DATES BEFORE THE HOSTNAME, deliberately. An expired certificate that also covers the wrong name
	// has two problems, and the one to report is the one that makes it unusable for EVERY name.
	switch {
	case now.Before(leaf.NotBefore):
		r.Outcome = OutcomeNotYetValid
		r.Reason = fmt.Sprintf("%s is not valid until %s — check the clock on this machine and on whatever issued it",
			certFile, r.NotBefore)
		return r
	case now.After(leaf.NotAfter):
		r.Outcome = OutcomeExpired
		r.Reason = fmt.Sprintf("%s expired on %s", certFile, r.NotAfter)
		return r
	}

	// AN EMPTY HOSTNAME IS NOT A FAILURE. The field starts empty by ruling (quince#908 §5 — do not
	// pre-fill it from the Host header, that is the name they are leaving), so a user who checks the
	// pair before deciding on a name gets everything except the coverage answer.
	if hostname != "" {
		if err := leaf.VerifyHostname(hostname); err != nil {
			r.Outcome = OutcomeWrongHost
			r.Reason = fmt.Sprintf("%s does not cover %s — it covers %s",
				certFile, hostname, strings.Join(r.Names, ", "))
			return r
		}
	}

	r.Outcome = OutcomeUsable
	r.Reason = fmt.Sprintf("%s and %s load, match, and are valid until %s", certFile, keyFile, r.NotAfter)
	return r
}

// classifyLoad separates "I could not read this" from "I read it and it is wrong", because the
// remedies share nothing: one is a path or a permission, the other is the file's contents.
//
// MISMATCH IS DETECTED BY THE MESSAGE, and that is worth flagging rather than hiding. The standard
// library returns a plain `errors.New("tls: private key does not match public key")` with no
// sentinel to compare against, so there is nothing typed to match on. If a Go release rewords it this
// degrades to `malformed` — wrong, but not silently wrong: the reason string is the library's own and
// still says what happened.
func classifyLoad(err error) string {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return OutcomeUnreadable
	}
	if strings.Contains(err.Error(), "does not match") {
		return OutcomeMismatched
	}
	return OutcomeMalformed
}

// leafNames flattens the SANs a client will actually match against. The legacy CN is deliberately
// NOT included: no browser has honoured it since 2017, so listing it would show a name that does not
// work and send somebody to debug DNS for a certificate that was never going to match.
func leafNames(leaf *x509.Certificate) []string {
	names := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	names = append(names, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	return names
}
