package main

import (
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// qn.13 D9's SECOND clause — `quince auth reset` must SAY on screen that it clears scoped
// credentials. The first clause, that it actually does, is `auth`'s `TestResetClearsScopedCredentials…`.
//
// THIS IS THE HALF THAT IS EASY TO LOSE. The behaviour is a `DELETE` with no `WHERE`, which nobody
// will accidentally narrow; the SENTENCE is one `Sprintf` that any later edit can flatten back to a
// total without anything failing. So it is asserted on rather than left to review.

func TestTheResetSummaryNamesTheSharedAccessItRemoved(t *testing.T) {
	got := resetSummary(auth.ResetResult{
		HadPassword: true, Passkeys: 3, ScopedPasskeys: 2, Sessions: 4,
	})

	if !strings.Contains(got, "3 passkey(s) removed") {
		t.Fatalf("summary lost the total: %q", got)
	}
	// THE ADMIN'S QUESTION IS WHO LOST ACCESS, not what the column is called — so the sentence is
	// asserted on its meaning rather than on the word "scoped".
	if !strings.Contains(got, "2 of them shared a single device with someone") {
		t.Fatalf("summary does not say that other people lost access: %q — an admin who ran this to "+
			"recover a lost phone learns it from a household member instead", got)
	}
	if !strings.Contains(got, "password cleared") || !strings.Contains(got, "4 session(s) invalidated") {
		t.Fatalf("summary dropped one of the counts it already had: %q", got)
	}
}

// SAID ONLY WHEN THERE WERE ANY. On an install that never shared a device the clause is noise, and a
// parenthetical about a feature nobody used is how a summary stops being read.
func TestTheResetSummaryStaysQuietWhenNothingWasShared(t *testing.T) {
	got := resetSummary(auth.ResetResult{
		HadPassword: false, Passkeys: 1, ScopedPasskeys: 0, Sessions: 0,
	})

	if strings.Contains(got, "shared a single device") {
		t.Fatalf("summary talks about shared access on an install that never had any: %q", got)
	}
	if !strings.Contains(got, "no password was set") || !strings.Contains(got, "1 passkey(s) removed") {
		t.Fatalf("summary lost what it always said: %q", got)
	}
}
