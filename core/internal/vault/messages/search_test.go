package messages_test

import (
	"errors"
	"testing"

	"github.com/novkostya/quince/core/internal/vault/messages"
	"github.com/novkostya/quince/core/internal/vault/messages/msgfixture"
)

// Searching finds a term that is present and does not invent one that is not.
//
// THE CONTROL IS THE POINT AND IT ALREADY EARNED ITS PLACE: the first version of this test
// searched for "invented", which appears only in the PADDED bodies and not in the default
// fixture at all — so the "absent term returns nothing" assertion was passing because the
// index had nothing matching either term. The control failed and the test was wrong, not the
// code.
func TestSearchFindsAPresentTermAndNotAnAbsentOne(t *testing.T) {
	r := reader(t, msgfixture.Spec{})

	present, err := r.Search(t.Context(), "reply", 20, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !present.Searchable {
		t.Fatal("Searchable is false — FTS5 should be available in the pinned build")
	}
	if len(present.Hits) == 0 {
		t.Fatal("control failed: a term that IS in the fixture found nothing, so the negative below proves nothing")
	}

	absent, err := r.Search(t.Context(), "zygomorphic", 20, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(absent.Hits) != 0 {
		t.Errorf("a term absent from the fixture returned %d hit(s)", len(absent.Hits))
	}
}

// A hit must carry its conversation, or a surface can show a message with no way to open the
// thread it came from.
func TestSearchHitsCarryTheirConversation(t *testing.T) {
	r := reader(t, msgfixture.Spec{Messages: 30})
	res, err := r.Search(t.Context(), "padding", 5, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("control failed: no hits, so conversation membership is untested")
	}
	for _, h := range res.Hits {
		if len(h.ChatIDs) == 0 {
			t.Errorf("hit %d carries no conversation", h.ID)
		}
	}
}

// An empty query is the CALLER's mistake and is said so, rather than being answered with an
// empty result that looks like "you have no messages containing that".
func TestEmptyQueryIsRefusedNotAnswered(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	for _, term := range []string{"", "   ", "\t"} {
		if _, err := r.Search(t.Context(), term, 10, nil); !errors.Is(err, messages.ErrEmptyQuery) {
			t.Errorf("Search(%q) err = %v, want ErrEmptyQuery", term, err)
		}
	}
}

// A term FTS5 cannot parse is the caller's too — an unbalanced quote is not a damaged backup.
func TestMalformedQueryIsItsOwnError(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	_, err := r.Search(t.Context(), `"unbalanced`, 10, nil)
	if err == nil {
		t.Skip("this FTS5 build accepts an unbalanced quote; nothing to assert")
	}
	if !errors.Is(err, messages.ErrBadQuery) {
		t.Errorf("err = %v, want ErrBadQuery", err)
	}
}

func TestSearchLimitClampIsDisclosed(t *testing.T) {
	r := reader(t, msgfixture.Spec{Messages: 40})
	res, err := r.Search(t.Context(), "invented", 100000, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !res.LimitClamped {
		t.Error("the limit was clamped and the result does not say so")
	}
}

// Searching must build the projection, exactly as opening a thread does — it is the other
// read that needs it.
func TestSearchBuildsTheProjection(t *testing.T) {
	r := reader(t, msgfixture.Spec{Messages: 20})
	built := 0
	if _, err := r.Search(t.Context(), "invented", 10, func(messages.Progress) { built++ }); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if built == 0 {
		t.Fatal("no progress fired — Search did not build the projection")
	}
	// Once only: a second search must not rebuild.
	before := built
	if _, err := r.Search(t.Context(), "invented", 10, func(messages.Progress) { built++ }); err != nil {
		t.Fatalf("second Search: %v", err)
	}
	if built != before {
		t.Errorf("the projection was rebuilt on the second search (%d then %d)", before, built)
	}
}
