package config

import "github.com/novkostya/quince/core/internal/wire"

// SetAllowInsecureTransport writes `sessions.allow_insecure_transport` and nothing else
// (quince#908 slice 6, Operator ruling 2026-08-14: *one setting, one route*).
//
// A NARROW WRITE RATHER THAN `PUT /api/config`, for the reason `AddStorage` and `ForgetStorage`
// already give and one this path makes sharper: it splices SERVER-SIDE, so it cannot drop a key the
// caller did not render. That is a correctness argument everywhere else in this package and a
// SECURITY argument here, because the route that reaches this function is PRE-AUTH. A full-document
// replace behind an unauthenticated door would let a stranger rewrite storage declarations, muxer
// settings and retention while turning on the plain-http opt-in. Exposing one boolean exposes one
// boolean.
//
// IT GOES BOTH WAYS. A control that can only be turned ON is a second dead end — you would relax the
// transport to finish setup and then need a shell on the box to put it back, which is the exact
// defect quince#912 named as *a remedy the user cannot follow*. quince#900 made the setting live in
// both directions, so `false` is applied without a restart like `true` is.
//
// THE CALLER OWNS THE `Configured()` GUARD, NOT THIS FUNCTION. Deliberate: this package knows
// nothing about credentials, and reaching into `auth` from here would invert the dependency and put
// the security bound two packages from the route it protects. `handleInsecureTransportSet` holds it,
// beside the exemption that makes it reachable, where a reader auditing the pre-auth surface finds
// both at once.
func (s *Service) SetAllowInsecureTransport(allow bool) ([]wire.ConfigError, []Warning, error) {
	// THE WHOLE READ-MODIFY-WRITE IS UNDER writeMu, as every other narrow write in this package
	// explains: read, modify and write are three steps, and two concurrent callers would otherwise
	// both read the same document and the second write would silently drop the first.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// REFUSED WHEN THE FILE ON DISK COULD NOT BE READ (quince#852), and this path wants it more than
	// the others do. `Current()` on a discarded document is `Default()`, so writing through it would
	// replace the operator's whole `config.yml` with defaults plus this one boolean — an
	// unauthenticated caller silently destroying every declaration in the file. The ruled guard
	// already exists; the point is that it must be here too.
	if errs := s.refuseIfConfigDiscarded(); len(errs) > 0 {
		return errs, nil, nil
	}

	next := s.Current()
	next.Sessions.AllowInsecureTransport = allow

	// One caller, so the route names itself — see AddStorage for the caveat (quince#967).
	errs, warns, err := s.replaceLocked(next, SourceInsecureTransport)
	if err != nil || len(errs) > 0 {
		return errs, nil, err
	}
	return nil, warns, nil
}
