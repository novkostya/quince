import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Thread, indexingLabel } from "./Thread";
import type { MessagesMessage, MessagesThread } from "@/lib/types";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).

function env(over: Partial<MessagesThread> = {}): MessagesThread {
  return {
    capabilities: ["threads", "attachments"],
    adapter_version: "messages.v1",
    warnings: [],
    unsupported_reason: null,
    page: { items: [] },
    ...over,
  };
}

function msg(over: Partial<MessagesMessage> = {}): MessagesMessage {
  return {
    id: 1, guid: "M1", time: "2026-08-20T10:00:00Z", from_me: false,
    handle: "A", body: "hello", body_unknown: false, is_tapback: false,
    edited: false, retracted: false,
    ...over,
  };
}

describe("indexingLabel", () => {
  // NO COUNT IS A DIFFERENT SENTENCE FROM ZERO, and that is the claim rather than a nicety.
  // Before the first messages.indexing frame arrives quince has counted nothing; rendering
  // "0 messages" would state a fact about the backup that nobody has established.
  it("says nothing about a count it does not have", () => {
    expect(indexingLabel(undefined)).toBe("Preparing this backup's messages…");
    expect(indexingLabel(undefined)).not.toMatch(/0/);
  });

  it("carries the live count once one arrives, grouped for reading", () => {
    expect(indexingLabel(254949)).toMatch(/254,949 so far/);
  });
});

describe("Thread", () => {
  // STORY 10 — the ~18 s wait is the state this component exists to render honestly. At that
  // duration a bare spinner is indistinguishable from a hang.
  it("reports the scan while the first page is in flight, as a live status", () => {
    render(<Thread data={undefined} messages={[]} indexing={40000} onOlder={() => {}} hasOlder={false} loadingOlder={false} />);

    const status = screen.getByRole("status");
    expect(status.textContent).toMatch(/40,000 so far/);
    // ANNOUNCED, because the screen is otherwise still and a reader using a screen reader has no
    // other signal that anything is happening.
    expect(status.getAttribute("aria-live")).toBe("polite");
    // NO PERCENTAGE ANYWHERE. There is no total to compute one from.
    expect(status.textContent).not.toMatch(/%/);
  });

  it("says quince cannot read the domain rather than showing an empty conversation", () => {
    render(
      <Thread
        data={env({ unsupported_reason: "this backup has no Messages database quince can read" })}
        messages={[]} indexing={undefined} onOlder={() => {}} hasOlder={false} loadingOlder={false}
      />,
    );
    expect(screen.getByText(/no Messages database quince can read/i)).toBeTruthy();
    expect(screen.queryByText(/no messages in this conversation/i)).toBeNull();
  });

  it("distinguishes a genuinely empty conversation from an unreadable one", () => {
    render(<Thread data={env()} messages={[]} indexing={undefined} onOlder={() => {}} hasOlder={false} loadingOlder={false} />);
    expect(screen.getByText(/no messages in this conversation/i)).toBeTruthy();
  });

  it("renders the messages it was handed and offers older ones only when there are more", () => {
    const onOlder = vi.fn();
    const { rerender } = render(
      <Thread data={env()} messages={[msg({ id: 1, body: "newest" }), msg({ id: 2, body: "older" })]}
        indexing={undefined} onOlder={onOlder} hasOlder={false} loadingOlder={false} />,
    );
    expect(screen.getByText("newest")).toBeTruthy();
    // NO CONTROL WHEN THERE IS NOTHING BEHIND IT — a "load older" that fetches nothing is a
    // silent dead end, the same defect class as an Unlock button with no dialog (quince#1518).
    expect(screen.queryByRole("button", { name: /older/i })).toBeNull();

    rerender(
      <Thread data={env()} messages={[msg({ id: 1, body: "newest" })]}
        indexing={undefined} onOlder={onOlder} hasOlder={true} loadingOlder={false} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /load older messages/i }));
    expect(onOlder).toHaveBeenCalledTimes(1);
  });

  it("disables the control while a page is loading, and says which state it is in", () => {
    render(<Thread data={env()} messages={[msg()]} indexing={undefined} onOlder={() => {}} hasOlder={true} loadingOlder={true} />);
    const b = screen.getByRole("button", { name: /loading older messages/i });
    expect((b as HTMLButtonElement).disabled).toBe(true);
  });

  it("surfaces the envelope's warnings rather than swallowing them", () => {
    render(
      <Thread data={env({ warnings: ["some messages could not be placed in a conversation"] })}
        messages={[msg()]} indexing={undefined} onOlder={() => {}} hasOlder={false} loadingOlder={false} />,
    );
    expect(screen.getByText(/could not be placed/i)).toBeTruthy();
  });
});
