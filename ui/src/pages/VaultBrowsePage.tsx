import * as React from "react";
import { ArrowLeft } from "lucide-react";
import { useParams } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { BackLink } from "@/components/BackLink";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { RelativeTime } from "@/components/RelativeTime";
import { APIError, api, messageFor } from "@/lib/api";
import { useDialogRoute } from "@/lib/useDialogRoute";
import type { BrowsePage, Session, Version } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { UnlockDialog } from "@/features/vault/UnlockDialog";
import { FileTable } from "@/features/vault/FileTable";

// VaultBrowsePage is one committed backup, opened (qn.8 slice 7 step 2).
//
// A PAGE RATHER THAN A DIALOG, which this codebase has now ruled the general case of three times:
// quince#838 gave scrolling back to the browser, quince#908 §4 gave a multi-step stateful flow a
// route instead of an accordion, and quince#931 made a dialog a place you went. `useDialogRoute`'s
// own comment names the exit condition — *"can become one the day a dialog is worth its own screen
// — at which point it is a page"* — and a paged, filterable list of a hundred thousand rows is that
// day.
//
// THE SESSION LIVES IN THIS COMPONENT AND NOWHERE ELSE. No store, no context, no cache: a vault
// session is not shared state, and a reload asks for the password again because *"the password is
// never persisted — unlock is per-session, always"* (contracts §1). That is the design working
// rather than state being lost.
//
// THE DIALOG DOES NOT OPEN ITSELF ON ARRIVAL, deliberately. An auto-opening dialog has no honest
// answer to Cancel: `navigate(-1)` on a freshly loaded tab leaves the application entirely, which
// is the hazard `BackLink` was written for. The locked panel below is a real screen instead — and
// it is the same screen you land on after Lock, so story 9's confirmation has somewhere to appear.
export function VaultBrowsePage() {
  const { id = "" } = useParams();
  const fromStore = useVersionsStore((s) => s.byId[id]);
  // On a cold deep-link the store is empty until the WS connects and `refreshAll` fills it, so this
  // falls back to a fetch exactly as DeviceDetailsPage does. There is no `GET /api/versions/{id}`;
  // the collection route is the one that exists.
  const all = useQuery({
    queryKey: ["versions"],
    queryFn: () => api.get<{ versions: Version[] }>("/api/versions"),
    enabled: !fromStore && id !== "",
  });
  const version = fromStore ?? all.data?.versions.find((v) => v.id === id);

  // THE SAME STORE `EncryptionDialog`'s CALL SITE READS, and that is the point rather than a
  // convenience: the backup password is set there and typed here, so both screens must anchor the
  // browser's saved credential on one value. `passwordSurfaces.test.tsx` asserts the two components
  // agree given the same prop; it cannot see the call sites, which is why this one is written
  // against the same source DeviceDetailsPage resolves `device.name` from.
  const deviceName = useDevicesStore((s) => (version ? s.byUdid[version.udid]?.name : undefined));

  // THE CACHE MUST NOT OUTLIVE THE SESSION, and `staleTime: Infinity` alone does not achieve that.
  // On a lock the query merely goes INACTIVE, and react-query holds its data for the default
  // `gcTime` — about five minutes — so a page of decrypted paths and filenames would sit in memory
  // after the user asked for it to be gone. Nothing wrong is ever displayed, because the key
  // carries the session id; what was wrong was the claim beside the query that nothing is held.
  //
  // BOTH ROUTES OUT OF A SESSION, not just the polite one: an explicit lock, and the 409 that says
  // the session ended without one. Found in review on quince#1410.
  const queryClient = useQueryClient();
  const dropBrowseCache = React.useCallback(
    (id: string) => {
      if (id !== "") queryClient.removeQueries({ queryKey: ["vault-browse", id] });
    },
    [queryClient],
  );
  const [session, setSession] = React.useState<Session | null>(null);
  const [lockError, setLockError] = React.useState<string | null>(null);
  const [locking, setLocking] = React.useState(false);
  const { open, onOpenChange } = useDialogRoute("unlock");
  const sessionID = session?.id ?? "";

  const browse = useInfiniteQuery({
    queryKey: ["vault-browse", sessionID],
    enabled: sessionID !== "",
    // The empty cursor IS the first page — contracts §1's `cursor` is optional and absent means the
    // beginning, so there is no separate first-page request shape to keep in step with this one.
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      api.get<BrowsePage>(
        `/api/sessions/${sessionID}/browse${pageParam ? `?cursor=${encodeURIComponent(pageParam)}` : ""}`,
      ),
    // ABSENT next_cursor IS THE LAST PAGE, and `undefined` is what react-query reads as "no more".
    // An empty string would be a falsy cursor that still looks like a page param, so it is
    // normalised here rather than at the two places that consume `hasNextPage`.
    getNextPageParam: (last) => last.next_cursor || undefined,
    // A browse page is decrypted backup content. It is not cached beyond the session that produced
    // it — the key is the session id, so a lock leaves nothing addressable — and it is never
    // refetched behind the user's back, because every refetch is a decrypt.
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: false,
  });

  // A 409 `locked` MID-BROWSE MEANS THE SESSION IS GONE, AND QUINCE CANNOT SAY WHICH WAY. The TTL
  // collecting it is the common route — nobody clicks Lock, they leave the tab open past the
  // timeout — but a daemon restart produces the identical answer, because the registry is in
  // memory. Measured on the stand: the body is `vault: no such session` either way, so there is
  // nothing below this to tell them apart. The copy names both causes rather than asserting the
  // likelier one, which would be a collapsed diagnostic (quince#940) in the one place the reader
  // cannot check it.
  const expired = browse.error instanceof APIError && browse.error.code === "locked";

  // THE OTHER ROUTE OUT. A `locked` 409 means the session ended without anybody locking it, and the
  // pages it produced are just as dead — so they go the same way as an explicit lock's. In an
  // effect rather than in render because dropping a query is a side effect, and doing it while
  // rendering would fight the very query that reported the error.
  React.useEffect(() => {
    if (expired) dropBrowseCache(sessionID);
  }, [expired, sessionID, dropBrowseCache]);

  async function lock() {
    setLocking(true);
    setLockError(null);
    try {
      await api.post(`/api/sessions/${sessionID}/lock`, {});
      setSession(null);
      dropBrowseCache(sessionID);
    } catch (err) {
      // The session may well be gone already — `lock` is idempotent and an unknown id answers 204
      // (contracts §1) — so anything that reaches here is a real refusal and keeps the session on
      // screen rather than pretending it closed.
      setLockError(messageFor(err, "Could not lock this backup."));
    } finally {
      setLocking(false);
    }
  }

  const back = version ? `/devices/${version.udid}` : "/";

  if (!version) {
    return (
      <section>
        <BackLink to="/" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
          <ArrowLeft size={14} /> Home
        </BackLink>
        {/* THREE STATES, NOT TWO, AND THE THIRD IS THE ONE THAT LIED. A failed fetch is neither
            "loading" nor "not in the list": quince did not check the version list, it failed to
            read it, and the id in the address bar may be perfectly good. Rendering the absent case
            for it sends the reader looking for a typo they did not make — two distinguishable
            causes with different remedies collapsed into the wrong one (quince#940).

            The server's own sentence is what says which, so it is shown rather than replaced —
            "the storage subsystem is not answering" is knowledge this client cannot reconstruct.
            Found in review on quince#1410. */}
        <div className="mt-4 text-sm text-muted">
          {all.isPending ? (
            "Loading…"
          ) : all.error ? (
            <span role="alert" className="text-danger">
              {messageFor(all.error, "Could not read this quince's version list.")}
            </span>
          ) : (
            "That backup is not in this quince's version list."
          )}
        </div>
      </section>
    );
  }

  const entries = browse.data?.pages.flatMap((p) => p.entries) ?? [];

  return (
    <section>
      <BackLink to={back} className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
        <ArrowLeft size={14} /> {deviceName || "Device"}
      </BackLink>

      <div className="mt-4 flex flex-wrap items-baseline justify-between gap-2">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Files in this backup</h1>
          {/* WHAT THE WIRE ACTUALLY CARRIES, AND NOT ONE FIELD MORE. Story 1 asks this header for
              the device name, the iOS version and the file count recorded IN the backup; `Session`
              carries none of the three and `vault.Info` carries all of them, which is quince#1408.
              Until that is ruled the header shows the version's own timestamp and the device's
              name as quince knows it TODAY — never an iOS version or a total, both of which would
              have to be invented here. */}
          <p className="mt-1 text-sm text-muted">
            <RelativeTime iso={version.created_at} />
            {deviceName ? ` · ${deviceName}` : null}
          </p>
        </div>
        {!version.encrypted ? <Badge tone="warn">unencrypted</Badge> : null}
      </div>

      {version.missing ? (
        <div className="mt-6 rounded-card border border-dashed border-line bg-card p-10 text-center">
          <div className="text-sm font-medium">This backup&rsquo;s files are no longer on disk</div>
          <div className="mt-1 text-sm text-muted">
            There is nothing to open. Remove the version from the device page to clear the row.
          </div>
        </div>
      ) : session && !expired ? (
        <>
          <div className="mt-6 flex flex-wrap items-center justify-between gap-2 rounded-card border border-line bg-card px-4 py-3">
            {/* THE EXPIRY IS DISPLAYED, NOT COUNTED DOWN. types.ts rules that a client-side timer
                diverges from the server's truth the moment a tab sleeps or a clock skews, and the
                version it shows is the one the user believes. RelativeTime is corrected for the
                server offset, so this is the server's instant rendered rather than the browser's
                arithmetic. */}
            <p className="text-sm text-muted">
              Locks <RelativeTime iso={session.expires_at} className="text-sm text-fg" />
            </p>
            <div className="flex items-center gap-2">
              {lockError ? (
                <span role="alert" className="text-sm text-danger">
                  {lockError}
                </span>
              ) : null}
              <Button size="sm" variant="outline" onClick={() => void lock()} disabled={locking}>
                {locking ? "Locking…" : "Lock"}
              </Button>
            </div>
          </div>

          {browse.isPending ? <div className="mt-4 text-sm text-muted">Reading the backup…</div> : null}

          {browse.error && !expired ? (
            <p role="alert" className="mt-4 text-sm text-danger">
              {messageFor(browse.error, "Could not read this backup.")}
            </p>
          ) : null}

          {browse.data ? (
            entries.length === 0 ? (
              <div className="mt-4 rounded-card border border-dashed border-line bg-card p-10 text-center text-sm text-muted">
                This backup holds no files.
              </div>
            ) : (
              <div className="mt-4">
                <FileTable entries={entries} />
                {/* NO INFINITE SCROLL. A page is a decrypt, and one that happens because the reader
                    scrolled is a decrypt nobody asked for. The count is shown so "more" is a
                    quantity rather than a promise. */}
                <div className="mt-3 flex items-center justify-between gap-2">
                  <span className="font-mono text-xs tabular-nums text-subtle">
                    {/* "so far" WHEN THERE IS MORE. The header goes to real trouble not to invent a
                        total (quince#1408), and a bare count beside a button whose whole meaning is
                        "there is more" reads as one. Review finding, quince#1410. */}
                    {entries.length} file{entries.length === 1 ? "" : "s"}
                    {browse.hasNextPage ? " so far" : ""}
                  </span>
                  {browse.hasNextPage ? (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => void browse.fetchNextPage()}
                      disabled={browse.isFetchingNextPage}
                    >
                      {browse.isFetchingNextPage ? "Reading…" : "Show more"}
                    </Button>
                  ) : (
                    <span className="text-xs text-muted">End of the backup.</span>
                  )}
                </div>
              </div>
            )
          ) : null}
        </>
      ) : (
        <div className="mt-6 rounded-card border border-line bg-card p-6">
          <div className="text-sm font-medium">
            {expired ? "This backup is no longer open" : "This backup is locked"}
          </div>
          <p className="mt-1 text-sm text-muted">
            {expired
              ? "The session ended — either the timeout in Settings passed, or quince restarted. Everything it decrypted was wiped either way. Open it again to carry on."
              : version.encrypted
                ? "quince decrypts it only while you are looking, and needs the backup password you set for this device."
                : "This backup is not encrypted, so opening it needs no password."}
          </p>
          <Button className="mt-4" onClick={() => onOpenChange(true)}>
            Open
          </Button>
        </div>
      )}

      <UnlockDialog
        version={version}
        deviceName={deviceName}
        open={open}
        onOpenChange={onOpenChange}
        onUnlocked={(s) => {
          setLockError(null);
          setSession(s);
        }}
      />
    </section>
  );
}
