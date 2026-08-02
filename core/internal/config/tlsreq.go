package config

import (
	"fmt"
	"io"
	"strings"
)

// TLSRequirement is the qn.6f startup gate: a config that ASKS for TLS and cannot get it
// stops the process rather than starting without it.
//
// WHY THIS IS NOT A Validate() ERROR — the same reason StorageRequirement exists, and the
// spec calls it this rung's load-bearing measurement. Load() DISCARDS a config that fails
// Validate and returns Default(), and Default() has NO TLS. So a certificate fault raised
// through Validate would start the daemon on defaults and serve PLAIN HTTP to a user who
// asked for HTTPS, behind a warning banner they cannot see — because they are not connected.
// That is the silent downgrade this rung exists to prevent, reached by putting the check in
// the obvious place.
//
// Validate still owns the half-set pair (`tls.cert_file` with no `tls.key_file`), because
// that is well-formedness and a PUT of it must be a 422 rather than a process exit. This
// answers the other question: may this process serve what the file asks for.
type TLSRequirement struct {
	// Configured is false when TLS is off — both keys empty. Not a fault: it is the correct
	// configuration for the reverse-proxy and `tailscale serve` tiers, and for --demo.
	Configured bool

	// CertFile and KeyFile are echoed back so the remedy can name the paths the operator
	// actually wrote, rather than the ones we hoped they wrote.
	CertFile string
	KeyFile  string

	// Unusable is the fault: the pair could not be loaded. Detail is the reason, already
	// phrased to name the file — tlsx.NewKeeper does that mapping, because the standard
	// library's own message for a mismatched pair contains no path at all.
	Unusable bool
	Detail   string
}

// OK reports whether the process may serve.
//
// TLS OFF IS OK. A refusal here would break every deployment behind a reverse proxy, which
// is the tier this rung recommends first.
func (r TLSRequirement) OK() bool { return !r.Unusable }

// CheckTLS decides whether the configured certificate can be served. load is the loader —
// tlsx.NewKeeper in production, a stub in tests — and is called ONLY when both keys are set,
// so a deployment with TLS off touches no filesystem.
func CheckTLS(c Config, load func(certFile, keyFile string) error) TLSRequirement {
	r := TLSRequirement{CertFile: c.TLS.CertFile, KeyFile: c.TLS.KeyFile}
	if !c.TLS.Enabled() {
		return r
	}
	r.Configured = true
	if err := load(c.TLS.CertFile, c.TLS.KeyFile); err != nil {
		r.Unusable, r.Detail = true, err.Error()
	}
	return r
}

// Explain prints the refusal and returns the error main() exits on.
//
// The idiom is StorageRequirement's, which is preflight's: name what was OBSERVED, say what
// follows from it, and print the exact thing to do. An error message is a claim.
func (r TLSRequirement) Explain(w io.Writer, configPath string) error {
	if r.OK() {
		return nil
	}
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, "quince: "+format+"\n", a...) }

	p("%s asks quince to serve TLS and the certificate cannot be used.", configPath)
	p("")
	p("    tls:")
	p("      cert_file: %s", r.CertFile)
	p("      key_file:  %s", r.KeyFile)
	p("")
	p("%s", r.Detail)
	p("")
	p("Check that both files exist, are readable by the user quince runs as, are PEM, and")
	p("that the key belongs to the certificate. A container needs them mounted — read-only")
	p("is expected and correct; quince never writes to this directory.")
	p("")
	p("To serve plain HTTP instead, clear BOTH keys. Leaving them set and unreadable is the")
	p("one thing quince will not do: it would mean serving http to somebody who asked for")
	p("https, and the browser would not tell them either.")
	p("")
	p("REFUSING to start. A quince that came up on plain HTTP here would look healthy while")
	p("serving the session cookie in clear to a user who had configured a certificate.")

	_, _ = io.WriteString(w, b.String())
	return fmt.Errorf("tls certificate in %s cannot be served", configPath)
}
