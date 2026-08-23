import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MessagesPage } from "./MessagesPage";
import { APIError, api } from "@/lib/api";
import type { MessagesChats, Version } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).
//
// WHAT THIS FILE IS FOR: the session lifecycle on the Messages screen — locked, open, locked
// again — and what the cache HOLDS across it. Conversation names ARE the correspondents' names,
// so this screen's retention question is sharper than the overview's: what a lock must remove is
// a list of the people someone talks to. ChatList's own rendering is covered in ChatList.test.tsx.

function ver(over: Partial<Version> = {}): Version {
  return {
    id: "V1", udid: "DEV-1", backend: "reflink", zfs_snapshot: null,
    browse_root: "/backups/DEV-1/latest", created_at: "2026-08-20T00:00:00Z",
    job_id: "J1", kind: "full", encrypted: true, is_latest: true,
    structure_verified_at: "2026-08-20T00:00:00Z", content_verified_at: null,
    logical_bytes: 42_500_000_000, missing: false, storage_id: null,
    ...over,
  };
}

function chats(over: Partial<MessagesChats> = {}): MessagesChats {
  return {
    capabilities: ["threads", "attachments"],
    adapter_version: "messages.v1",
    warnings: [],
    unsupported_reason: null,
    page: {
      items: [
        { id: 1, guid: "G1", display_name: "Book Club", is_group: true, participants: ["A", "B"] },
        { id: 2, guid: "G2", identifier: "+10000000000", is_group: false },
      ],
    },
    ...over,
  };
}

// heldForSession counts what the cache holds under a session id, PREFIX-MATCHED rather than
// looked up by exact key — the invariant `removeQueries` actually enforces. Same reasoning as
// VersionOverviewPage.test.tsx: an exact lookup pins the test to the key's SHAPE where the
// claim is about its OWNER.
function heldForSession(qc: QueryClient, id: string): number {
  return qc.getQueryCache().findAll({ queryKey: ["messages-chats", id] }).length;
}

function renderPage() {
  useVersionsStore.getState().replaceAll([ver()]);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const r = render(
    <QueryClientProvider client={qc}>
      {/* Through the ROUTE, so the path shape in router.tsx and the one this page reads cannot
          drift apart silently. */}
      <MemoryRouter initialEntries={["/versions/V1/messages"]}>
        <Routes>
          <Route path="/versions/:id/messages" element={<MessagesPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...r, qc };
}

async function openAndUnlock() {
  fireEvent.click(await screen.findByRole("button", { name: /^unlock$/i }));
  fireEvent.submit(document.querySelector("form") as HTMLFormElement);
  await waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull());
}

beforeEach(() => {
  vi.restoreAllMocks();
  useVersionsStore.setState({ byId: {}, order: [] });
  useDevicesStore.setState({ byUdid: {}, order: [] });
});

describe("MessagesPage", () => {
  it("asks for the password before reading anything, and names what it is for", async () => {
    const get = vi.spyOn(api, "get");
    renderPage();

    expect(await screen.findByText(/needs the backup password/i)).toBeTruthy();
    // THE CLAIM IS THAT NOTHING WAS READ, not merely that nothing was shown. A locked screen
    // that had already fetched the conversations would look identical.
    expect(get.mock.calls.some(([url]) => String(url).includes("/messages/chats"))).toBe(false);
  });

  it("lists the conversations once unlocked", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ id: "S1", version_id: "V1", expires_at: "" });
    vi.spyOn(api, "get").mockImplementation(async (url: string) =>
      url.includes("/messages/chats") ? chats() : { versions: [ver()] },
    );
    renderPage();
    await openAndUnlock();

    expect(await screen.findByText("Book Club")).toBeTruthy();
    // The unnamed one falls back to its identifier — ChatList's rule, exercised through the
    // page so the wiring is covered as well as the component.
    expect(await screen.findByText("+10000000000")).toBeTruthy();
  });

  it("drops the conversations from the cache on lock — story 6", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ id: "S1", version_id: "V1", expires_at: "" });
    vi.spyOn(api, "get").mockImplementation(async (url: string) =>
      url.includes("/messages/chats") ? chats() : { versions: [ver()] },
    );
    const { qc } = renderPage();
    await openAndUnlock();
    await screen.findByText("Book Club");
    expect(heldForSession(qc, "S1")).toBe(1);

    fireEvent.click(screen.getByRole("button", { name: /^lock$/i }));

    // NOT MERELY THAT THE NAMES ARE GONE FROM THE SCREEN — the key carries the session id, so
    // that half is safe by construction. What needs asserting is that the cache is not HOLDING
    // a list of the correspondents after the user asked for it to be gone.
    await waitFor(() => expect(heldForSession(qc, "S1")).toBe(0));
    expect(screen.queryByText("Book Club")).toBeNull();
  });

  it("treats an expired session as a lock, and says both causes", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ id: "S1", version_id: "V1", expires_at: "" });
    vi.spyOn(api, "get").mockImplementation(async (url: string) => {
      if (url.includes("/messages/chats")) throw new APIError(409, "locked", "session not found or expired");
      return { versions: [ver()] };
    });
    renderPage();
    await openAndUnlock();

    // BOTH CAUSES, because quince cannot tell a TTL from a restart and naming one would be a
    // guess the screen cannot support.
    expect(await screen.findByText(/timed out, or quince restarted/i)).toBeTruthy();
  });

  it("shows the adapter's own sentence when this backup has no readable Messages database", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ id: "S1", version_id: "V1", expires_at: "" });
    vi.spyOn(api, "get").mockImplementation(async (url: string) =>
      url.includes("/messages/chats")
        ? chats({ unsupported_reason: "this backup has no Messages database quince can read", page: { items: [] } })
        : { versions: [ver()] },
    );
    renderPage();
    await openAndUnlock();

    // NOT AN EMPTY LIST. "quince cannot read this" and "you have no conversations" are
    // different facts, and rendering the first as the second is a claim about the user's data
    // that nobody established.
    expect(await screen.findByText(/no Messages database quince can read/i)).toBeTruthy();
  });
});
