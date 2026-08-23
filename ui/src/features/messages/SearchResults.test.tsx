import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { SearchResults, searchable } from "./SearchResults";
import type { MessagesSearch } from "@/lib/types";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).

function res(over: Partial<MessagesSearch> = {}): MessagesSearch {
  return {
    capabilities: ["threads", "attachments", "search"],
    adapter_version: "messages-quince.v1",
    warnings: [],
    unsupported_reason: null,
    page: { items: [] },
    ...over,
  };
}

function hit(body: string) {
  return {
    id: 1, guid: "M1", time: "2026-08-20T10:00:00Z", from_me: false, handle: "A",
    body, body_unknown: false, is_tapback: false, edited: false, retracted: false,
    chat_ids: [7],
  };
}

describe("searchable", () => {
  it("is the capability, not the presence of results", () => {
    // AN EMPTY RESULT SET FROM A REAL INDEX IS STILL SEARCHABLE. Deriving this from
    // `items.length` is the bug this helper exists to make impossible.
    expect(searchable(res({ page: { items: [] } }))).toBe(true);
    expect(searchable(res({ capabilities: ["threads", "attachments"] }))).toBe(false);
    // THE DISCRIMINATING CASE, and without it this test does not pin the property at all:
    // results present, capability absent. Deriving `searchable` from `items.length` passes both
    // assertions above and fails only here — measured, by breaking it exactly that way.
    //
    // The capability is the source of truth because the two facts are independent: an index that
    // exists can return nothing, and results without the capability would mean the server
    // contradicted itself — which this must not paper over by trusting the rows.
    expect(searchable(res({ capabilities: ["threads"], page: { items: [hit("x")] } }))).toBe(false);
  });
});

describe("SearchResults", () => {
  // THE FOUR OUTCOMES THAT ALL LOOK LIKE "NOTHING FOUND".
  it("says quince cannot read the messages at all when the domain is unsupported", () => {
    render(<SearchResults term="hello" data={res({ unsupported_reason: "this backup has no Messages database quince can read" })} />);
    expect(screen.getByText(/no Messages database quince can read/i)).toBeTruthy();
    expect(screen.queryByText(/no messages match/i)).toBeNull();
  });

  it("says the INDEX is missing rather than that nothing matched, and gives the reason", () => {
    render(
      <SearchResults
        term="hello"
        data={res({ capabilities: ["threads", "attachments"], warnings: ["FTS5 is not available in this build"] })}
      />,
    );
    // A FACT ABOUT QUINCE, NOT ABOUT THE MESSAGES. Reporting this as "no results" would tell
    // somebody they never wrote a word they may well have written.
    expect(screen.getByText(/could not build a search index/i)).toBeTruthy();
    expect(screen.getByText(/FTS5 is not available/i)).toBeTruthy();
    expect(screen.queryByText(/no messages match/i)).toBeNull();
  });

  it("says nothing matched when the index is real and empty-handed, quoting the term", () => {
    render(<SearchResults term="xyzzy" data={res()} />);
    // THE TERM IS QUOTED BACK so a typo is visible without retyping it.
    expect(screen.getByText(/no messages match/i).textContent).toMatch(/xyzzy/);
    expect(screen.queryByText(/could not build a search index/i)).toBeNull();
  });

  it("renders the hits, and surfaces warnings alongside them", () => {
    render(
      <SearchResults term="hello" data={res({ warnings: ["some messages could not be placed"], page: { items: [hit("hello there")] } })} />,
    );
    expect(screen.getByText("hello there")).toBeTruthy();
    expect(screen.getByText(/could not be placed/i)).toBeTruthy();
  });
});
