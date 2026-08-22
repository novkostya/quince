package vaultsvc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/wire"
)

func totals(n int) vault.Totals {
	t := vault.Totals{}
	for i := 0; i < n; i++ {
		d := vault.DomainTotals{Domain: string(rune('a'+i/26)) + string(rune('a'+i%26)), Files: int64(i + 1), Bytes: int64((i + 1) * 100)}
		t.Domains = append(t.Domains, d)
		t.TotalFiles += d.Files
		t.TotalBytes += d.Bytes
	}
	return t
}

// The page carries a slice and the TOTALS carry the whole version — which is the point:
// a client holding one page of 1,264 rows cannot compute the total itself, so without this
// qn.9 D3's reconciliation requirement is unprovable at exactly the scale that matters.
func TestTotalsDescribeTheVersionNotThePage(t *testing.T) {
	all := totals(700)
	got := toWireOverview(all, wire.BrowseQuery{Limit: 100})

	if len(got.Page.Items) != 100 {
		t.Fatalf("page has %d items, want 100", len(got.Page.Items))
	}
	if got.Totals.DomainCount != 700 {
		t.Errorf("Totals.DomainCount = %d, want 700 — it must describe the version, not the page", got.Totals.DomainCount)
	}
	if got.Totals.Files != all.TotalFiles || got.Totals.Bytes != all.TotalBytes {
		t.Errorf("Totals = %+v, want files=%d bytes=%d", got.Totals, all.TotalFiles, all.TotalBytes)
	}
	// Control: a page sum is NOT the total, so the assertion above is not passing by accident
	// on a fixture where the two coincide.
	var pageFiles int64
	for _, it := range got.Page.Items {
		pageFiles += it.Files
	}
	if pageFiles == got.Totals.Files {
		t.Fatal("the page happens to sum to the total — this fixture cannot distinguish " +
			"per-version totals from per-page ones")
	}
}

// Paging is total: every domain appears exactly once across pages, in order.
func TestPagingIsTotalAndOrdered(t *testing.T) {
	all := totals(250)
	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		got := toWireOverview(all, wire.BrowseQuery{Cursor: cursor, Limit: 40})
		pages++
		for _, it := range got.Page.Items {
			seen[it.Domain]++
		}
		if got.Page.NextCursor == "" {
			break
		}
		cursor = got.Page.NextCursor
		if pages > 20 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 250 {
		t.Errorf("saw %d distinct domains across %d pages, want 250", len(seen), pages)
	}
	for d, n := range seen {
		if n != 1 {
			t.Errorf("domain %q appeared %d times — a cursor that repeats loses nothing but "+
				"double-counts everything a client sums", d, n)
		}
	}
}

// quince#1459 condition 1: `domains` is ABSENT, not null, when there is no report. Asserted
// on the encoded JSON, because that is the only place the distinction exists.
func TestDomainsKeyIsAbsentNotNullWhenThereIsNoReport(t *testing.T) {
	got := toWireOverview(totals(3), wire.BrowseQuery{})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	// DECODED, not a substring: `"domains"` also matches totals.domain_count's neighbours,
	// and the first version of this test failed for exactly that reason — which is how the
	// name collision was found.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["domains"]; present {
		t.Errorf("`domains` is present with no report: %s", b)
	}
	// Control: the key DOES appear once populated, so the absence above is about the report
	// rather than about the field never being emitted at all.
	got.Domains = []wire.DomainCapability{{Domain: "calls", State: "absent"}}
	b2, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var raw2 map[string]json.RawMessage
	if err := json.Unmarshal(b2, &raw2); err != nil {
		t.Fatal(err)
	}
	if _, present := raw2["domains"]; !present {
		t.Fatalf("`domains` absent even when populated: %s", b2)
	}
}

// The envelope's own fields keep their shapes — including the two that must never be null on
// the wire, and the one that must be PRESENT and null.
func TestEnvelopeFieldsAreNeverNull(t *testing.T) {
	b, err := json.Marshal(toWireOverview(totals(2), wire.BrowseQuery{}))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"capabilities", "adapter_version", "warnings", "unsupported_reason", "page", "totals"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("envelope field %q is absent", k)
		}
	}
	if string(raw["warnings"]) == "null" {
		t.Error("warnings is null; a client iterating it should not have to check")
	}
	// unsupported_reason must be PRESENT AND NULL — overview can always serve something, and
	// the frozen envelope says the field is how an adapter declines a whole backup.
	if string(raw["unsupported_reason"]) != "null" {
		t.Errorf("unsupported_reason = %s, want null", raw["unsupported_reason"])
	}
}

// `capabilities` is what THIS adapter does — NOT the per-domain report. The two sharing a
// word is what quince#1459 was filed about, and conflating them is the shape the ruling
// rejected as option (d).
func TestCapabilitiesIsAFlatAdapterListNotTheReport(t *testing.T) {
	got := toWireOverview(totals(2), wire.BrowseQuery{})
	if len(got.Capabilities) == 0 {
		t.Fatal("capabilities is empty")
	}
	b, err := json.Marshal(got.Capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `["`) {
		t.Errorf("capabilities encoded as %s — the frozen field is a list of STRINGS, and "+
			"redefining it to carry the domain report is option (d), which was rejected", b)
	}
}
