import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import type { MessagesChat, MessagesChats } from "@/lib/types";

// ChatList renders the conversations in an unlocked backup — qn.10 slice 7a, story 1.
//
// IT IS THE FIRST MESSAGES SCREEN AND IT COSTS NOTHING. The conversation list is answered
// live off the parser — 23 ms for 390 conversations on a real backup — and does NOT build the
// session projection, whose ~18 s scan is deferred until someone opens an actual conversation
// (qn.10 D2). So this screen must not grow anything that needs the projection: a message
// preview or an unread count would drag that cost onto the first thing a user sees.
//
// THREE STATES THAT MUST NOT COLLAPSE, which is this rung's recurring shape:
//
//   unsupported_reason set   quince cannot serve messages from this backup, and the sentence
//                            says which of the two causes it is. NOT an empty list.
//   no conversations         the backup is readable and genuinely holds none. A real case:
//                            one of the Operator's own devices has a valid, supported, EMPTY
//                            messages database.
//   conversations            the list.
//
// Rendering the first as the second would tell somebody they have no messages when nobody
// could look; rendering the second as the first would report a fault where there is none.

// nameFor is what to call a conversation.
//
// DISPLAY NAME IS OFTEN EMPTY, INCLUDING FOR GROUPS — it is a user-set title, and most group
// chats never get one. Falling back to the participants is what stops a blank row; falling
// back to the identifier after that is what stops an empty string when `handles` is absent
// from the schema. Every step is a real state, not defensive padding.
export function nameFor(chat: MessagesChat): string {
  if (chat.display_name) return chat.display_name;
  const people = chat.participants ?? [];
  if (people.length > 0) return people.join(", ");
  if (chat.identifier) return chat.identifier;
  // Reached when the schema's `handles` unit is absent AND there is no identifier. Naming the
  // conversation by its id is honest; inventing "Unknown contact" would read as a person.
  return `Conversation ${chat.id}`;
}

export function ChatList({ data, onOpen }: { data: MessagesChats; onOpen?: (chat: MessagesChat) => void }) {
  // `text-fg`, NOT `text-muted`, ON BOTH SENTENCES BELOW, AND THAT IS NOT A STYLE PREFERENCE.
  // Each is the ONLY content on its card — the answer to "where are my messages". There is
  // nothing for it to be secondary to, and rendering the answer as secondary text
  // de-emphasises the one thing the reader came for. `text-muted` is for an explanation
  // sitting BESIDE primary content, which is how DomainReport uses it.
  //
  // quince#1215's class exactly: the contrast floors are right and the ROLE was wrong. The
  // first version of this component had both muted.
  if (data.unsupported_reason) {
    // THE SENTENCE, NOT AN EMPTY LIST. It distinguishes "no readable Messages database" from
    // "a schema with no conversations", and those have different remedies.
    return (
      <Card className="p-6">
        <p className="text-sm text-fg">{data.unsupported_reason}</p>
      </Card>
    );
  }

  const chats = data.page.items;
  if (chats.length === 0) {
    return (
      <Card className="p-6">
        <p className="text-sm text-fg">This backup has no conversations in it.</p>
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
        {chats.map((chat) => {
          // The row's CONTENT is built once and wrapped twice, so the two shapes below cannot
          // drift apart — a group badge that appeared only in the clickable variant would be a
          // difference nobody would notice until a list rendered without a handler.
          const row = (
            <>
              <span className="min-w-0 flex-1 truncate text-sm text-fg">{nameFor(chat)}</span>
              {chat.is_group && (
                <Badge tone="neutral">
                  {/* The COUNT rather than the word "group": a reader wants to know how many
                      people are in it, and an absent participant list is why this is
                      conditional rather than always shown. */}
                  {chat.participants && chat.participants.length > 0
                    ? `${chat.participants.length} people`
                    : "Group"}
                </Badge>
              )}
            </>
          );
          return (
            <li key={chat.id}>
              {/* A BUTTON, NOT A LINK, AND `onOpen` IS OPTIONAL. Opening a conversation cannot
                  be a route of its own: the vault session lives in the page component, and a
                  separate route would ask for the password a second time (7c-2a). So the caller
                  selects within its own screen — and a list rendered WITHOUT a handler, as slice
                  7a shipped it, stays exactly as it was rather than becoming a dead control. */}
              {onOpen ? (
                <button
                  type="button"
                  onClick={() => onOpen(chat)}
                  className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-card focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                >
                  {row}
                </button>
              ) : (
                <div className="flex items-center gap-3 px-4 py-3">{row}</div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
