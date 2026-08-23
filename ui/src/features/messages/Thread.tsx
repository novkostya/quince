import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { MessageRow } from "./MessageRow";
import type { MessagesMessage, MessagesThread } from "@/lib/types";

// Thread renders one conversation — qn.10 slice 7c-2b, stories 2 and 10.
//
// ITS DOM IS BOUNDED BY PAGING, NOT BY A VIRTUALIZER, and that is a ruling rather than a shortcut
// (D9, narrowed 2026-08-23 on quince#1483). The route pages at 50 and caps at 200, so nothing
// renders that has not been fetched: the cursor guards against HOLDING the rows, where a
// virtualizer would guard against rendering rows already held. A second bound on the same axis is
// what a library would buy, and it costs a runtime dependency on a bundle already warning at
// 621.64 kB — on a project whose primary client is a phone over Wi-Fi.
//
// THE MEASUREMENT IS OWED, NOT WAIVED: render N rows, time interaction, record it in D9 before the
// rung closes. If a plausible scroll makes this bad, a virtualizer lands and the narrowing is
// spent. What was ruled is the ORDER — cheapest reversible thing first — so this component must
// stay easy to swap: it takes rows and renders them, and holds no scroll state of its own.
//
// NEWEST FIRST, and "load older" walks backwards, matching the cursor the route issues.

// indexingLabel is the wait state's sentence.
//
// INDETERMINATE IN ITS RENDERING WHILE CARRYING A LIVE COUNT, which is the distinction D3 turns on:
// the parser does not count rows before streaming them, so there is no total and any percentage
// would be invented. A count that climbs is what separates "working" from "hung" — and until the
// first `messages.indexing` frame arrives there is no count at all, which is a different sentence
// rather than a zero. Rendering `0 messages` would state a fact about the backup that nobody has
// established yet.
export function indexingLabel(count: number | undefined): string {
  if (count === undefined) return "Preparing this backup's messages…";
  return `Preparing this backup's messages — ${count.toLocaleString()} so far…`;
}

export function Thread({
  data,
  messages,
  indexing,
  onOlder,
  hasOlder,
  loadingOlder,
}: {
  data: MessagesThread | undefined;
  messages: MessagesMessage[];
  indexing: number | undefined;
  onOlder: () => void;
  hasOlder: boolean;
  loadingOlder: boolean;
}) {
  // THE WAIT STATE COMES FIRST because it is the one that lasts: the first conversation opened in
  // a session pays for the projection — ~18 s on a real backup — and the request BLOCKS for it.
  // At that duration a bare spinner is indistinguishable from a hang, which is why D2 makes this
  // report load-bearing rather than decorative.
  if (!data) {
    return (
      <Card className="p-6">
        <p className="text-sm text-fg" role="status" aria-live="polite">
          {indexingLabel(indexing)}
        </p>
        <p className="mt-1 text-xs text-muted">
          quince reads every message once per session. Later conversations open immediately.
        </p>
      </Card>
    );
  }

  if (data.unsupported_reason) {
    // THE SENTENCE, NOT AN EMPTY THREAD — the same rule ChatList follows. "quince cannot read
    // this" and "this conversation is empty" are different facts with different remedies.
    return (
      <Card className="p-6">
        <p className="text-sm text-fg">{data.unsupported_reason}</p>
      </Card>
    );
  }

  if (messages.length === 0) {
    return (
      <Card className="p-6">
        <p className="text-sm text-fg">There are no messages in this conversation.</p>
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

      {/* OLDER IS AT THE TOP, because the list is newest-first and that is the direction the
          cursor walks. A control rather than an automatic scroll trigger: an infinite scroller
          that fetches on approach is the shape whose bugs are invisible in component tests, and
          this rung already declined that trade once. */}
      {hasOlder && (
        <div className="flex justify-center">
          <Button variant="outline" onClick={onOlder} disabled={loadingOlder}>
            {loadingOlder ? "Loading older messages…" : "Load older messages"}
          </Button>
        </div>
      )}

      <ul className="divide-y divide-line rounded-md border border-line">
        {messages.map((m) => (
          <MessageRow key={m.id} message={m} />
        ))}
      </ul>
    </div>
  );
}
