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

function renderPage(v: Version | null = ver()) {
  if (v) useVersionsStore.getState().replaceAll([v]);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
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
}

beforeEach(() => {
  vi.restoreAllMocks();
  useVersionsStore.setState({ byId: {}, order: [] });
  useDevicesStore.setState({ byUdid: {}, order: [] });
});

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
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    fireEvent.submit(document.querySelector("form") as HTMLFormElement);

    expect(await screen.findByText("Library/Notes/notes.sqlite")).toBeTruthy();
    expect(screen.getByText("HomeDomain")).toBeTruthy();
    expect(screen.getByText("4.1 KB")).toBeTruthy();
    // The expiry is DISPLAYED, not counted down — the word is what pins that a server instant is
    // being rendered rather than a client timer being started.
    expect(screen.getByText(/locks/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /^lock$/i })).toBeTruthy();
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
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    fireEvent.submit(document.querySelector("form") as HTMLFormElement);
    await screen.findByText("Library/Notes/notes.sqlite");

    expect(screen.queryByText(/iOS/)).toBeNull();
    // "1 file" is the count of what was LOADED and is true; a total from the backup is not
    // available at all, so no "of N" may appear anywhere.
    expect(screen.queryByText(/\bof \d/)).toBeNull();
    expect(screen.getByText(/^1 file$/)).toBeTruthy();
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
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    fireEvent.submit(document.querySelector("form") as HTMLFormElement);
    await screen.findByText("Library/Notes/notes.sqlite");

    fireEvent.click(screen.getByRole("button", { name: /show more/i }));
    expect(await screen.findByText("Media/DCIM/a.jpg")).toBeTruthy();
    // The cursor is ECHOED BACK UNTOUCHED, which is the one thing a client can get wrong about an
    // opaque value.
    expect(get).toHaveBeenLastCalledWith("/api/sessions/S1/browse?cursor=CUR-1");
    // The first page's rows are still there: paging ADDS, it does not replace.
    expect(screen.getByText("Library/Notes/notes.sqlite")).toBeTruthy();
    expect(screen.getByText(/end of the backup/i)).toBeTruthy();
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
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    fireEvent.submit(document.querySelector("form") as HTMLFormElement);

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
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    fireEvent.submit(document.querySelector("form") as HTMLFormElement);
    await screen.findByText("Library/Notes/notes.sqlite");

    post.mockResolvedValueOnce(undefined as never);
    fireEvent.click(screen.getByRole("button", { name: /^lock$/i }));

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
    fireEvent.click(await screen.findByRole("button", { name: /^open$/i }));
    fireEvent.submit(document.querySelector("form") as HTMLFormElement);

    expect(await screen.findByText(/the domain’s own folder/i)).toBeTruthy();
    expect(screen.getByText("HomeDomain")).toBeTruthy();
    // An absent mtime is ordinary in this format and must not render as 1 January 1970.
    expect(screen.queryByText(/1970/)).toBeNull();
    expect(screen.getByText("dir")).toBeTruthy();
  });
});
