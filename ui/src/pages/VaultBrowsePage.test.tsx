import { describe, it, expect, vi, beforeEach } from "vitest";
// `waitFor` from testing-library rather than `vi.waitFor`: it wraps its polling in `act`, so a
// query resolving mid-wait does not produce an "update was not wrapped in act(...)" warning.
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { VaultBrowsePage } from "./VaultBrowsePage";
import { APIError, api } from "@/lib/api";
import type { Device, Version } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";

// THE VAULT'S FIRST SCREEN (qn.8 slice 7 step 2). What is asserted here is the SESSION LIFECYCLE as
// a user meets it — locked, open, paged, locked again — because that is the part no other test can
// see: the REST handlers are covered in Go, the dialog's credential shape in
// `passwordSurfaces.test.tsx`, and neither knows what happens to the screen in between.
//
// jsdom COMPUTES NO LAYOUT, so nothing below claims the page looks right. It claims the page says
// only what it can source and offers only what the state supports.

function ver(over: Partial<Version> = {}): Version {
  return {
    id: "V1",
    udid: "DEV-1",
    backend: "reflink",
    zfs_snapshot: null,
    browse_root: "/backups/DEV-1/latest",
    created_at: "2026-08-20T00:00:00Z",
    job_id: "J1",
    kind: "full",
    encrypted: true,
    is_latest: true,
    structure_verified_at: "2026-08-20T00:00:00Z",
    content_verified_at: null,
    logical_bytes: 42_500_000_000,
    missing: false,
    storage_id: null,
    ...over,
  };
}

// INVENTED PATHS, ALWAYS. A browse row is device content — a real relative path names somebody's
// files — so fixtures here are made up rather than trimmed from a stand.
function entry(over: Record<string, unknown> = {}) {
  return {
    file_id: "F1",
    domain: "HomeDomain",
    relative_path: "Library/Notes/notes.sqlite",
    kind: "file",
    size: 4096,
    mtime: "2026-08-19T10:00:00Z",
    ...over,
  };
}

// heldForSession counts what the cache holds under a session id, PREFIX-MATCHED rather than looked
// up by exact key — which is the invariant `removeQueries` actually enforces, and the one worth
// asserting.
//
// AN EXACT `getQueryData(["vault-browse", id])` BROKE THE MOMENT THE KEY GREW. Slice 7's filters
// appended `filter.domain` and `filter.prefix`, so the real key became
// `["vault-browse", id, "", ""]` and the lookup started returning `undefined` for a page that was
// very much still held. The production code was right throughout — `removeQueries` prefix-matches
// by default — so the test was pinned to the key's SHAPE where the claim is about its OWNER.
function heldForSession(qc: QueryClient, id: string): number {
  return qc.getQueryCache().findAll({ queryKey: ["vault-browse", id] }).length;
}

function renderPage(v: Version | null = ver()) {
  if (v) useVersionsStore.getState().replaceAll([v]);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // The client is RETURNED so a test can look at what is HELD rather than only at what is shown:
  // dropping a decrypted page from the cache leaves nothing on screen to assert.
  const r = render(
    <QueryClientProvider client={qc}>
      {/* Rendered THROUGH THE ROUTE rather than with a mocked `useParams`, so the path shape in
          `router.tsx` and the one this page reads cannot drift apart silently. */}
      <MemoryRouter initialEntries={["/versions/V1/browse"]}>
        <Routes>
          <Route path="/versions/:id/browse" element={<VaultBrowsePage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...r, qc };
}


// openAndUnlock drives the two clicks every session test needs, and then WAITS FOR THE DIALOG TO
// GO before returning.
//
// THE WAIT IS THE POINT, not the deduplication. `UnlockDialog` closes by navigating —
// `useDialogRoute` models a dialog as a place, so closing is a history pop — and the browse query
// fires the moment the session lands. Those two settle independently, so the first page can render
// while the dialog is still on top of it. Radix marks the background `aria-hidden` while a modal is
// open, which is why the symptom is precisely that `getByText` finds a row and `getByRole` cannot
// find a button beside it. Left implicit, it made three different tests fail on three consecutive
// runs as unrelated edits moved the timing — the shape of a flake that gets blamed on whatever was
// changed last.
//
// So the wait is also an assertion: the dialog really does close after a successful unlock, which
// nothing else here checks.

// domainOptions reads what the `<datalist>` is offering. The box stays a free-text `<input>` — the
// options only SUGGEST — so this deliberately reads the options rather than anything about what can
// be typed.
function domainOptions(): string[] {
  return Array.from(document.querySelectorAll("#browse-domains option")).map(
    (o) => (o as HTMLOptionElement).value,
  );
}

async function openAndUnlock() {
  fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
  fireEvent.submit(document.querySelector("form") as HTMLFormElement);
  await waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull());
}

