package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// G4b's OTHER HALF — qn.6n. `reauth/finish` must issue no session and set no cookie.
//
// The spec asks for it on the response *"because the endpoint it is modelled on does issue one, and
// inheriting that would silently make this a second login path"*. Issuing one would also bind the
// proof to a session id that is gone by the time the mutating call arrives — two failures pointing
// in opposite directions, both of which would read as a bug in the binding rather than in the
// endpoint.
//
// TWO TESTS, BECAUSE ONE CANNOT REACH THE CASE THAT MATTERS. The interesting branch is the SUCCESS
// path, and reaching it needs a verified assertion, which needs a real authenticator — G7/G8's
// territory, declared unrun. So:
//
//	over HTTP   → proves no cookie on every branch CI can actually reach
//	over the AST → proves no `SetCookie` call exists in the file AT ALL, success path included
//
// The second is the one that catches the copy-paste the spec singled out, and it is deliberately
// structural: a comment saying "no cookie is set here" asserts an ABSENCE, and this project's own
// finding three PRs ago was that *a comment asserting parity guarantees none* (quince#909).

func reauthDeps(t *testing.T) Deps {
	t.Helper()
	d := testDeps(t)
	// The passkey and reauth routes register only when the ceremony stores are non-nil, so a test
	// router without these does not serve the endpoint under test at all — it would 404 and the
	// assertion below would pass vacuously.
	d.Passkeys = newPasskeyCeremoniesForTest()
	d.Reauth = newReauthCeremoniesForTest()
	d.Proofs = newProofsForTest()
	return d
}

func TestReauthFinishSetsNoCookieOnAnyReachableBranch(t *testing.T) {
	srv := httptest.NewServer(NewRouter(reauthDeps(t)))
	defer srv.Close()

	for _, c := range []struct {
		name string
		path string
	}{
		{"no ceremony parameter", "/api/auth/reauth/finish"},
		{"an unknown ceremony", "/api/auth/reauth/finish?ceremony=no-such-key"},
	} {
		req, err := http.NewRequest("POST", srv.URL+c.path, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		_ = resp.Body.Close()

		// THE ROUTE MUST EXIST, or this test proves nothing. A 404 here means the router did not
		// register the pair and every cookie assertion below is vacuous.
		if resp.StatusCode == http.StatusNotFound {
			t.Fatalf("%s: 404 — the reauth route is not registered, so this test is vacuous", c.name)
		}
		if cookies := resp.Cookies(); len(cookies) != 0 {
			t.Errorf("%s: response set %d cookie(s) — reauth mints no session", c.name, len(cookies))
		}
	}
}

// THE SUCCESS PATH, REACHED THE ONLY WAY CI CAN: by reading the source rather than running it.
//
// Unusual, and it is the honest instrument for this claim. The property is *"this file never calls
// SetCookie"*, which is exactly what an AST can decide and exactly what no reachable request can —
// the branch that would carry the call is the one needing an authenticator.
//
// It also fails in the right place. Somebody adding `http.SetCookie` by analogy with
// `handlePasskeyLoginFinish` gets a named test failure rather than a passing suite and a silent
// second login path.
func TestNoReauthFileCallsSetCookie(t *testing.T) {
	for _, file := range []string{"handlers_reauth.go", "../auth/reauth.go"} {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// `http.SetCookie(...)`, and any `.SetCookie(...)` — the second catches a helper that
			// wraps it, which is how this would arrive if it arrived at all.
			if sel.Sel.Name == "SetCookie" {
				t.Errorf("%s:%d calls SetCookie — reauth issues no session, and a cookie here would "+
					"make it a second login path", file, fset.Position(call.Pos()).Line)
			}
			return true
		})
	}
}

// AND THE RESPONSE BODY CARRIES NOTHING SESSION-SHAPED. `passkeys/login/finish` answers
// {state, csrf_token}; adding either here would be the same defect arriving through the body rather
// than through a header, and the cookie tests above would not see it.
func TestTheReauthResponseCarriesOnlyAProof(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "../wire/objects.go", nil, 0)
	if err != nil {
		t.Fatalf("parse wire: %v", err)
	}
	var fields []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "ReauthFinish" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})
	if len(fields) != 1 || fields[0] != "Proof" {
		t.Fatalf("wire.ReauthFinish has fields %v, want exactly [Proof] — anything else is a "+
			"session-shaped response arriving through the body", fields)
	}
}
