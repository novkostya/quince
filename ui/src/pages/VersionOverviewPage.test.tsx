import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { VersionOverviewPage } from "./VersionOverviewPage";
import { api } from "@/lib/api";
import type { Version } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).
//
// WHAT THIS FILE IS FOR, and it is narrow: the SESSION LIFECYCLE on the overview screen —
// locked, open, locked again — and specifically what the cache HOLDS across it. The rendering
// of the two tiers is covered in VersionSummary.test.tsx and UnlockedContents.test.tsx; what
// neither of them can see is whether decrypted figures survive a lock.

const NOTES = "com.example.notes";

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

// heldForSession counts what the cache holds under a session id, PREFIX-MATCHED rather than
// looked up by exact key — the invariant `removeQueries` actually enforces.
//
// COPIED FROM VaultBrowsePage.test.tsx DELIBERATELY, including the reason. An exact
// `getQueryData(["session-overview", id])` would pin this to the key's SHAPE where the claim
// is about its OWNER, and that broke there the moment the key grew a member. This one has the
// same hazard: an infinite query's cache entry is not what a naive lookup expects.
function heldForSession(qc: QueryClient, id: string): number {
  return qc.getQueryCache().findAll({ queryKey: ["session-overview", id] }).length;
}

function renderPage() {
  useVersionsStore.getState().replaceAll([ver()]);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const r = render(
    <QueryClientProvider client={qc}>
      {/* Through the ROUTE, so the path shape in router.tsx and the one this page reads
          cannot drift apart silently. */}
      <MemoryRouter initialEntries={["/versions/V1"]}>
        <Routes>
          <Route path="/versions/:id" element={<VersionOverviewPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { ...r, qc };
}

function preUnlock() {
  return {
    version_id: "V1",
    udid: "DEV-1",
    encrypted: true,
    created_at: "2026-08-20T00:00:00Z",
    kind: "full",
    file_count: null,
    device: {
      present: true, name: "Study Tablet", ios_version: "17.5.1", class: "iPad",
      product_type: "iPadT9,9", build_version: "21F9000",
      serial_number: "", unique_device_id: "",
    },
    backup: {
      present: true, state: "new", snapshot_state: "finished",
      date: "2026-08-20T00:00:00Z", uuid: "UUIDINVENTED0001", format_version: "3.3",
    },
    apps: {
      present: true, bundle_ids: [NOTES], display_name: "Study Tablet",
      itunes_version: "", last_backup_date: "",
      cellular: { imei: "", iccid: "", phone_number: "" },
    },
  };
}

function sessionOverview() {
  return {
    capabilities: [], adapter_version: "test", warnings: [], unsupported_reason: null,
    page: { items: [{ domain: `AppDomain-${NOTES}`, files: 10, bytes: 1000 }] },
    totals: { files: 10, bytes: 1000, domain_count: 1 },
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
  useVersionsStore.setState({ byId: {}, order: [] });
  useDevicesStore.setState({ byUdid: {}, order: [] });
});

async function openAndUnlock() {
  fireEvent.click(await screen.findByRole("button", { name: /^unlock$/i }));
  fireEvent.submit(document.querySelector("form") as HTMLFormElement);
  await waitFor(() => expect(document.querySelector('[role="dialog"]')).toBeNull());
}

describe("VersionOverviewPage", () => {
  // STORY 6 — the HELD half, which is the one with nothing structurally behind it.
  //
  // The query key carries the session id, so nothing wrong is ever DISPLAYED whatever the
  // cache holds — that half is safe by construction and cannot regress. Retention is
  // different: on a lock the query merely goes inactive and react-query keeps its data for
  // the default gcTime, so the explicit removeQueries is the only thing that drops it.
  // Deleting that call leaves the whole suite green without this test (quince#1476 review).
  it("holds nothing from the version's contents after a lock", async () => {
    vi.spyOn(api, "get").mockImplementation(async (path: string) => {
      if (path.includes("/overview") && path.startsWith("/api/versions")) return preUnlock();
      if (path.includes("/overview")) return sessionOverview();
      return { versions: [ver()] };
    });
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1", version_id: "V1", expires_at: "2099-01-01T00:00:00Z",
    });

    const { qc } = renderPage();
    await openAndUnlock();

    // The figures arrived, so there is genuinely something to drop — without this the test
    // could pass against a cache that was never populated.
    await waitFor(() => expect(heldForSession(qc, "S1")).toBeGreaterThan(0));

    fireEvent.click(await screen.findByRole("button", { name: /^lock$/i }));

    await waitFor(() => expect(heldForSession(qc, "S1")).toBe(0));
  });

  // The locked tier is not touched by a lock — it never needed the session, so the screen the
  // user came for is still there afterwards rather than emptying out.
  it("keeps the pre-unlock summary after a lock", async () => {
    vi.spyOn(api, "get").mockImplementation(async (path: string) => {
      if (path.includes("/overview") && path.startsWith("/api/versions")) return preUnlock();
      if (path.includes("/overview")) return sessionOverview();
      return { versions: [ver()] };
    });
    vi.spyOn(api, "post").mockResolvedValue({
      id: "S1", version_id: "V1", expires_at: "2099-01-01T00:00:00Z",
    });

    renderPage();
    await openAndUnlock();
    fireEvent.click(await screen.findByRole("button", { name: /^lock$/i }));

    expect(await screen.findByText("Study Tablet")).toBeInTheDocument();
    expect(screen.getByText(NOTES)).toBeInTheDocument();
  });

  // D9 — the browser stays reachable, and it stays reachable when the overview FAILS. A
  // version whose plists will not parse is exactly when somebody wants the file tree.
  it("still offers the file browser when the overview cannot be read", async () => {
    vi.spyOn(api, "get").mockImplementation(async (path: string) => {
      if (path.includes("/overview")) throw new Error("nope");
      return { versions: [ver()] };
    });

    renderPage();
    const link = await screen.findByRole("link", { name: /browse the files/i });
    expect(link.getAttribute("href")).toBe("/versions/V1/browse");
  });
});
