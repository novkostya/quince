import * as React from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { BackLink } from "@/components/BackLink";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { APIError, api, messageFor } from "@/lib/api";
import { useDialogRoute } from "@/lib/useDialogRoute";
import type { MessagesChats, MessagesThread, Session, Version } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { UnlockDialog } from "@/features/vault/UnlockDialog";
import { ChatList, nameFor } from "@/features/messages/ChatList";
import { Thread } from "@/features/messages/Thread";
import { useMessagesIndexingStore } from "@/stores/messagesIndexing";

// MessagesPage is the Messages surface of one backup — qn.10 slice 7c-2a, story 1.
//
// A PAGE WITH ITS OWN SESSION, which is this codebase's settled shape rather than a choice made
// here: VaultBrowsePage and VersionOverviewPage each hold their own `useState<Session>` and their
// own unlock dialog, because "the password is never persisted — unlock is per-session, always"
// (contracts §1). A shared session store would be the thing that makes a reload NOT ask again,
// which is the property those two exist to keep.
//
// ROUTED ON THE VERSION, not on the session, for the reason both of them already give: a session
// id dies at its TTL, so a link carrying one is stale within minutes where a version id is durable.
//
// THIS SCREEN COSTS NOTHING TO OPEN, AND THAT IS THE POINT OF D2. The conversation list is answered
// live off the parser — 23 ms for 390 conversations — and builds no projection. The ~18 s scan
// belongs to opening a conversation (7c-2b), which is why nothing here previews a message or counts
// unread ones. Do not add either: it would drag that cost onto the first thing a user sees.

