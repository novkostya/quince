package config

import "github.com/novkostya/quince/core/internal/wire"

// SetTLSFiles writes `tls.cert_file` and `tls.key_file` and nothing else, and hands back the pair it
// displaced (quince#908 slice 5).
//
// A NARROW WRITE, for the reason `SetAllowInsecureTransport` gives beside it: the route that reaches
// this function is PRE-AUTH, so a full-document replace behind that door would let a stranger rewrite
// storage declarations and retention while pointing `tls.cert_file` somewhere. Splicing server-side
// exposes the two keys the ruling named and no others. The Operator's ruling of 2026-08-14 extended
// the pre-auth write to exactly this pair and said the line moves ONCE, explicitly — so a third key
// here is a decision rather than a small edit.
//
// # THE DISPLACED PAIR IS RETURNED BECAUSE THE REVERT MUST NOT RE-READ IT
//
// An apply that is never confirmed is undone, and the undo has to restore what THIS call displaced.
// A caller that read `Current().TLS` before calling would be reading outside `writeMu`, so a second
// write landing in between would make it revert to a pair that was already gone — and on the path
// that matters, a first-run user retrying an apply, that is not a hypothetical ordering. The pair is
// captured under the same lock that replaces it, which is the only place the two facts are known
// together.
//
// IT IS RETURNED ON FAILURE TOO, and that is the safer of the two options rather than an oversight.
// The value means *what `config.yml` held when this call took the lock*, which is true whether or not
// the write went on to succeed. So a caller who arms a revert against a write that was refused
// restores the pair that is still there — a no-op — where a zero value would have turned that
// mistake into "TLS off". The misuse is unlikely; making it harmless costs nothing.
//
// PATH POLICY BELONGS TO THE ROUTE, NOT HERE. `Validate` checks the pair for the one thing knowable
// from the values alone — half a pair is a mistake — and deliberately checks nothing else, because a
// config that fails validation is DISCARDED and would take the daemon to plain http (validate.go).
// Absolute-path and readability checks live at `POST /api/onboarding/certificate`, where a refusal is
// an answer to the user rather than a document that will not load.
//
// BOTH PATHS EMPTY IS LEGAL AND IS HOW THE REVERT UNDOES A FIRST-RUN APPLY. `TLSConfig.Enabled` is
// false for it, `Validate` accepts it, and the applier hands it to `Keeper.SetFiles`, which documents
// the empty pair as "turn TLS off". A first-run user had no certificate to go back to, so that is
// precisely the pair this returns to them.
func (s *Service) SetTLSFiles(certFile, keyFile, source string) (TLSConfig, []wire.ConfigError, []Warning, error) {
	// THE WHOLE READ-MODIFY-WRITE IS UNDER writeMu, as every other narrow write in this package
	// explains: read, modify and write are three steps, and two concurrent callers would otherwise
	// both read the same document and the second write would silently drop the first. Here it also
	// buys the guarantee above — that the returned pair is the one this write displaced.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// READ BEFORE THE GUARD so that `prev` means one thing on every return path — the live
	// document's pair at the moment this call took the lock. The guard below still precedes every
	// WRITE, which is what it is for; reading the snapshot ahead of it changes nothing it protects.
	next := s.Current()
	prev := next.TLS

	// REFUSED WHEN THE FILE ON DISK COULD NOT BE READ (quince#852), and this path wants it for the
	// reason `SetAllowInsecureTransport` states: `Current()` on a discarded document is `Default()`,
	// so writing through it would replace the operator's whole `config.yml` with defaults plus this
	// pair — an unauthenticated caller silently destroying every declaration in the file.
	if errs := s.refuseIfConfigDiscarded(); len(errs) > 0 {
		return prev, errs, nil, nil
	}

	next.TLS.CertFile, next.TLS.KeyFile = certFile, keyFile

	errs, warns, err := s.replaceLocked(next, source)
	if err != nil || len(errs) > 0 {
		return prev, errs, nil, err
	}
	return prev, nil, warns, nil
}
