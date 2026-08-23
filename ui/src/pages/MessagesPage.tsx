import * as React from "react";
import { useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { BackLink } from "@/components/BackLink";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { APIError, api, messageFor } from "@/lib/api";
import { useDialogRoute } from "@/lib/useDialogRoute";
import type { MessagesChats, Session, Version } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { UnlockDialog } from "@/features/vault/UnlockDialog";
import { ChatList } from "@/features/messages/ChatList";

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
  const sessionGone = chats.error instanceof APIError && chats.error.code === "locked";
  React.useEffect(() => {
    if (sessionGone) {
      setExpired(true);
      dropChats(sessionID);
      setSession(null);
    }
  }, [sessionGone, sessionID, dropChats]);

  async function lock() {
    const sid = sessionID;
    setLockError(null);
    try {
      await api.post(`/api/sessions/${sid}/lock`, {});
      setSession(null);
      dropChats(sid);
    } catch (err) {
      // `lock` is idempotent and an unknown id answers 204, so anything reaching here is a real
      // refusal and keeps the session on screen rather than pretending it closed.
      setLockError(messageFor(err, "Could not lock this backup."));
    }
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
      ) : chats.isError ? (
        <Card>
          {/* THE SERVER'S OWN MESSAGE. "could not read this backup's Messages domain" and a
              session that has gone are different facts with different remedies. */}
          <p className="text-sm text-danger">
            {messageFor(chats.error, "These conversations could not be read.")}
          </p>
        </Card>
      ) : (
        <>
          <ChatList data={chats.data} />
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
      )}

      {version ? (
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
      ) : null}
    </div>
  );
}
