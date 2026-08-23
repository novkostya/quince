import type { MessagesMessage } from "@/lib/types";

// MessageRow renders one message — qn.10 slice 7b, stories 2 and 8.
//
// ITS WHOLE JOB IS NOT INVENTING FACTS. A message record carries several states that all
// LOOK like "an empty bubble" and mean different things, and the rung's hard rule is that a
// surface must never render one as another:
//
//   body + no flags      an ordinary message.
//   body empty           legitimately empty — an attachment-only or system message.
//   body_unknown         the body could NOT be decoded. UNKNOWN, not empty. Rendering this
//                        as an empty bubble states that someone sent nothing, which nobody
//                        established.
//   retracted            unsent by the sender. The message existed and was withdrawn.
//   balloon              an app message whose payload quince does not decode. There IS
//                        content; quince cannot read it.
//
// Every one of those gets its own words below. Collapsing any two would satisfy *state
// honesty* — each sentence would be true — and fail *troubleshooting is actionable*, which
// names collapsing distinguishable causes as a defect even when every word is true.

// bodyText returns what to show, and whether it is the message's own words.
//
// THE BOOLEAN IS THE POINT: a caller must style quince's explanation differently from the
// user's text, or an explanation reads as something a person typed.
export function bodyText(m: MessagesMessage): { text: string; ownWords: boolean } {
  if (m.retracted) return { text: "This message was unsent.", ownWords: false };
  if (m.body_unknown) return { text: "quince could not read this message's text.", ownWords: false };
  if (m.body) return { text: m.body, ownWords: true };
  if (m.balloon) return { text: `An app message (${m.balloon}) quince cannot display.`, ownWords: false };
  if (m.attachments && m.attachments.length > 0) return { text: "", ownWords: true };
  // Reached by a system or group event with no text. Empty is the honest render: something
  // happened in the conversation and this row is its place.
  return { text: "", ownWords: true };
}

export function MessageRow({ message }: { message: MessagesMessage }) {
  const { text, ownWords } = bodyText(message);
  const attachments = message.attachments ?? [];

  return (
    <li className="flex flex-col gap-1 px-4 py-3">
      <div className="flex items-baseline gap-2">
        <span className="text-xs text-muted">
          {/* A SENT message has no handle, and that is not missing data — the counterpart
              lives on the chat. Saying "You" is what the record means. */}
          {message.from_me ? "You" : (message.handle ?? "Unknown sender")}
        </span>
        {message.edited && <span className="text-xs text-muted">edited</span>}
        {message.is_tapback && <span className="text-xs text-muted">reaction</span>}
      </div>

      {text !== "" && (
        <p className={ownWords ? "text-sm text-fg" : "text-sm text-muted italic"}>{text}</p>
      )}

      {attachments.map((a, i) => (
        <p key={`${message.id}-${i}`} className="text-xs text-muted">
          {a.present
            ? (a.name ?? "attachment")
            : // NO LINK, AND THE REASON. `present: false` means the backup does not hold the
              // bytes — not downloaded, purged, or iCloud-only. Offering a link that cannot
              // resolve would be worse than saying so.
              `${a.name ?? "attachment"} — not in this backup`}
        </p>
      ))}
    </li>
  );
}
