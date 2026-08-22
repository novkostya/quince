import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { DeviceDetailsPage } from "./DeviceDetailsPage";
import { useDevicesStore } from "@/stores/devices";
import { api } from "@/lib/api";
import { authStatusKey } from "@/lib/auth";
import type { Device } from "@/lib/types";

// EVERY IDENTIFIER IN THIS FILE IS INVENTED — the device name, the udid, the credential ids.
//
// SAID OUT LOUD BECAUSE THE GATE STRUCTURALLY CANNOT SAY IT (quince#1473 review). `privacy-check`
// answers *"is this a string somebody has already recorded as private"*; it cannot answer *"is this
// a real person"*, so a clean sweep is no evidence at all for a name nobody has flagged yet. The
// reviewer has to establish provenance by reading, and a file that does not state it costs them a
// sweep every time.
//
// THIS FILE EARNED THE LINE. Its first version carried a device name copied from the Operator's
// photograph of the real stand — a household member's given name joined to a device type. It was
// caught in review, before the commit reached `main`, and amended rather than followed up: §6 merges
// with `--rebase`, so a later rename would have left the original value in history permanently,
// which canon calls an incident requiring a rewrite.
//
// qn.13 slice 8e — THE ADMIN'S AFFORDANCES LEAVE A SCOPED HOLDER'S DEVICE PAGE.
//
// Found by the Operator on hardware, 2026-08-22, on the first walk after 8d-2 made this page a
// scoped holder's Home. Two things rendered that should not have:
//
//   - the "← Home" back link, which for them points at the page they are already on;
//   - the whole "Share this device" section, offering a household member the means to invite
//     ANOTHER one.
//
// NOTHING LEAKED. The enrolment routes are `adminOnly` (`scope_routes.go:101-103`), so the API
// refused. This is D8's other half — *the shell hides what a principal cannot use* — and a control
// that always fails is what that rule exists to remove.
//
// THE REAL PAGE, NOT A FRAGMENT. `DeviceDetailsWifiSync.test.tsx` deliberately extracts its badge
// rather than mounting this page, and that is right for a three-line rule. It is wrong here: the
// claim is about which controls the PAGE renders for whom, and a fragment test would prove the
// conditional and not its attachment — the exact gap that cost quince#1465 and quince#1467 a round
// each.

const UDID = "udid-fixture-0001";

const device = {
  udid: UDID,
  name: "hallway-iphone",
  model: "iPhone",
  ios_version: "26.6",
  paired: true,
  connection: "usb",
  wifi_sync: "unknown",
  backup_encryption: "unknown",
} as unknown as Device;

function stageStatus(scope: { udid: string } | null) {
  vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path.startsWith("/api/auth/status")) {
      return Promise.resolve({ state: "authenticated", csrf_token: "t", scope });
    }
    // Everything else this page reads — jobs, versions, storages, enrolments — answers empty. The
    // claim under test is about which CONTROLS render, not about their contents.
    if (path.startsWith("/api/config")) {
      return Promise.resolve({ config: { backup: {}, notifications: {} }, warnings: [], source: {}, file_text: "" });
    }
    return Promise.resolve({ jobs: [], versions: [], storages: [], enrolments: [], passkeys: [] });
  });
}

async function renderPage(scope: { udid: string } | null) {
  stageStatus(scope);
  useDevicesStore.setState({ byUdid: { [UDID]: device }, order: [UDID] });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const out = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/devices/${UDID}`]}>
        <Routes>
          <Route path="/devices/:udid" element={<DeviceDetailsPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  // WAIT FOR THE STATUS, or every absence assertion below passes against a page that has not
  // decided anything yet (quince#1452, quince#1465).
  await waitFor(() => expect(qc.getQueryData(authStatusKey)).toBeDefined());
  await screen.findByText("hallway-iphone");
  return out;
}

beforeEach(() => vi.restoreAllMocks());
afterEach(() => {
  vi.restoreAllMocks();
  useDevicesStore.setState({ byUdid: {}, order: [] });
});

describe("a scoped holder's own device page", () => {
  it("has no back link, because this page IS their Home", async () => {
    await renderPage({ udid: UDID });

    expect(screen.queryByRole("link", { name: /home/i })).not.toBeInTheDocument();
  });

  it("does not offer Share this device", async () => {
    await renderPage({ udid: UDID });

    expect(screen.queryByText(/share this device/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /create a code/i })).not.toBeInTheDocument();
  });
});

// THE CONTROLS. Without them both assertions above pass for a page that renders neither control to
// ANYBODY — which would take the admin's way back and their sharing surface away, and no scoped test
// would notice.
describe("the admin's view of the same page — the control", () => {
  it("keeps the back link", async () => {
    await renderPage(null);

    expect(await screen.findByRole("link", { name: /home/i })).toBeInTheDocument();
  });

  it("keeps Share this device", async () => {
    await renderPage(null);

    expect(await screen.findByText(/share this device/i)).toBeInTheDocument();
  });
});
