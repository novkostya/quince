import * as React from "react";
import { Link, useParams } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderTree } from "lucide-react";
import { BackLink } from "@/components/BackLink";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { APIError, api, messageFor } from "@/lib/api";
import { useDialogRoute } from "@/lib/useDialogRoute";
import type { Session, SessionOverview, Version, VersionOverview } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { UnlockDialog } from "@/features/vault/UnlockDialog";
import { VersionSummary } from "@/features/overview/VersionSummary";
import { UnlockedContents, versionAppIDs } from "@/features/overview/UnlockedContents";

// VersionOverviewPage is what a version IS — qn.9 slice 10, the rung's primary surface.
//
// IT IS THE VERSION'S PAGE, at `/versions/:id`, and the file browser moved one click behind
// it. The rung opened on "this is just test UI, it's not going to be in the end product" said
// about that browser; D9 is explicit that the answer is to stop it being PRIMARY rather than
// to delete it — it is qn.8's gate, the escape hatch for a domain no viewer models, and the
// only surface that reaches a file nothing models. The link out is below and stays.
//
// NO PASSWORD IS ASKED FOR HERE AND NONE CAN BE. Everything on this screen comes from the
// three unencrypted plists, so the page renders on arrival with no session, no unlock dialog
// and no cache to tear down at lock. That is the tier, not an optimisation.
//
// ROUTED ON THE VERSION for the reason VaultBrowsePage already gives: a session id dies at
// its TTL and a link carrying one is stale within minutes, where a version id is durable.
export function VersionOverviewPage() {
  const { id = "" } = useParams();

  const overview = useQuery({
    queryKey: ["version-overview", id],
    queryFn: () => api.get<VersionOverview>(`/api/versions/${id}/overview`),
    enabled: id !== "",
    // The tier is read off an IMMUTABLE version — a committed version is never mutated, by a
    // hard rule — so this answer cannot go stale while the page is open. The server memoises
    // the same read for the same reason.
    staleTime: Infinity,
  });

  // Same cold-deep-link fallback DeviceDetailsPage and VaultBrowsePage use: the store is
  // empty until the WS connects, and there is no `GET /api/versions/{id}` — the collection
  // route is the one that exists.
  const fromStore = useVersionsStore((s) => s.byId[id]);
  const all = useQuery({
    queryKey: ["versions"],
    queryFn: () => api.get<{ versions: Version[] }>("/api/versions"),
    enabled: !fromStore && id !== "",
  });
  const version = fromStore ?? all.data?.versions.find((v) => v.id === id);
  const deviceName = useDevicesStore((s) => (version ? s.byUdid[version.udid]?.name : undefined));

  // ── The UNLOCKED tier (slice 10b). Everything above this line needs no password. ──────────
  //
  // THE SESSION LIVES IN THIS COMPONENT AND NOWHERE ELSE, exactly as VaultBrowsePage keeps
  // its own: a vault session is not shared state, and a reload asks for the password again
  // because "the password is never persisted — unlock is per-session, always" (contracts §1).
  const queryClient = useQueryClient();
  const [session, setSession] = React.useState<Session | null>(null);
  const [lockError, setLockError] = React.useState<string | null>(null);
  const { open, onOpenChange } = useDialogRoute("unlock");
  const sessionID = session?.id ?? "";

  // STORY 6 — nothing derived from the version's CONTENT survives a lock, and `staleTime`
  // alone does not achieve that: on a lock the query merely goes inactive and react-query
  // holds its data for the default gcTime. The key carries the session id, so nothing wrong
  // is ever displayed; what must also be true is that nothing is HELD, and that needs the
  // explicit removal below.
  const dropContents = React.useCallback(
    (id: string) => {
      if (id !== "") queryClient.removeQueries({ queryKey: ["session-overview", id] });
    },
    [queryClient],
  );

  const contents = useInfiniteQuery({
    queryKey: ["session-overview", sessionID],
    enabled: sessionID !== "",
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      // limit=2000 IS MaxLimit, AND IT IS ASKED FOR ON PURPOSE — the one place in this UI
      // that does. D3's partition needs EVERY domain row to reconcile, not a page of them:
      // per-app sizes plus the remainder must equal the total, and a partial walk cannot be
      // checked against `totals`. One measured backup has 1,264 domains, so this is a single
      // request in practice; the pagination below is what makes it correct rather than lucky.
      const p = new URLSearchParams({ limit: "2000" });
      if (pageParam) p.set("cursor", pageParam);
      return api.get<SessionOverview>(`/api/sessions/${sessionID}/overview?${p.toString()}`);
    },
    getNextPageParam: (last) => last.page.next_cursor || undefined,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: false,
  });

  // WALK EVERY PAGE, and do it here rather than asking the user to press "more". The partition
  // is meaningless on a partial set — this is an aggregate, not a browsable list, which is the
  // whole difference between this screen and the file browser.
  React.useEffect(() => {
    if (contents.hasNextPage && !contents.isFetchingNextPage) void contents.fetchNextPage();
  }, [contents.hasNextPage, contents.isFetchingNextPage, contents]);

  // A 409 `locked` mid-walk means the session ended without anybody locking it — the TTL, or a
  // daemon restart. Same treatment as an explicit lock: the rows it produced are just as dead.
  const expired = contents.error instanceof APIError && contents.error.code === "locked";
  React.useEffect(() => {
    if (expired) {
      dropContents(sessionID);
      setSession(null);
    }
  }, [expired, sessionID, dropContents]);

  // The pages are folded into one object: the envelope's non-page fields come from the LAST
  // page (they describe the version, not the page), and the items are concatenated.
  const merged: SessionOverview | null = React.useMemo(() => {
    const pages = contents.data?.pages;
    if (!pages || pages.length === 0) return null;
    const last = pages[pages.length - 1];
    return { ...last, page: { items: pages.flatMap((p) => p.page.items) } };
  }, [contents.data]);

  async function lock() {
    const id = sessionID;
    setLockError(null);
    try {
      await api.post(`/api/sessions/${id}/lock`, {});
      setSession(null);
      dropContents(id);
    } catch (err) {
      // `lock` is idempotent and an unknown id answers 204, so anything reaching here is a
      // real refusal and keeps the session on screen rather than pretending it closed.
      setLockError(messageFor(err, "Could not lock this backup."));
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <BackLink to={version ? `/devices/${version.udid}` : "/"}>
        {deviceName ?? "Back"}
      </BackLink>

      {overview.isPending ? (
        <Card>
          <p className="text-sm text-muted">Reading this backup…</p>
        </Card>
      ) : overview.isError ? (
        <Card>
          {/* THE SERVER'S OWN MESSAGE, not a generic one. `corrupt_manifest` and `not_found`
              mean different things and have different remedies — a broken backup versus a
              version whose artifact is gone — and collapsing them is the defect this rung is
              named after. */}
          <p className="text-sm text-danger">
            {messageFor(overview.error, "This backup could not be read.")}
          </p>
        </Card>
      ) : (
        <VersionSummary overview={overview.data} deviceName={deviceName} />
      )}

      {/* THE UNLOCKED TIER. Below the summary, because the summary is what the screen is for
          and it is already complete — the unlock ADDS to a finished page rather than being
          the thing the page waits on. */}
      {overview.isSuccess ? (
        sessionID !== "" ? (
          <>
            <UnlockedContents
              overview={merged}
              bundleIDs={versionAppIDs(overview.data)}
              loading={contents.isPending || contents.isFetchingNextPage || contents.hasNextPage}
            />
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
          <Card>
            <div className="flex items-center justify-between gap-4">
              <div>
                <h3 className="text-sm font-medium text-fg">Sizes and file counts</h3>
                {/* NAMES THE PASSWORD AS THE COST, and says what is gained. A control whose
                    label is just "Unlock" makes the user find out by clicking. */}
                <p className="mt-1 text-xs text-muted">
                  How much space each app takes needs the backup password.
                </p>
              </div>
              <Button onClick={() => onOpenChange(true)}>Unlock</Button>
            </div>
            {expired ? (
              // BOTH CAUSES, because quince cannot tell them apart and saying one would be a
              // guess: the body is the same either way. Same reasoning VaultBrowsePage uses.
              <p className="mt-2 text-sm text-warn">
                That session ended — either it timed out, or quince restarted. Unlocking again
                is all that is needed.
              </p>
            ) : null}
          </Card>
        )
      ) : null}

      {version ? (
        <UnlockDialog
          version={version}
          deviceName={deviceName}
          open={open}
          onOpenChange={onOpenChange}
          onUnlocked={(s) => setSession(s)}
        />
      ) : null}

      {/* D9 — THE BROWSER STAYS, one click away. Kept even while the overview is loading or
          has failed: a version whose plists will not parse is exactly when somebody wants the
          file tree, and hiding the escape hatch on error would remove the remedy along with
          the diagnosis. */}
      <Card>
        <Link
          to={`/versions/${id}/browse`}
          className="flex items-center gap-2 text-sm text-accent hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
        >
          <FolderTree size={16} aria-hidden />
          Browse the files in this backup
        </Link>
        <p className="mt-1 text-xs text-muted">
          Every file, as stored. Needs the backup password.
        </p>
      </Card>
    </div>
  );
}