beforeEach(() => {
  vi.restoreAllMocks();
  useVersionsStore.setState({ byId: {}, order: [] });
  useDevicesStore.setState({ byUdid: {}, order: [] });
});

// THE CONTROLS ARE AWAITED, NOT FETCHED SYNCHRONOUSLY, and that is what makes this file
// deterministic (quince#1419). Every test here crosses an async boundary — submit the unlock form,
// then the session and the first page arrive — and the ROW text lands in an earlier render than the
// chrome around it. `await findByText(row)` therefore does NOT prove `Lock` or `Show more` exist
// yet, and a synchronous `getByRole` for them raced that gap.
//
// Measured on `main` at 874581a before the fix: two failures in five full-suite runs locally, with
// a DIFFERENT test failing each time — `/^lock$/i` in one run, `/show more/i` in another — which is
// the signature of a race rather than a broken assertion. It had already turned trunk red once
// (run 32494673159) on a commit that changed two Go files and nothing else.
//
// THE INTERMEDIATE RENDER IS REAL AND IS NOT CHANGED HERE. The page genuinely shows rows for a tick
// before it shows the controls; that is a fact about the component, benign to a user at this speed,
// and left alone deliberately — a test that waits properly is the fix for a test that did not.
describe("VaultBrowsePage", () => {
  it("lands locked, and does not open a password dialog by itself", async () => {
    renderPage();
    expect(await screen.findByText(/this backup is locked/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /^open$/i })).toBeTruthy();
    // The dialog is a place you went (quince#931). Arriving is not going there, and an
    // auto-opened dialog has no honest answer to Cancel on a freshly loaded tab.
    expect(screen.queryByLabelText(/backup password/i)).toBeNull();
  });

  // STORY 8. The unencrypted case is not "the encrypted one minus the crypto" — it is a different
  // implementation (spec D7), and the promise the UI makes about it is that it never asks for a
  // password it would ignore.
  it("tells an unencrypted backup it needs no password, and never asks for one", async () => {
    renderPage(ver({ encrypted: false }));
    expect(await screen.findByText(/not encrypted, so opening it needs no password/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(screen.getByRole("button", { name: /^open$/i })).toBeTruthy());
    expect(screen.queryByLabelText(/backup password/i)).toBeNull();
  });

  it("opens, lists the first page, and shows when the session locks itself", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({ entries: [entry()] });

    renderPage();
    await openAndUnlock();

    expect(await screen.findByText("Library/Notes/notes.sqlite")).toBeTruthy();
    // THE CHROME IS AWAITED FIRST, and everything after it is a safe synchronous read of the same
    // render. The rows arrive in an earlier pass than the controls, so a sync assertion on chrome
    // placed above this line is the race quince#1419 is about.
    expect(await screen.findByRole("button", { name: /^lock$/i })).toBeTruthy();
    expect(screen.getByText("HomeDomain")).toBeTruthy();
    expect(screen.getByText("4.1 KB")).toBeTruthy();
    // The expiry is DISPLAYED, not counted down — the word is what pins that a server instant is
    // being rendered rather than a client timer being started.
    expect(screen.getByText(/locks/i)).toBeTruthy();
  });

  // STORY 1's LIMIT, ASSERTED AS A NEGATIVE. `Session` carries no device name, iOS version or file
  // count from the backup and `vault.Info` carries all three (quince#1408). Until that is ruled the
  // header must show none of them — and the failure mode of "show it anyway" is a plausible number
  // nobody can trace, which is why this is a test rather than a comment.
  it("claims no iOS version and no file total, because the wire carries neither", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({ entries: [entry()] });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    // THE POSITIVE IS AWAITED BEFORE THE NEGATIVE, and that ordering is load-bearing rather than
    // tidy: `queryByText(...)).toBeNull()` passes trivially against a page that has not rendered
    // yet, so a negative placed first would assert nothing at all on exactly the runs where the
    // race bites (quince#1419).
    expect(await screen.findByText(/^1 file$/)).toBeTruthy();
    expect(screen.queryByText(/iOS/)).toBeNull();
    // "1 file" is the count of what was LOADED and is true; a total from the backup is not
    // available at all, so no "of N" may appear anywhere.
    expect(screen.queryByText(/\bof \d/)).toBeNull();
  });

  // STORY 3's UI HALF. The cursor is the server's and opaque; what the page owes is that a page
  // with one yields a control, and a page without one says the list ended rather than leaving a
  // reader to guess whether more exists. That second direction is the one that would rot.
  it("pages on the cursor, and says when there is no next page", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi
      .spyOn(api, "get")
      .mockResolvedValueOnce({ entries: [entry()], next_cursor: "CUR-1" })
      .mockResolvedValueOnce({ entries: [entry({ file_id: "F2", relative_path: "Media/DCIM/a.jpg" })] });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    fireEvent.click(await screen.findByRole("button", { name: /show more/i }));
    expect(await screen.findByText("Media/DCIM/a.jpg")).toBeTruthy();
    // The cursor is ECHOED BACK UNTOUCHED, which is the one thing a client can get wrong about an
    // opaque value.
    expect(get).toHaveBeenLastCalledWith("/api/sessions/S1/browse?cursor=CUR-1");
    // The first page's rows are still there: paging ADDS, it does not replace.
    expect(screen.getByText("Library/Notes/notes.sqlite")).toBeTruthy();
    // AWAITED, AND BEFORE THE NEGATIVE BELOW IT. "End of the backup" is chrome that follows the
    // second page's rows, and `queryByRole(...)).toBeNull()` would pass against a page that has
    // not caught up — asserting the absence of a control for the wrong reason (quince#1419).
    expect(await screen.findByText(/end of the backup/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /show more/i })).toBeNull();
  });

  // STORY 9's UI HALF, and story 10's. A 409 `locked` mid-browse means the session is gone, and
  // quince cannot say which way: the TTL collecting it is the common route — nobody clicks Lock,
  // they leave the tab open — but a daemon restart gives the identical `vault: no such session`,
  // measured on the stand. So the copy names BOTH causes, and this asserts that rather than the
  // likelier one, because asserting one cause is how a collapsed diagnostic gets pinned in place.
  it("reads a dead session as the session ending, and names both ways it can", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockRejectedValue(
      new APIError(409, "locked", "vault: no such session"),
    );

    renderPage();
    await openAndUnlock();

    expect(await screen.findByText(/no longer open/i)).toBeTruthy();
    expect(screen.getByText(/timeout in settings passed, or quince restarted/i)).toBeTruthy();
    // NOT rendered as a failure: no alert, and the way back is on screen.
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("button", { name: /^open$/i })).toBeTruthy();
  });

  it("locks on request and returns to the locked screen", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({ entries: [entry()] });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    post.mockResolvedValueOnce(undefined as never);
    fireEvent.click(await screen.findByRole("button", { name: /^lock$/i }));

    expect(await screen.findByText(/this backup is locked/i)).toBeTruthy();
    expect(post).toHaveBeenLastCalledWith("/api/sessions/S1/lock", {});
    expect(screen.queryByText("Library/Notes/notes.sqlite")).toBeNull();
  });

  // A DEAD VERSION HAS NOTHING TO OPEN. The row already refuses to link to this page, so reaching
  // it means a bookmark or a version whose artifact vanished while the page was open — and the
  // answer must be the reason rather than an empty list, which is what a locked panel would look
  // like here.
  it("refuses a missing version with the reason, and offers no Open", async () => {
    renderPage(ver({ missing: true }));
    expect(await screen.findByText(/no longer on disk/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^open$/i })).toBeNull();
  });

  it("names a version this quince does not have, rather than spinning", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ versions: [] });
    renderPage(null);
    expect(await screen.findByText(/not in this quince's version list/i)).toBeTruthy();
  });

  // THE CREDENTIAL ANCHOR, AT THE CALL SITE. `passwordSurfaces.test.tsx` asserts that the unlock
  // dialog and the encryption dialog agree GIVEN THE SAME PROP; it cannot see which value each call
  // site passes. This is that half: the page resolves the device's name from the same store
  // `DeviceDetailsPage` resolves `device.name` from, so the browser files one saved password rather
  // than two for one secret.
  it("anchors the unlock dialog on the device name the encryption dialog uses", async () => {
    useDevicesStore.setState({
      byUdid: { "DEV-1": { udid: "DEV-1", name: "family-iphone" } as Device },
      order: ["DEV-1"],
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    const anchor = (await screen.findByLabelText("Device")) as HTMLInputElement;
    expect(anchor.value).toBe("family-iphone");
    expect(anchor.getAttribute("autocomplete")).toBe("username");
  });

  // STORY 3's OTHER HALF. Both fields go TO THE SERVER — a client-side filter over the pages
  // already loaded would narrow a sample and present it as the answer, which is the same silent cap
  // as offering a closed list of the domains seen so far.
  it("filters at the server, on domain and prefix together", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi.spyOn(api, "get").mockResolvedValue({ entries: [entry()] });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    fireEvent.change(screen.getByLabelText("Domain"), { target: { value: "MediaDomain" } });
    fireEvent.change(screen.getByLabelText(/path starts with/i), { target: { value: "DCIM/" } });
    // TYPING IS NOT ASKING. Six keystrokes above; the request happens on submit, because every one
    // of these is a walk of the manifest on the daemon.
    expect(get).toHaveBeenCalledTimes(1);

    fireEvent.click(await screen.findByRole("button", { name: /^filter$/i }));
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith("/api/sessions/S1/browse?domain=MediaDomain&prefix=DCIM%2F"),
    );
  });

  // A FILTERED MISS IS NOT AN EMPTY BACKUP. Collapsing the two is quince#940's defect exactly: both
  // sentences are true of an empty list and only one of them names something the reader can act on.
  it("says a filter matched nothing, rather than that the backup is empty", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get")
      .mockResolvedValueOnce({ entries: [entry()] })
      .mockResolvedValueOnce({ entries: [] });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    fireEvent.change(screen.getByLabelText("Domain"), { target: { value: "HomeDomian" } });
    fireEvent.click(await screen.findByRole("button", { name: /^filter$/i }));

    expect(await screen.findByText(/match that domain and path/i)).toBeTruthy();
    expect(screen.queryByText(/this backup holds no files/i)).toBeNull();
  });

  // THE UNFILTERED CONTROL for the case above: the same empty page, no filter, and the OTHER
  // sentence. Without this the test above passes against a component that only ever says one thing.
  it("says the backup is empty when it is, and no filter is set", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({ entries: [] });

    renderPage();
    await openAndUnlock();

    expect(await screen.findByText(/this backup holds no files/i)).toBeTruthy();
    expect(screen.queryByText(/match that domain and path/i)).toBeNull();
  });

  // Clearing must reach the LIST, not just the boxes. A Clear that emptied the inputs and left the
  // narrowed rows on screen would be a filter you cannot get out of.
  //
  // ASSERTED ON THE SCREEN RATHER THAN ON THE NETWORK, and the first version of this test got that
  // wrong: it expected a request for the unfiltered page and there is none, because react-query
  // still holds it under its own key. That is the cache being right — going back to the whole list
  // costs no second decrypt of a manifest already read — and a test that demanded the fetch would
  // have been pinning a waste.
  it("clears back to the whole backup", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi
      .spyOn(api, "get")
      .mockResolvedValue({ entries: [entry({ file_id: "F9", relative_path: "Media/DCIM/a.jpg" })] })
      .mockResolvedValueOnce({ entries: [entry()] });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    fireEvent.change(screen.getByLabelText("Domain"), { target: { value: "MediaDomain" } });
    fireEvent.click(await screen.findByRole("button", { name: /^filter$/i }));
    expect(await screen.findByText("Media/DCIM/a.jpg")).toBeTruthy();
    expect(screen.queryByText("Library/Notes/notes.sqlite")).toBeNull();
    expect(get).toHaveBeenLastCalledWith("/api/sessions/S1/browse?domain=MediaDomain");

    fireEvent.click(screen.getByRole("button", { name: /^clear$/i }));
    expect(await screen.findByText("Library/Notes/notes.sqlite")).toBeTruthy();
    expect(screen.queryByText("Media/DCIM/a.jpg")).toBeNull();
    expect(screen.queryByRole("button", { name: /^clear$/i })).toBeNull();
  });

  // STORY 4's UI HALF. `effective_limit` is *"no silent caps or fallbacks as a wire field"*, and a
  // client that reads it and renders nothing is where that guarantee dies quietly.
  //
  // BOTH DIRECTIONS, because the field is PRESENT ONLY WHEN THE SERVER CLAMPED. A test that only
  // proves the notice appears would pass just as happily against a component that shows it always,
  // which would report a clamp on every ordinary page.
  //
  // THE SENTENCE NAMES NO CAUSE, and that is asserted rather than left to the copy: it used to say
  // the server reduced *"the page size this request asked for"*, and this client asks for no page
  // size at all. If a clamp ever does arrive it will be the server clamping its own default, and
  // the notice would have described the wrong cause on the one surface whose job is to make a cap
  // non-silent (review, quince#1418).
  it("discloses a clamped page size, without claiming this client asked for one", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi.spyOn(api, "get").mockResolvedValue({ entries: [entry()] });

    const { unmount } = renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");
    expect(screen.queryByText(/at most/i)).toBeNull();
    unmount();

    get.mockResolvedValue({ entries: [entry()], effective_limit: 2000 });
    renderPage();
    await openAndUnlock();
    expect(await screen.findByText(/at most 2000 files per page/i)).toBeTruthy();
    // The request this client makes carries no `limit`, so nothing may say it asked for one.
    expect(screen.queryByText(/this request asked for/i)).toBeNull();
    expect(get).toHaveBeenLastCalledWith("/api/sessions/S1/browse");
  });

  // THE SUGGESTIONS ARE SUGGESTIONS. Nothing on the wire enumerates a backup's domains, so a
  // `<select>` built from loaded pages would omit exactly the domain you have not reached yet — a
  // silent cap wearing a helpful face. The input stays free text and the datalist only hints.
  it("suggests the domains it has seen without closing the list", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({
      entries: [entry(), entry({ file_id: "F2", domain: "MediaDomain", relative_path: "DCIM/a.jpg" })],
    });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");

    const box = screen.getByLabelText("Domain") as HTMLInputElement;
    expect(box.tagName).toBe("INPUT");
    const options = Array.from(document.querySelectorAll("#browse-domains option")).map(
      (o) => (o as HTMLOptionElement).value,
    );
    expect(options).toEqual(["HomeDomain", "MediaDomain"]);
  });

  // quince#1420. THE HALF THAT WAS NOT TESTED, and the accumulator exists ONLY for this half.
  //
  // The test above asserts the options in the UNFILTERED state, which is the one state where the
  // broken derivation — `Array.from(new Set(entries.map(e => e.domain)))` — was also correct. So
  // every test in this file passed against the bug, and a reasonable-looking simplification back to
  // that one line would pass them all again.
  //
  // THE PROPERTY IS NOT TRUE BY CONSTRUCTION. It is true because of one ref, and the whole point of
  // that ref is invisible from any state where the filter is empty. `entries` is the FILTERED
  // result, so deriving from it means the box forgets every domain the moment it is used — filter
  // to one and it offers exactly that one, and the way back to the full set is to clear the filter,
  // which is the state the user was trying to leave.
  it("keeps offering the domains it has seen AFTER a filter narrows the rows", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi.spyOn(api, "get").mockResolvedValueOnce({
      entries: [
        entry(),
        entry({ file_id: "F2", domain: "MediaDomain", relative_path: "DCIM/a.jpg" }),
        entry({ file_id: "F3", domain: "CameraRollDomain", relative_path: "DCIM/b.jpg" }),
      ],
    });

    renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");
    expect(domainOptions()).toEqual(["CameraRollDomain", "HomeDomain", "MediaDomain"]);

    // Narrow to ONE domain. The server answers with that domain's rows only, which is what makes
    // the derived version collapse — the filtered page is the only thing it could see.
    get.mockResolvedValue({
      entries: [entry({ file_id: "F2", domain: "MediaDomain", relative_path: "DCIM/a.jpg" })],
    });
    // AWAITED, NOT SYNCHRONOUS. A row landing does not prove the chrome rendered: the filter form
    // arrives in a later pass than the rows it sits above, and a synchronous query races that gap.
    // The rule is quince#1421's and it binds whether or not today's timing realises the race.
    fireEvent.change(await screen.findByLabelText("Domain"), { target: { value: "MediaDomain" } });
    fireEvent.click(await screen.findByRole("button", { name: /^filter$/i }));
    await screen.findByText("DCIM/a.jpg");
    expect(screen.queryByText("Library/Notes/notes.sqlite")).toBeNull();

    // ALL THREE, not the one on screen. This is the assertion the file did not have.
    expect(domainOptions()).toEqual(["CameraRollDomain", "HomeDomain", "MediaDomain"]);
  });

  // AND THEY DO NOT CROSS SESSIONS. A different backup has different domains, so carrying them over
  // would suggest names that are not in the thing being browsed — the mirror of the bug above, and
  // the reason the ref is keyed on the session rather than being a plain Set.
  it("forgets the domains when a new session opens", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi.spyOn(api, "get").mockResolvedValue({
      entries: [entry({ file_id: "F2", domain: "MediaDomain", relative_path: "DCIM/a.jpg" })],
    });

    renderPage();
    await openAndUnlock();
    await screen.findByText("DCIM/a.jpg");
    expect(domainOptions()).toEqual(["MediaDomain"]);

    // A second unlock mints a new session id, which is what the ref is keyed on.
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S2",
      version_id: "V1",
      expires_at: "2026-08-20T00:30:00Z",
    });
    get.mockResolvedValue({
      entries: [entry({ file_id: "F9", domain: "CameraRollDomain", relative_path: "DCIM/c.jpg" })],
    });
    fireEvent.click(await screen.findByRole("button", { name: /^lock$/i }));
    await screen.findByText(/this backup is locked/i);
    await openAndUnlock();
    await screen.findByText("DCIM/c.jpg");

    expect(domainOptions()).toEqual(["CameraRollDomain"]);
  });

  // FOUND ON HARDWARE, NOT IN A FIXTURE. The first page of a real encrypted iPad version is 500
  // rows of which 99 carry an EMPTY `relative_path` — one per domain, every one a `dir` of size 0.
  // The row rendered a blank line for each of them. A fixture author writes the rows they are
  // thinking about, which is why this one arrived from the stand and not from this file.
  it("names a domain-root row instead of rendering a blank line", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({
      entries: [entry({ file_id: "R1", relative_path: "", kind: "dir", size: 0, mtime: "" })],
    });

    renderPage();
    await openAndUnlock();

    expect(await screen.findByText(/the domain’s own folder/i)).toBeTruthy();
    expect(screen.getByText("HomeDomain")).toBeTruthy();
    // An absent mtime is ordinary in this format and must not render as 1 January 1970.
    expect(screen.queryByText(/1970/)).toBeNull();
    expect(screen.getByText("dir")).toBeTruthy();
  });

  // REVIEW FINDING, quince#1410. A FAILED fetch is neither "loading" nor "absent": quince did not
  // check the version list, it failed to read it, and the id may be perfectly good. The old copy
  // sent the reader looking for a typo they had not made — and the server's own sentence, which
  // says which, was sitting unread in `all.error`.
  it("says the version list could not be READ, rather than that the backup is not in it", async () => {
    vi.spyOn(api, "get").mockRejectedValue(
      new APIError(503, "unavailable", "the storage subsystem is not answering"),
    );
    renderPage(null);

    expect(await screen.findByText(/storage subsystem is not answering/i)).toBeTruthy();
    expect(screen.queryByText(/not in this quince's version list/i)).toBeNull();
  });

  // THE CONTROL for the case above, and it is the one that used to be the only case. A successful
  // fetch that genuinely does not hold the id still says so.
  it("still names a version this quince really does not have", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ versions: [] });
    renderPage(null);
    expect(await screen.findByText(/not in this quince's version list/i)).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  // REVIEW FINDING, quince#1410. `staleTime: Infinity` keeps a page fresh; it does not keep it out
  // of memory. On a lock the query goes INACTIVE and react-query holds its data for `gcTime` — so
  // a page of decrypted paths would outlive the lock that was meant to end it. Nothing wrong is
  // displayed either way, which is exactly why this has to be asserted against the CACHE.
  it("drops the decrypted page from the cache when the session is locked", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    vi.spyOn(api, "get").mockResolvedValue({ entries: [entry()] });

    const { qc } = renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");
    // The control: it really is held before the lock, so the assertion after it can fail.
    expect(heldForSession(qc, "S1")).toBeGreaterThan(0);

    post.mockResolvedValueOnce(undefined as never);
    fireEvent.click(await screen.findByRole("button", { name: /^lock$/i }));
    await screen.findByText(/this backup is locked/i);
    expect(heldForSession(qc, "S1")).toBe(0);
  });

  // A session that ended WITHOUT a lock leaves pages just as dead, and the polite route is not the
  // one users take (nobody clicks Lock). Same assertion, other door — reached here by asking for
  // the next page, which is what a reader does while a session quietly runs out under them.
  it("drops it on a session that ended by itself, not only on an explicit lock", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi.spyOn(api, "get").mockResolvedValueOnce({ entries: [entry()], next_cursor: "CUR-1" });

    const { qc } = renderPage();
    await openAndUnlock();
    await screen.findByText("Library/Notes/notes.sqlite");
    expect(heldForSession(qc, "S1")).toBeGreaterThan(0);

    get.mockRejectedValue(new APIError(409, "locked", "vault: no such session"));
    fireEvent.click(screen.getByRole("button", { name: /show more/i }));
    await screen.findByText(/no longer open/i);
    expect(heldForSession(qc, "S1")).toBe(0);
  });

  // REVIEW FINDING, quince#1410. The header refuses to invent a total (quince#1408) and the footer
  // then printed a bare count beside a button whose whole meaning is "there is more", which reads
  // as one. BOTH directions, because the qualifier must go away on the last page or it becomes the
  // same defect pointing the other way.
  it("qualifies the count while more pages exist, and drops the qualifier at the end", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1",
      version_id: "V1",
      expires_at: "2026-08-20T00:15:00Z",
    });
    const get = vi
      .spyOn(api, "get")
      .mockResolvedValueOnce({ entries: [entry()], next_cursor: "CUR-1" })
      .mockResolvedValueOnce({ entries: [entry({ file_id: "F2", relative_path: "Media/a.jpg" })] });

    renderPage();
    await openAndUnlock();

    expect(await screen.findByText(/^1 file so far$/)).toBeTruthy();
    fireEvent.click(await screen.findByRole("button", { name: /show more/i }));
    expect(await screen.findByText(/^2 files$/)).toBeTruthy();
    expect(screen.queryByText(/so far/)).toBeNull();
    expect(get).toHaveBeenCalledTimes(2);
  });
});
