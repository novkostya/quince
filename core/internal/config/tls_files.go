package config

import "github.com/novkostya/quince/core/internal/wire"

// SetTLSFiles writes `tls.cert_file` and `tls.key_file` and nothing else (quince#908 slice 5).
//
// A NARROW WRITE, for the reason `SetAllowInsecureTransport` gives beside it: the route that reaches
// this function is PRE-AUTH, so a full-document replace behind that door would let a stranger rewrite
// storage declarations and retention while pointing `tls.cert_file` somewhere. Splicing server-side
// exposes the two keys the ruling named and no others. The Operator's ruling of 2026-08-14 extended
// the pre-auth write to exactly this pair and said the line moves ONCE, explicitly — so a third key
// here is a decision rather than a small edit.
//
// # IT IS CALLED ONLY AFTER THE CERTIFICATE HAS PROVED ITSELF, WHICH IS WHY IT RETURNS NO "PREVIOUS"
//
// The first version of this function handed back the pair it displaced, so an unconfirmed apply could
// be undone. That shape wrote `config.yml` at the START of the ceremony and wrote it a SECOND time to
// undo — leaving a certificate that never worked visible in a hand-edited file for ten minutes
// (Operator, 2026-08-14, on quince#977).
//
// The trial now lives in `tlsx.Keeper`, which is what actually serves TLS and needs no file to do it.
// So this is the COMMIT: it runs when a request has arrived over the daemon's own https half carrying
// the apply's token, and there is nothing to revert because nothing was written until then. A
// displaced pair returned here would have no consumer.
//
// D12 IS THE POINT RATHER THAN A SIDE EFFECT: `config.yml` contains only what the user set, and a
// certificate somebody tried and abandoned was never something they set.
//
// PATH POLICY BELONGS TO THE ROUTE, NOT HERE. `Validate` checks the pair for the one thing knowable
// from the values alone — half a pair is a mistake — and deliberately checks nothing else, because a
// config that fails validation is DISCARDED and would take the daemon to plain http (validate.go).
// Absolute-path and readability checks live at `POST /api/onboarding/certificate`, where a refusal is
// an answer to the user rather than a document that will not load.
//
// BOTH PATHS EMPTY IS LEGAL, and it is how an authenticated admin turns TLS off. `TLSConfig.Enabled`
// is false for it, `Validate` accepts it, and the applier hands it to `Keeper.SetFiles`, which
// documents the empty pair as "turn TLS off".
func (s *Service) SetTLSFiles(certFile, keyFile, source string) ([]wire.ConfigError, []Warning, error) {
	// THE WHOLE READ-MODIFY-WRITE IS UNDER writeMu, as every other narrow write in this package
	// explains: read, modify and write are three steps, and two concurrent callers would otherwise
	// both read the same document and the second write would silently drop the first.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// REFUSED WHEN THE FILE ON DISK COULD NOT BE READ (quince#852), and this path wants it for the
	// reason `SetAllowInsecureTransport` states: `Current()` on a discarded document is `Default()`,
	// so writing through it would replace the operator's whole `config.yml` with defaults plus this
	// pair — an unauthenticated caller silently destroying every declaration in the file.
	if errs := s.refuseIfConfigDiscarded(); len(errs) > 0 {
		return errs, nil, nil
	}

	next := s.Current()
	next.TLS.CertFile, next.TLS.KeyFile = certFile, keyFile

	errs, warns, err := s.replaceLocked(next, source)
	if err != nil || len(errs) > 0 {
		return errs, nil, err
	}
	return nil, warns, nil
}