// chatName titles the open conversation, reusing the list's own naming rule.
//
// `nameFor` RATHER THAN A SECOND RULE, so the header and the row a reader just clicked cannot
// disagree — display name, else participants, else identifier, else the id. A conversation that
// is not in the loaded list falls back to a neutral title rather than inventing a name: the
// thread route answers by id, so a hand-edited `?chat=` can legitimately open something the
// list does not carry.
function chatName(data: MessagesChats | undefined, chatID: number): string {
  const chat = data?.page.items.find((c) => c.id === chatID);
  return chat ? nameFor(chat) : "Conversation";
}
export function MessagesPage() {
  const { id = "" } = useParams();

  // The cold-deep-link fallback every version-routed page uses: the store is empty until the WS
  // connects, and there is no `GET /api/versions/{id}` — the collection route is the one that
  // exists.
  const fromStore = useVersionsStore((s) => s.byId[id]);
  const all = useQuery({
    queryKey: ["versions"],
    queryFn: () => api.get<{ versions: Version[] }>("/api/versions"),
    enabled: !fromStore && id !== "",
  });
  const version = fromStore ?? all.data?.versions.find((v) => v.id === id);
  const deviceName = useDevicesStore((s) => (version ? s.byUdid[version.udid]?.name : undefined));

  const queryClient = useQueryClient();
  const [session, setSession] = React.useState<Session | null>(null);
  const [lockError, setLockError] = React.useState<string | null>(null);
  const { open, onOpenChange } = useDialogRoute("unlock");
  const sessionID = session?.id ?? "";

  // STORY 6 — NOTHING READ FROM THE BACKUP SURVIVES A LOCK, and `staleTime` alone does not
  // achieve that: on a lock the query merely goes inactive and react-query holds its data for the
  // default gcTime. Conversation names are the correspondents' names, so this is exactly the
  // content a lock is supposed to remove. The key carries the session id, so nothing wrong is ever
  // DISPLAYED; the removal below is what makes sure nothing is HELD.
  const dropChats = React.useCallback(
    (sid: string) => {
      if (sid !== "") queryClient.removeQueries({ queryKey: ["messages-chats", sid] });
    },
    [queryClient],
  );

  const chats = useQuery({
    queryKey: ["messages-chats", sessionID],
    enabled: sessionID !== "",
    queryFn: () => api.get<MessagesChats>(`/api/sessions/${sessionID}/messages/chats`),
    // NOT PAGINATED, by the route's own contract — the list is bounded by how many people
    // someone has talked to, not by how much they have said. So one query, no infinite scroll.
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: false,
  });

  // THE SELECTED CONVERSATION LIVES IN THE URL, NOT IN COMPONENT STATE, so Back leaves the thread
  // rather than the page — which is what a reader who pressed it expects. It is a SEARCH PARAM
  // rather than a route segment for the reason 7c-2a gives: a route of its own would mount a
  // different component, and the vault session lives here, so it would ask for the password again.
  //
  // A RELOAD LANDS ON THE LOCKED PANEL, and that is the design rather than lost state: the session
  // did not survive, so neither should the view of its contents.
  const [params, setParams] = useSearchParams();
  const chatParam = params.get("chat");
  // `=== ""` AS WELL AS null: `Number("")` is 0 and `Number.isSafeInteger(0)` is true, so an empty
  // `?chat=` would open conversation 0 rather than none — the server answers honestly, but the
  // claim here is that a hand-edited param is no selection, and for the empty case it was not
  // (quince#1520 review).
  const chatID = chatParam === null || chatParam === "" ? null : Number(chatParam);
  // A non-numeric ?chat= is somebody's edit, not a conversation. Treated as none rather than sent
  // to the server as NaN, which the route would answer as a bad request about a page marker.
  const openChat = chatID !== null && Number.isSafeInteger(chatID) ? chatID : null;

  const dropThread = React.useCallback(
    (sid: string) => {
      if (sid !== "") queryClient.removeQueries({ queryKey: ["messages-thread", sid] });
    },
    [queryClient],
  );

  const thread = useInfiniteQuery({
    queryKey: ["messages-thread", sessionID, openChat],
    enabled: sessionID !== "" && openChat !== null,
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      const p = new URLSearchParams();
      if (pageParam) p.set("cursor", String(pageParam));
      const q = p.toString();
      return api.get<MessagesThread>(
        `/api/sessions/${sessionID}/messages/chats/${openChat}/messages${q ? `?${q}` : ""}`,
      );
    },
    getNextPageParam: (last) => last.page.next_cursor || undefined,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: false,
  });

  // THE LIVE COUNT FROM `messages.indexing` (quince#1515). Read only while the first page is in
  // flight — the scan happens once per session, so every later conversation resolves immediately
  // and this is never shown again.
  const indexing = useMessagesIndexingStore((s) => (sessionID === "" ? undefined : s.bySession[sessionID]));

  // The envelope fields describe the CONVERSATION rather than the page, so they come from the
  // first page; the items are concatenated in the order the cursor walked them, newest first.
  const threadPages = thread.data?.pages;
  const threadMessages = React.useMemo(
    () => (threadPages ? threadPages.flatMap((p) => p.page.items) : []),
    [threadPages],
  );

  // A 409 `locked` means the session ended without anybody locking it — the TTL, or a daemon
  // restart. Same treatment as an explicit lock: what it produced is just as dead.
  //
  // THE FACT IS HELD IN STATE, NOT DERIVED FROM THE QUERY, AND THAT IS WHAT MAKES THE BANNER
  // REACHABLE AT ALL. Deriving it reads correct and cannot work: clearing the session changes
  // this query's key, so the next render is observing a DIFFERENT query — enabled: false, no
  // error — and the derived flag falls back to false in the same tick that reveals the panel
  // the banner lives in. The user is returned to a locked screen with no explanation, which is
  // precisely the collapse *troubleshooting is actionable* forbids.
  //
  // MEASURED, NOT REASONED: the same derived shape in VersionOverviewPage is unreachable today
  // and no test covers it (quince#1516).
  const [expired, setExpired] = React.useState(false);
  // EITHER REQUEST CAN BE THE ONE THAT DISCOVERS THE SESSION IS GONE, and in a thread it is the
  // LIKELY one: that is where a reader dwells while the TTL runs out. Watching only `chats` reads
  // correct and cannot work — that query has `staleTime: Infinity` and never refetches, so once it
  // has succeeded its error stays undefined for the life of the page, and the 409 lands on
  // `thread` instead.
  //
  // THE SOCKET DOES NOT COVER THIS EITHER. `session.locked` reaches `useSessionStore`, but this
  // page holds its session in local state by design (7c-2a), and nothing joins the two. A request
  // 409 is the only detection route here, so it has to watch every request that can carry one.
  //
  // WHAT IT COSTS TO MISS: the session is never cleared, `dropChats`/`dropThread` never run, and
  // pressing "All conversations" renders the cached list — correspondent names on screen, from a
  // session that no longer exists. That is story 6, which is why this is not a tidiness concern
  // (quince#1520 review).
  const lockedErr = (e: unknown) => e instanceof APIError && e.code === "locked";
  const sessionGone = lockedErr(chats.error) || lockedErr(thread.error);
  React.useEffect(() => {
    if (sessionGone) {
      setExpired(true);
      dropChats(sessionID);
      dropThread(sessionID);
      setSession(null);
    }
  }, [sessionGone, sessionID, dropChats, dropThread]);

  async function lock() {
    const sid = sessionID;
    setLockError(null);
    try {
      await api.post(`/api/sessions/${sid}/lock`, {});
      setSession(null);
      dropChats(sid);
      dropThread(sid);
    } catch (err) {
      // `lock` is idempotent and an unknown id answers 204, so anything reaching here is a real
      // refusal and keeps the session on screen rather than pretending it closed.
      setLockError(messageFor(err, "Could not lock this backup."));
    }
  }

  // AN UNRESOLVED VERSION IS THREE STATES, NOT ONE, AND THE UNLOCK BUTTON MUST NOT RENDER FOR ANY
  // OF THEM (quince#1518). The control is what opens `UnlockDialog`, and the dialog needs the
  // version; rendering the button without one gives a screen whose only control writes a route and
  // paints nothing — no error, no spinner, no sentence. On a cold deep link that is a short window,
  // but for a deleted version or a failed list fetch it is permanent.
  //
  // THE THIRD STATE IS THE ONE THAT LIES. A failed fetch is neither "loading" nor "not in the
  // list": quince did not check, it failed to read, and the id in the address bar may be perfectly
  // good — so reporting it as absent sends the reader hunting a typo they did not make. The
  // server's own sentence is shown rather than replaced, because "the storage subsystem is not
  // answering" is knowledge this client cannot reconstruct.
  //
  // COPIED FROM VaultBrowsePage, INCLUDING THE REASON, which got this right via review finding
  // quince#1410. Two later pages inherited the shape without the guard.
  if (!version) {
    return (
      <div className="flex flex-col gap-4">
        <BackLink to="/">Home</BackLink>
        <Card>
          <p className="text-sm text-muted">
            {all.isPending ? (
              "Loading…"
            ) : all.error ? (
              <span role="alert" className="text-danger">
                {messageFor(all.error, "Could not read this quince's version list.")}
              </span>
            ) : (
              "That backup is not in this quince's version list."
            )}
          </p>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <BackLink to={`/versions/${id}`}>{deviceName ?? "Back"}</BackLink>

      <div>
        <h2 className="text-base font-medium text-fg">Messages</h2>
        <p className="mt-1 text-xs text-muted">
          The conversations in this backup, most recent first.
        </p>
      </div>

      {sessionID === "" ? (
        <Card>
          <div className="flex items-center justify-between gap-4">
            <div>
              <h3 className="text-sm font-medium text-fg">Conversations</h3>
              {/* NAMES THE COST AND WHAT IS GAINED, as the version page does. A control
                  labelled only "Unlock" makes the user find out by clicking. */}
              <p className="mt-1 text-xs text-muted">
                Reading messages needs the backup password.
              </p>
            </div>
            <Button onClick={() => onOpenChange(true)}>Unlock</Button>
          </div>
          {expired ? (
            // BOTH CAUSES, because quince cannot tell them apart and naming one would be a
            // guess. Same wording the other two session pages use.
            <p className="mt-2 text-sm text-warn">
              That session ended — either it timed out, or quince restarted. Unlocking again is
              all that is needed.
            </p>
          ) : null}
        </Card>
      ) : chats.isPending ? (
        <Card>
          <p className="text-sm text-muted">Reading this backup&rsquo;s conversations…</p>
        </Card>
      ) : chats.isError && !sessionGone ? (
        <Card>
          {/* THE SERVER'S OWN MESSAGE — but NOT for a session that has gone. `expired` is
              state set in an effect, so it is still false on the render that first sees the 409;
              without `!sessionGone` this arm paints `session not found or expired` in danger red
              for one frame before the warn banner replaces it. `sessionGone`, not `expired`, is
              the flag that is true in that render (quince#1517 review). VaultBrowsePage carries
              the same guard.

              NOT COVERED BY A TEST, AND THAT IS DECLARED RATHER THAN ASSUMED. A one-frame
              flicker is not observable through React Testing Library's settled-DOM queries:
              both sentences live in mutually exclusive arms of this ternary, so by the time
              `findByText` resolves on the banner, the error node is already gone and
              `queryByText` sees only the current DOM. MEASURED — the test that claimed to
              check this passed with the guard REMOVED (997/997), so it was asserting the end
              state, which was always right. It was deleted rather than kept under a name that
              promised more than it could check (quince#1519 review).

              "could not read this backup's Messages domain" and a
              session that has gone are different facts with different remedies. */}
          <p className="text-sm text-danger">
            {messageFor(chats.error, "These conversations could not be read.")}
          </p>
        </Card>
      ) : chats.data ? (
        <>
          {openChat === null ? (
            <ChatList
              data={chats.data}
              onOpen={(chat) => {
                // `replace: false` — opening a conversation is a navigation, so Back returns to
                // the list. Threading it through the URL is what makes that work at all.
                setParams({ chat: String(chat.id) });
              }}
            />
          ) : (
            <>
              <div className="flex items-center justify-between gap-4">
                <h3 className="min-w-0 truncate text-sm font-medium text-fg">
                  {chatName(chats.data, openChat)}
                </h3>
                <Button variant="outline" onClick={() => setParams({})}>
                  All conversations
                </Button>
              </div>
              {thread.isError && !sessionGone ? (
                <Card>
                  {/* THE SERVER'S OWN SENTENCE. A bad page marker and an unreadable backup are
                      different remedies — reload versus this backup is damaged — and the route
                      keeps them apart, so the surface must not collapse them. */}
                  <p className="text-sm text-danger">
                    {messageFor(thread.error, "This conversation could not be read.")}
                  </p>
                </Card>
              ) : (
                <Thread
                  data={thread.data?.pages[0]}
                  sessionID={sessionID}
                  messages={threadMessages}
                  indexing={indexing}
                  onOlder={() => void thread.fetchNextPage()}
                  hasOlder={thread.hasNextPage}
                  loadingOlder={thread.isFetchingNextPage}
                />
              )}
            </>
          )}
          <Card>
            <div className="flex items-center justify-between gap-4">
              <p className="text-xs text-muted">
                This backup is open. Locking it removes everything read from it.
              </p>
              <Button variant="outline" onClick={() => void lock()}>
                Lock
              </Button>
            </div>
            {lockError ? <p className="mt-2 text-sm text-danger">{lockError}</p> : null}
          </Card>
        </>
      ) : (
        // THE HANDOVER FRAME: the 409 has landed, so the error arm above is suppressed, and the
        // effect that clears the session has not run yet — one render with no data and no
        // session-gone panel. Nothing is drawn on purpose. A sentence here would appear for a
        // single frame and be replaced by the banner that actually explains it, which is a
        // flicker saying something quince cannot stand behind.
        null
      )}

      {/* NO `version ?` GATE, and its absence is the fix rather than a tidy-up. The early return
          above guarantees a version here, so gating again would restore the exact mismatch
          quince#1518 is about: a control rendered under one condition opening a dialog rendered
          under a stricter one. One guard, at the top, for both. */}
      <UnlockDialog
        version={version}
        deviceName={deviceName}
        open={open}
        onOpenChange={onOpenChange}
        onUnlocked={(s) => {
          // The banner describes the LAST session, so a new one clears it. Left standing it
          // would explain a problem the user has just fixed.
          setExpired(false);
          setSession(s);
        }}
      />
    </div>
  );
}
