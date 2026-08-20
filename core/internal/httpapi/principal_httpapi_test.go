package httpapi

import (
	"context"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// THE ACCESSOR MUST FAIL CLOSED, which is the whole reason it returns a bool.
//
// An exempt route runs with no principal. If a caller drops the second value it gets a zero
// Principal — which reads as a password login, which reads as the admin. That is spec D6's defect
// shape arriving through the accessor rather than through a credential predicate, so it is asserted
// here before any capability check exists to be fooled by it.
func TestPrincipalFromIsAbsentOnAnUnauthenticatedContext(t *testing.T) {
	if p, ok := PrincipalFrom(context.Background()); ok {
		t.Fatalf("a bare context yielded a principal: %+v", p)
	}
}

func TestPrincipalRoundTripsThroughTheContext(t *testing.T) {
	want := auth.Principal{CredentialID: "Y3JlZC14eXo"}
	got, ok := PrincipalFrom(withPrincipal(context.Background(), want))
	if !ok {
		t.Fatal("principal did not survive the context")
	}
	if got != want {
		t.Fatalf("principal changed: got %+v want %+v", got, want)
	}
}

// A password login binds a principal that IS present and IS a password login. Absent and
// password-login are different states and must not collapse — the first means nobody is
// authenticated, the second means the admin is.
func TestPasswordLoginPrincipalIsPresentAndEmpty(t *testing.T) {
	got, ok := PrincipalFrom(withPrincipal(context.Background(), auth.Principal{}))
	if !ok {
		t.Fatal("a password-login principal read as absent")
	}
	if !got.IsPasswordLogin() {
		t.Fatalf("expected a password login, got %+v", got)
	}
}
