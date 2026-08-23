import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { MessageRow, bodyText } from "./MessageRow";
import type { MessagesMessage } from "@/lib/types";

function msg(over: Partial<MessagesMessage> = {}): MessagesMessage {
  return { id: 1, guid: "g", time: "2026-08-23T00:00:00Z", from_me: false, ...over };
}

describe("bodyText", () => {
  // THE FOUR STATES THAT ALL LOOK LIKE AN EMPTY BUBBLE. Each must produce different words,
  // and the flag must say whether they are the user's.
  it("renders the message's own words as its own words", () => {
    expect(bodyText(msg({ body: "see you then" }))).toEqual({
      text: "see you then",
      ownWords: true,
    });
  });

  it("says an undecodable body is UNREAD, not empty", () => {
    const got = bodyText(msg({ body_unknown: true }));
    expect(got.text).toMatch(/could not read/);
    // The flag is what stops quince's explanation being styled as something a person typed.
    expect(got.ownWords).toBe(false);
  });

  it("says an unsent message was unsent", () => {
    const got = bodyText(msg({ retracted: true, body: "gone" }));
    expect(got.text).toMatch(/unsent/);
    expect(got.ownWords).toBe(false);
  });

  it("names the app for a message quince cannot display", () => {
    const got = bodyText(msg({ balloon: "com.apple.Passbook" }));
    expect(got.text).toMatch(/com\.apple\.Passbook/);
    expect(got.ownWords).toBe(false);
  });

  // An attachment-only message IS legitimately empty. It must not borrow the unknown wording.
  it("leaves an attachment-only message empty rather than explaining it", () => {
    expect(bodyText(msg({ attachments: [{ present: true, name: "x.heic" }] }))).toEqual({
      text: "",
      ownWords: true,
    });
  });

  // CONTROL: the four explanations must be DISTINCT strings, or two states have collapsed
  // into one sentence and every assertion above would still pass.
  it("gives each state its own words", () => {
    const texts = [
      bodyText(msg({ body_unknown: true })).text,
      bodyText(msg({ retracted: true })).text,
      bodyText(msg({ balloon: "com.example.app" })).text,
    ];
    expect(new Set(texts).size).toBe(3);
  });
});

describe("MessageRow", () => {
  it("names a sent message as You rather than as missing data", () => {
    render(<MessageRow message={msg({ from_me: true })} />);
    expect(screen.getByText("You")).toBeInTheDocument();
  });

  it("marks an edited message", () => {
    render(<MessageRow message={msg({ body: "hi", edited: true })} />);
    expect(screen.getByText("edited")).toBeInTheDocument();
  });

  // An attachment the backup does not hold gets NO link and says why.
  it("says an absent attachment is not in this backup", () => {
    render(
      <MessageRow message={msg({ attachments: [{ present: false, name: "gone.jpg" }] })} />,
    );
    expect(screen.getByText(/gone\.jpg — not in this backup/)).toBeInTheDocument();
    // CONTROL: a PRESENT attachment must not carry that wording, or the two are one state.
    render(<MessageRow message={msg({ attachments: [{ present: true, name: "here.jpg" }] })} />);
    expect(screen.getByText("here.jpg")).toBeInTheDocument();
  });
});
