package config

import (
	"errors"
	"strings"
	"testing"
)

// TLS OFF IS OK, and this is the row that matters most: a refusal here would break every
// deployment behind a reverse proxy, which is the tier this rung recommends first, plus
// --demo and every e2e run.
func TestCheckTLSOffIsOKAndNeverLoads(t *testing.T) {
	loaded := false
	req := CheckTLS(Default(), func(string, string) error { loaded = true; return nil })
	if !req.OK() {
		t.Fatal("TLS off must be OK — it is the reverse-proxy tier, not a fault")
	}
	if req.Configured {
		t.Error("Configured should be false when both keys are empty")
	}
	if loaded {
		t.Error("the loader ran with no certificate configured; a TLS-off deployment must touch no filesystem")
	}
}

func TestCheckTLSAcceptsAUsablePair(t *testing.T) {
	c := Default()
	c.TLS.CertFile, c.TLS.KeyFile = "/certs/q.pem", "/certs/q.key"
	var gotCert, gotKey string
	req := CheckTLS(c, func(certFile, keyFile string) error {
		gotCert, gotKey = certFile, keyFile
		return nil
	})
	if !req.OK() || !req.Configured {
		t.Fatalf("a usable pair must be OK and Configured, got %+v", req)
	}
	if gotCert != "/certs/q.pem" || gotKey != "/certs/q.key" {
		t.Errorf("loader got %q/%q, want the configured paths", gotCert, gotKey)
	}
}

// The whole reason this check exists on the serve path: a config that ASKS for TLS and cannot
// get it must stop the process, not fall back to Default() and serve plain http.
func TestCheckTLSRefusesAndExplains(t *testing.T) {
	c := Default()
	c.TLS.CertFile, c.TLS.KeyFile = "/certs/q.pem", "/certs/q.key"
	req := CheckTLS(c, func(string, string) error {
		return errors.New("/certs/q.pem and /certs/q.key were read but are not a usable certificate/key pair")
	})
	if req.OK() {
		t.Fatal("an unusable certificate was accepted")
	}

	var sb strings.Builder
	err := req.Explain(&sb, "/data/config.yml")
	if err == nil {
		t.Fatal("Explain returned nil for a refusal; main() would exit 0 and serve nothing")
	}
	out := sb.String()

	// The message is a claim, so it must name what was observed, both paths, and the remedy.
	for _, want := range []string{
		"/data/config.yml",
		"/certs/q.pem",
		"/certs/q.key",
		"not a usable certificate/key pair",
		"REFUSING to start",
		"clear BOTH keys",
		"read-only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, out)
		}
	}
}

// Explain on a healthy requirement must be a no-op returning nil, or a caller that always
// calls it would refuse to start every TLS-off deployment.
func TestExplainIsSilentWhenOK(t *testing.T) {
	var sb strings.Builder
	if err := (TLSRequirement{}).Explain(&sb, "/data/config.yml"); err != nil {
		t.Fatalf("Explain on an OK requirement returned %v", err)
	}
	if sb.Len() != 0 {
		t.Errorf("Explain wrote %q on an OK requirement", sb.String())
	}
}

// A half-set pair never reaches here: Enabled() is false, so CheckTLS reports TLS off and the
// process starts. That is correct and deliberate — Validate owns the half-set pair as a 422,
// because a PUT of one must not be a process exit. Pinned so nobody "fixes" it by refusing
// here and turning a config-form mistake into a crash loop.
func TestCheckTLSLeavesTheHalfSetPairToValidate(t *testing.T) {
	c := Default()
	c.TLS.CertFile = "/certs/q.pem" // no key

	req := CheckTLS(c, func(string, string) error {
		t.Fatal("the loader ran on a half-set pair; Enabled() should have been false")
		return nil
	})
	if !req.OK() {
		t.Error("a half-set pair must not be a startup refusal — Validate answers it with a 422")
	}

	errs := Validate(c)
	var named bool
	for _, e := range errs {
		if e.Path == "tls.key_file" {
			named = true
		}
	}
	if !named {
		t.Errorf("Validate did not name tls.key_file for a half-set pair: %+v", errs)
	}
}
