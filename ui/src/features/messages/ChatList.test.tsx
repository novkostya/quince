import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ChatList, nameFor } from "./ChatList";
import type { MessagesChats } from "@/lib/types";

function envelope(over: Partial<MessagesChats> = {}): MessagesChats {
  return {
    capabilities: ["threads", "attachments"],
    adapter_version: "messages-quince.v1",
    warnings: [],
    unsupported_reason: null,
    page: { items: [] },
    ...over,
  };
}

describe("nameFor", () => {
  it("prefers the user-set name", () => {
    expect(nameFor({ id: 1, guid: "g", is_group: true, display_name: "weekend plans" })).toBe(
      "weekend plans",
    );
  });

  // Most group chats have no display name. Falling back to the participants is what stops a
  // blank row on the most common group case.
  it("falls back to the participants when there is no name", () => {
    expect(
      nameFor({ id: 1, guid: "g", is_group: true, participants: ["+15550100001", "+15550100002"] }),
    ).toBe("+15550100001, +15550100002");
  });

  // participants is EMPTY when the schema's `handles` unit is absent — "not recorded", never
  // "a conversation with nobody".
  it("falls back to the identifier when participants are not recorded", () => {
    expect(nameFor({ id: 1, guid: "g", is_group: false, identifier: "+15550100003" })).toBe(
      "+15550100003",
    );
  });

  // Naming it by id is honest. "Unknown contact" would read as a person who is not there.
  it("names the conversation by id rather than inventing a person", () => {
    expect(nameFor({ id: 7, guid: "g", is_group: false })).toBe("Conversation 7");
  });
});

describe("ChatList", () => {
  it("lists conversations", () => {
    render(
      <ChatList
        data={envelope({
          page: {
            items: [
              { id: 1, guid: "a", is_group: false, identifier: "+15550100001" },
              { id: 2, guid: "b", is_group: true, display_name: "weekend plans" },
            ],
          },
        })}
      />,
    );
    expect(screen.getByText("+15550100001")).toBeInTheDocument();
    expect(screen.getByText("weekend plans")).toBeInTheDocument();
  });

  // THE THREE STATES THAT MUST NOT COLLAPSE. quince cannot read this backup's messages is a
  // fact about quince; this backup has no conversations is a fact about the user's data.
  it("shows the unsupported sentence rather than an empty list", () => {
    render(
      <ChatList
        data={envelope({
          unsupported_reason: "this backup has no Messages database quince can read",
        })}
      />,
    );
    expect(
      screen.getByText("this backup has no Messages database quince can read"),
    ).toBeInTheDocument();
    // CONTROL: it must not ALSO render the empty-list wording, or the two states are one.
    expect(screen.queryByText(/has no conversations in it/)).not.toBeInTheDocument();
  });

  it("says a readable backup genuinely holds none", () => {
    render(<ChatList data={envelope()} />);
    expect(screen.getByText(/has no conversations in it/)).toBeInTheDocument();
  });

  it("surfaces warnings rather than swallowing them", () => {
    render(
      <ChatList
        data={envelope({
          warnings: ["2 conversation(s) could not be read and are not listed"],
          page: { items: [{ id: 1, guid: "a", is_group: false, identifier: "+15550100001" }] },
        })}
      />,
    );
    expect(
      screen.getByText("2 conversation(s) could not be read and are not listed"),
    ).toBeInTheDocument();
  });

  // A group whose participants were not recorded still reads as a group — the badge degrades
  // to the word rather than claiming "0 people".
  it("does not claim zero people for a group with no recorded participants", () => {
    render(
      <ChatList
        data={envelope({
          page: { items: [{ id: 2, guid: "b", is_group: true, display_name: "weekend plans" }] },
        })}
      />,
    );
    expect(screen.getByText("Group")).toBeInTheDocument();
    expect(screen.queryByText("0 people")).not.toBeInTheDocument();
  });
});
