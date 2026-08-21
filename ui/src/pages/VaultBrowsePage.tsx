import * as React from "react";
import { ArrowLeft } from "lucide-react";
import { useParams } from "react-router-dom";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { BackLink } from "@/components/BackLink";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

  // TWO PIECES OF STATE FOR ONE FILTER, and the split is what stops a keystroke being a decrypt.
  // `draft` is what the boxes hold; `filter` is what has been asked for. Only the second is in the
  // query key, so typing `HomeDomain` one letter at a time issues one request rather than ten
  // against a manifest the daemon has to walk each time.
  const [draft, setDraft] = React.useState({ domain: "", prefix: "" });
  const [filter, setFilter] = React.useState({ domain: "", prefix: "" });
  const filtering = filter.domain !== "" || filter.prefix !== "";

  const browse = useInfiniteQuery({
    // THE FILTER IS IN THE KEY, so narrowing the list starts a fresh walk from the beginning rather
    // than appending to the one already on screen. A cursor is only meaningful against the query
    // that produced it (spec D3), so carrying one across a filter change would page through the
    // wrong sequence.
    queryKey: ["vault-browse", sessionID, filter.domain, filter.prefix],
    enabled: sessionID !== "",
    // The empty cursor IS the first page — contracts §1's `cursor` is optional and absent means the
    // beginning, so there is no separate first-page request shape to keep in step with this one.
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      // NO `limit`, DELIBERATELY. The server's default is design §7's batch size and quince has no
      // reason to ask for a different one — which means quince's own client CANNOT provoke a clamp,
      // and the disclosure below is for a caller that can. Sending a number here to make that
      // surface reachable would be inventing a knob to justify a guard.
      const p = new URLSearchParams();
      if (filter.domain) p.set("domain", filter.domain);
      if (filter.prefix) p.set("prefix", filter.prefix);
      if (pageParam) p.set("cursor", pageParam);
      const qs = p.toString();
      return api.get<BrowsePage>(`/api/sessions/${sessionID}/browse${qs ? `?${qs}` : ""}`);
    },
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
  // THE DOMAINS EVERY LOADED PAGE HAS SHOWN, kept in a ref so that accumulating them costs no
  // render. The union is done below, where `entries` is; only the ref itself has to be a hook.
  const seenDomains = React.useRef<{ id: string; set: Set<string> }>({ id: "", set: new Set() });

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

  // THE SUGGESTIONS ACCUMULATE ACROSS PAGES *AND* FILTERS, which is the whole point of them and is
  // not what deriving them from `entries` does.
  //
  // THE BUG THIS REPLACES, found in review on quince#1418: `entries` is the FILTERED result, so
  // "domains seen so far" was really "domains in the current result set" — it forgot everything the
  // moment it was used. Filter to one domain and the box offered exactly that domain; the way back
  // to the full set was to clear the filter first, which is the state the user was trying to leave.
  // That contradicts the argument the datalist is built on: a `<select>` was rejected for missing
  // the domain you have not paged to yet, and this missed the one you had just come from. On the
  // stand's numbers — 99 distinct domains on page one — it is the difference between a usable box
  // and one that helps once.
  //
  // ACCUMULATED DURING RENDER, WITH NO STATE AND NO EFFECT, and that is not a micro-optimisation.
  // The first version of this fix kept a `useState` and unioned in an effect, which adds a render
  // right after unlock — and **an extra render there leaves the unlock dialog open**, because
  // `useDialogRoute` closes it by navigating and the pop does not survive a re-render landing on
  // top of it. Two different tests caught it, one after the other, as the extra render moved. A set
  // union is idempotent, so doing it in render is safe under StrictMode's double invocation in a
  // way most render-time mutation is not.
  //
  // RESET IS KEYED ON THE SESSION: a different backup has different domains, and carrying them over
  // would suggest names that are not in the thing being browsed.
  if (seenDomains.current.id !== sessionID) seenDomains.current = { id: sessionID, set: new Set() };
  for (const e of entries) seenDomains.current.set.add(e.domain);
  const suggestions = Array.from(seenDomains.current.set).sort();
  // The list itself is accumulated above, and offered as SUGGESTIONS rather than as THE list. A
  // domain filter is an exact match and nothing on the wire enumerates the domains a backup holds —
  // a page carries entries, never a catalogue. So a `<select>` here would be a closed list built
  // from whichever pages happened to be loaded, which is a silent cap wearing a helpful face: the
  // domain you want is missing precisely because you have not paged to it yet. A `<datalist>`
  // suggests and still takes anything typed.

  // THE SERVER CLAMPED, SO THE SCREEN SAYS SO — contracts §1's `effective_limit` is *"no silent caps
  // or fallbacks as a wire field"*, and a client that reads it and shows nothing is where that
  // guarantee would die quietly. Present only when a clamp happened, so this is absent on every
  // ordinary page.
  //
  // UNREACHABLE FROM THIS CLIENT TODAY, stated rather than left to be discovered: the query above
  // sends no `limit`, so the server has nothing to clamp. This exists so that a clamp can never
  // arrive unannounced — if a page-size control is ever added, the disclosure is already wired
  // rather than being remembered.
  const clamped = browse.data?.pages.find((p) => p.effective_limit)?.effective_limit;
  // WHETHER ANYTHING ON SCREEN CARRIES A FLAG, which decides whether the words below the header
  // appear at all. Derived from the loaded rows rather than kept in state: the flags come from the
  // session's own memory of what it has read, so they arrive with a page rather than being events.
  const anyIncomplete = entries.some((e) => e.incomplete);
  const anyOverlong = entries.some((e) => e.overlong);

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

          {/* A FORM, so Enter applies. Both fields go to the server rather than filtering what is
              already on screen: a client-side filter over the loaded pages would narrow a sample
              and present it as the answer — the same silent cap as a `<select>` of seen domains. */}
          <form
            className="mt-4 flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              setFilter(draft);
            }}
          >
            <div className="flex flex-col gap-1">
              <Label htmlFor="browse-domain">Domain</Label>
              <Input
                id="browse-domain"
                list="browse-domains"
                placeholder="exact, e.g. HomeDomain"
                className="w-56"
                value={draft.domain}
                onChange={(e) => setDraft((d) => ({ ...d, domain: e.target.value }))}
              />
              <datalist id="browse-domains">
                {suggestions.map((d) => (
                  <option key={d} value={d} />
                ))}
              </datalist>
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="browse-prefix">Path starts with</Label>
              <Input
                id="browse-prefix"
                placeholder="e.g. Library/SMS"
                className="w-56"
                value={draft.prefix}
                onChange={(e) => setDraft((d) => ({ ...d, prefix: e.target.value }))}
              />
            </div>
            <Button type="submit" size="sm" variant="outline">
              Filter
            </Button>
            {filtering ? (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => {
                  setDraft({ domain: "", prefix: "" });
                  setFilter({ domain: "", prefix: "" });
                }}
              >
                Clear
              </Button>
            ) : null}
          </form>

          {/* THE CLAMP, IF ONE HAPPENED. Rendered above the list rather than beside the count,
              because it changes what the count MEANS.

              IT NAMES NO CAUSE, and that is a correction rather than brevity (review, quince#1418).
              This said the server *"reduced the page size this request asked for"* — and this client
              asks for no page size at all, which the query above says twice and the stand confirms.
              So the one request that could ever provoke this notice is a request that did not ask,
              and the sentence would have arrived describing the wrong cause on the one surface whose
              whole job is to make a cap non-silent. What the field states is the effective limit;
              that is what is written. */}
          {clamped ? (
            <p className="mt-3 text-sm text-warn">
              The server returned at most {clamped} files per page. Use <b>Show more</b> to read the
              rest.
            </p>
          ) : null}

          {browse.data ? (
            entries.length === 0 ? (
              // AN EMPTY FILTERED LIST IS NOT AN EMPTY BACKUP, and telling a user their backup
              // holds no files when what happened is that `HomeDomian` matched nothing is the
              // collapsed-diagnostic defect quince#940 named. The remedy is in the sentence.
              <div className="mt-4 rounded-card border border-dashed border-line bg-card p-10 text-center text-sm text-muted">
                {filtering
                  ? "No files in this backup match that domain and path. The domain has to match exactly — the box suggests the ones seen so far."
                  : "This backup holds no files."}
              </div>
            ) : (
              <div className="mt-4">
                {/* A DEGRADED MODE, SO IT IS PERSISTENT AND NAMES THE CONSEQUENCE (ui.design §1,
                    Operator ruling quince#446): no dismiss, no timeout, and it says what the
                    download will do rather than that something is wrong.

                    IT DOES NOT SAY THE OTHER FILES ARE FINE. Both flags are only known after quince
                    has READ a file, and `overlong` is detected on the unencrypted backend alone —
                    so "no badge" means nothing was reported, never that anything was checked
                    (quince#1379 review). The sentence about Refresh is what makes that
                    actionable instead of merely true. */}
                {anyIncomplete || anyOverlong ? (
                  <div className="mb-3 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn">
                    {anyIncomplete ? (
                      <p>
                        <b>incomplete</b> — the backup holds fewer bytes for that file than its own
                        index records, so the download ends early and cannot be completed by
                        retrying. A fresh backup of this device re-records it.
                      </p>
                    ) : null}
                    {anyOverlong ? (
                      <p className={anyIncomplete ? "mt-2" : undefined}>
                        <b>overlong</b> — the backup holds more bytes for that file than its index
                        records. quince delivers the recorded length and stops there.
                      </p>
                    ) : null}
                    <p className="mt-2">
                      quince learns either of these only by reading a file, so a row you have just
                      downloaded may need <b>Refresh</b> before it says so — and a row with no badge
                      is one nothing has been reported about, not one that has been checked.
                    </p>
                  </div>
                ) : null}
                <FileTable entries={entries} sessionID={sessionID} />
                {/* NO INFINITE SCROLL. A page is a decrypt, and one that happens because the reader
                    scrolled is a decrypt nobody asked for. The count is shown so "more" is a
                    quantity rather than a promise. */}
                <div className="mt-3 flex items-center justify-between gap-2">
                  <span className="flex items-center gap-3">
                    <span className="font-mono text-xs tabular-nums text-subtle">
                      {/* "so far" WHEN THERE IS MORE. The header goes to real trouble not to invent
                          a total (quince#1408), and a bare count beside a button whose whole meaning
                          is "there is more" reads as one. Review finding, quince#1410. */}
                      {entries.length} file{entries.length === 1 ? "" : "s"}
                      {browse.hasNextPage ? " so far" : ""}
                    </span>
                    {/* REFRESH EXISTS FOR THE FLAGS, and without it they would be unreachable in
                        practice. `incomplete` and `overlong` live in the SESSION's memory of what it
                        has read (`Registry.IncompleteIn`), so a file first discovered to be short
                        during your own download is flagged on the NEXT browse — and these pages are
                        held with `staleTime: Infinity`, deliberately, because a refetch is a
                        decrypt. So the one thing that would never happen on its own is the one
                        thing a reader needs after a download that looked wrong.

                        MEASURED ON THE STAND: zero flagged rows across six pages, then 15 overlong
                        and 12 incomplete across the same six after reading 600 real files. Nothing
                        on that screen changes on its own.

                        IT REFETCHES EVERY LOADED PAGE, which is why it is a button rather than
                        something automatic on focus. */}
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => void browse.refetch()}
                      disabled={browse.isRefetching}
                    >
                      {browse.isRefetching ? "Refreshing…" : "Refresh"}
                    </Button>
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
                    <span className="text-xs text-muted">
                      {filtering ? "End of the matches." : "End of the backup."}
                    </span>
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
