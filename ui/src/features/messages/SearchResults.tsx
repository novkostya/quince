import { Card } from "@/components/ui/card";
import { MessageRow } from "./MessageRow";
import type { MessagesSearch } from "@/lib/types";

// SearchResults renders what a search found — qn.10 slice 7e, story 6.
//
// FOUR OUTCOMES THAT ALL LOOK LIKE "NO RESULTS", and keeping them apart is the whole job:
//
//   unsupported_reason   quince cannot read this backup's messages at all.
//   no `search` capability   the full-text index was NOT built — FTS5 unavailable, or the build
//                        failed. `warnings` says which. This is a fact about quince.
//   zero hits            the index exists and holds nothing matching. A fact about the messages.
//   hits                 the results.
//
// Reporting the second as the third tells somebody they never wrote a word they may well have
// written; reporting the third as the second blames quince for a search that worked.

// searchable reports whether this answer came from a real index.
export function searchable(data: MessagesSearch): boolean {
  return data.capabilities.includes("search");
}

export function SearchResults({ data, term }: { data: MessagesSearch; term: string }) {
  if (data.unsupported_reason) {
    return (
      <Card className="p-6">
        <p className="text-sm text-fg">{data.unsupported_reason}</p>
      </Card>
    );
  }

  if (!searchable(data)) {
    // NOT "no results". The index is missing, so nothing was searched — and `warnings` carries
    // the reason, which is the actionable half (D4).
    return (
      <Card className="p-6">
        <p className="text-sm text-fg">
          quince could not build a search index for this backup, so its messages cannot be
          searched.
        </p>
        {data.warnings.map((w) => (
          <p key={w} className="mt-1 text-xs text-muted">
            {w}
          </p>
        ))}
      </Card>
    );
  }

  const hits = data.page.items;
  if (hits.length === 0) {
    return (
      <Card className="p-6">
        {/* THE TERM IS QUOTED BACK, so a typo is visible without retyping it. */}
        <p className="text-sm text-fg">No messages match &ldquo;{term}&rdquo;.</p>

        {/* THE WARNINGS BELONG HERE MORE THAN ANYWHERE ELSE, and dropping them was the defect
            (quince#1522 review). They are the PROJECTION BUILD's, carried on the reader, so they
            are independent of hit count — and one of them is
            `"N message(s) could not be read and are missing from this view"`. Without it this
            screen answers a search over an INCOMPLETE index with a COMPLETE negative: a definite
            claim about somebody's data with the one fact that qualifies it suppressed, on the
            only screen where it changes the meaning.

            Surfacing them when there ARE hits and swallowing them when there are none is exactly
            backwards, and it is the harm this file's own header names — telling somebody they
            never wrote a word they may well have written.

            `text-warn` RATHER THAN `text-muted`: beside a list of results a warning is a footnote,
            beside a negative it QUALIFIES THE ANSWER, and muted styling would rank it below the
            sentence it is correcting.

            THE NEGATIVE ITSELF IS NOT REWORDED, deliberately. The warning already states the
            qualification in the server's own words; a second, softer sentence composed here would
            be quince paraphrasing a fact it was handed, and free to drift from it. */}
        {data.warnings.map((w) => (
          <p key={w} className="mt-2 text-sm text-warn">
            {w}
          </p>
        ))}
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      {data.warnings.map((w) => (
        <p key={w} className="text-sm text-warn">
          {w}
        </p>
      ))}
      <ul className="divide-y divide-line rounded-md border border-line">
        {hits.map((h) => (
          // NO sessionID, SO ATTACHMENTS RENDER AS NAMES RATHER THAN LINKS. A result is a
          // pointer into a conversation, and opening it is where the file lives; a download link
          // in a search result would work but it is not what this list is for.
          <MessageRow key={h.id} message={h} />
        ))}
      </ul>
    </div>
  );
}
