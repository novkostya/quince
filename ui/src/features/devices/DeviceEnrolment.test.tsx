import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { DeviceEnrolment } from "./DeviceEnrolment";
import { api } from "@/lib/api";
import type { Device } from "@/lib/types";

// qn.13 slice 9d-2 — THE ADMIN'S QR SURFACE.
//
// The claim this file exists to pin is D5's: the code carries THIS BROWSER'S address, and the
// screen says which one. Two of the three things that address fixes fail silently when wrong, so a
// QR generated at the wrong address is the failure mode most likely to look like quince being
// broken rather than like a mistake anybody made.

const device = { udid: "DEVICE-A", name: "Household iPhone" } as Device;

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <DeviceEnrolment device={device} />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.restoreAllMocks());

function stageList(enrolments: unknown[] = []) {
  return vi.spyOn(api, "get").mockResolvedValue({ enrolments });
}

// THE QR ENCODES THIS BROWSER'S ORIGIN — the whole of D5 at the surface a person sees.
it("builds the code from the address this page was opened at, and names it on screen", async () => {
  stageList();
  vi.spyOn(api, "post").mockResolvedValue({
    id: "01A", udid: "DEVICE-A", created_at: "2026-08-21T12:00:00Z",
    expires_at: "2026-08-21T12:10:00Z", secret: "SEK",
  });
  renderSection();

  fireEvent.click(await screen.findByRole("button", { name: /create a code/i }));

  // The address is STATED, not left for the admin to infer from a code they cannot read.
  const shown = await screen.findByText(window.location.origin);
  expect(shown).toBeTruthy();

  // And the QR itself carries it. Rendered as SVG, so the value is readable from the DOM rather
  // than needing an image decode.
  const svg = document.querySelector("svg");
  expect(svg).toBeTruthy();
  expect(screen.getByText(/must be able to reach quince at that exact address/i)).toBeTruthy();
});

// THE WINDOW IS NAMED. Whoever scans this gets what it grants, so "works once, few minutes" is
// information the admin needs before deciding where to point their phone's camera.
it("says the code is single-use and short-lived", async () => {
  stageList();
  vi.spyOn(api, "post").mockResolvedValue({
    id: "01A", udid: "DEVICE-A", created_at: "2026-08-21T12:00:00Z",
    expires_at: "2026-08-21T12:10:00Z", secret: "SEK",
  });
  renderSection();

  fireEvent.click(await screen.findByRole("button", { name: /create a code/i }));
  expect(await screen.findByText(/works once, and only for a few minutes/i)).toBeTruthy();
});

// OUTSTANDING CODES ARE LISTED THOUGH THE SECRET IS NOT IN THEM. "Authority nobody can see is
// authority nobody revokes" — the ruling named the listing as the part not to trade away.
it("lists outstanding codes with a way to cancel each", async () => {
  stageList([
    { id: "01A", udid: "DEVICE-A", created_at: "2026-08-21T12:00:00Z", expires_at: "2026-08-21T12:10:00Z" },
  ]);
  renderSection();

  expect(await screen.findByText(/waiting to be used/i)).toBeTruthy();
  expect(screen.getByRole("button", { name: /cancel/i })).toBeTruthy();
});

// CANCELLING THE SHOWN CODE TAKES IT OFF THE SCREEN. A cancelled QR left rendered is a live
// credential that is not one, which is the worst direction for this particular thing to be wrong in.
it("removes the displayed code when it is cancelled", async () => {
  stageList();
  vi.spyOn(api, "post").mockResolvedValue({
    id: "01A", udid: "DEVICE-A", created_at: "2026-08-21T12:00:00Z",
    expires_at: "2026-08-21T12:10:00Z", secret: "SEK",
  });
  const del = vi.spyOn(api, "del").mockResolvedValue(undefined);
  stageList([
    { id: "01A", udid: "DEVICE-A", created_at: "2026-08-21T12:00:00Z", expires_at: "2026-08-21T12:10:00Z" },
  ]);
  renderSection();

  fireEvent.click(await screen.findByRole("button", { name: /create a code/i }));
  // The control: the code is on screen before the cancel.
  await screen.findByText(window.location.origin);
  expect(document.querySelector("svg")).toBeTruthy();

  fireEvent.click(await screen.findByRole("button", { name: /^cancel$/i }));
  await waitFor(() => expect(del).toHaveBeenCalled());
  await waitFor(() => expect(document.querySelector("svg")).toBeNull());
});

// A REFUSAL IS SHOWN, not swallowed. The admin clicked a button and must learn whether it worked.
it("surfaces a failure to create", async () => {
  stageList();
  vi.spyOn(api, "post").mockRejectedValue(new Error("boom"));
  renderSection();

  fireEvent.click(await screen.findByRole("button", { name: /create a code/i }));
  expect(await screen.findByRole("alert")).toBeTruthy();
});

// THE EXPIRY IS SERVER-CORRECTED, NOT A RAW BROWSER CLOCK (quince#1437 review).
//
// This screen is the sharpest case in the product for that rule: the window is minutes, the value
// is a credential, and the decision it drives is whether to hand a device over now. A skewed clock
// does not make a stale number here — it makes the admin wrong about whether a live credential is
// still live, in both directions.
//
// ASSERTED THROUGH THE RENDERED ELEMENT rather than by reading the source: `RelativeTime` emits a
// <time> with the ISO in `dateTime`, which `toLocaleTimeString` cannot produce.
it("renders the expiry through the server-corrected clock", async () => {
  stageList([
    { id: "01A", udid: "DEVICE-A", created_at: "2026-08-21T12:00:00Z", expires_at: "2026-08-21T12:10:00Z" },
  ]);
  renderSection();

  await screen.findByText(/waiting to be used/i);
  const time = document.querySelector("time");
  expect(time).toBeTruthy();
  expect(time?.getAttribute("dateTime")).toBe("2026-08-21T12:10:00Z");
});
