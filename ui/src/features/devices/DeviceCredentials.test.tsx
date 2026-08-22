import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DeviceCredentials } from "./DeviceCredentials";
import { api } from "@/lib/api";
import { passkeysKey } from "@/features/settings/Passkeys";
import type { Device } from "@/lib/types";

// qn.13 slice 11 / D9 — "the admin revokes ONE scoped credential without touching the others, from
// the device page it was issued from."
//
// THE CLAIM THIS FILE PINS is the *without touching the others* half, which is the one a screenshot
// cannot show and a careless filter would break: this section must list the credentials for THIS
// device and no others, so the admin cannot revoke a household member's access to the kitchen iPad
// while looking at a page about the hallway tablet.
//
// SYNTHETIC UDIDS. A real one is Operator-private and never enters a fixture.

const device = { udid: "DEVICE-A", name: "Household iPhone" } as Device;
const HERE = "quince.example.com";

function renderSection() {
  return renderFor(device);
}

// renderFor RENDERS AND WAITS FOR THE QUERY TO LAND — and that waiting is the whole point.
//
// AN ABSENCE ASSERTION MADE BEFORE THE RENDER PROVES NOTHING. These tests mostly assert that
// something is NOT on the screen, and `await waitFor(() => expect(api.get).toHaveBeenCalled())` — the
// obvious thing, and what this file did first — waits for the fetch to be CALLED, not for it to
// resolve and re-render. The container is empty at that moment whatever the component does, so the
// test passes for a broken filter, an absent filter, or an absent component.
//
// MEASURED, NOT THEORISED: with the filter deleted outright (`const rows = all`), three of the four
// absence tests here still passed. Only the one that did `findByText` first — forcing a render —
// went red.
//
// WAITING ON THE CACHE RATHER THAN ON A RENDERED ELEMENT is what makes this reusable for the cases
// where the correct answer is that NOTHING renders. There is no element to find, so the query's own
// data is the only observable that says the component has had its chance.
async function renderFor(d: Device) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const out = render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <DeviceCredentials device={d} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  await waitFor(() => expect(qc.getQueryData(passkeysKey)).toBeDefined());
  return out;
}

function row(over: Record<string, unknown>) {
  return {
    id: "x",
    name: "a passkey",
    rp_id: HERE,
    created_at: "2026-08-01T00:00:00Z",
    last_used_at: null,
    scope: null,
    ...over,
  };
}

function stageList(passkeys: unknown[]) {
  return vi.spyOn(api, "get").mockResolvedValue({ rp_id: HERE, supported: true, passkeys });
}

beforeEach(() => vi.restoreAllMocks());
afterEach(() => vi.restoreAllMocks());

describe("which credentials this device's page shows", () => {
  it("lists a credential scoped to THIS device", async () => {
    stageList([row({ id: "mine", name: "Household iPhone", scope: { udid: "DEVICE-A" } })]);

    await renderSection();

    expect(await screen.findByText("Household iPhone")).toBeInTheDocument();
  });

  it("does NOT list a credential scoped to another device", async () => {
    // THE *WITHOUT TOUCHING THE OTHERS* HALF. A filter that let this row through would put a
    // Remove button for the kitchen iPad on the hallway tablet's page — a wrong SUCCESS, which is
    // worse than a wrong refusal.
    stageList([row({ id: "theirs", name: "Kitchen iPad", scope: { udid: "DEVICE-B" } })]);

    const { container } = await renderSection();

    expect(screen.queryByText("Kitchen iPad")).not.toBeInTheDocument();
    // And nothing at all renders, rather than an empty panel.
    expect(container).toBeEmptyDOMElement();
  });

  it("does NOT list the ADMIN's credential", async () => {
    // The admin's passkey reaches every device, so it is not "a passkey for this device" and
    // removing it here would be removing the admin's own way in from a page about a phone.
    stageList([row({ id: "admin", name: "laptop", scope: null })]);

    const { container } = await renderSection();

    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when nobody holds a credential for this device", async () => {
    // NOT AN EMPTY-STATE CARD. The common device has no scoped credential and never will, so a
    // permanent panel would be noise that makes the real case harder to notice.
    stageList([]);

    const { container } = await renderSection();

    expect(container).toBeEmptyDOMElement();
  });

  it("shows only this device's rows when several devices have been shared", async () => {
    stageList([
      row({ id: "a", name: "Household iPhone", scope: { udid: "DEVICE-A" } }),
      row({ id: "b", name: "Kitchen iPad", scope: { udid: "DEVICE-B" } }),
      row({ id: "c", name: "laptop", scope: null }),
    ]);

    await renderSection();

    await screen.findByText("Household iPhone");
    expect(screen.queryByText("Kitchen iPad")).not.toBeInTheDocument();
    expect(screen.queryByText("laptop")).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /remove/i })).toHaveLength(1);
  });
});

describe("revoking from here", () => {
  it("removes the credential the admin pressed Remove on", async () => {
    stageList([row({ id: "cred-1", name: "Household iPhone", scope: { udid: "DEVICE-A" } })]);
    const del = vi.spyOn(api, "del").mockResolvedValue(undefined as never);

    await renderSection();

    fireEvent.click(await screen.findByRole("button", { name: /remove/i }));

    await waitFor(() => expect(del).toHaveBeenCalled());
    expect(String(vi.mocked(del).mock.calls[0][0])).toContain("cred-1");
  });

  it("says removing signs that person out straight away", async () => {
    // quince#1001 landed, so this is TRUE rather than reassuring — and D9 requires the screen to say
    // so, or to say plainly that revocation is not immediate. There is no third option.
    stageList([row({ id: "cred-1", name: "Household iPhone", scope: { udid: "DEVICE-A" } })]);

    await renderSection();

    expect(await screen.findByText(/signs that person out straight away/i)).toBeInTheDocument();
  });
});

// THE EMPTY-UDID CASE — quince#1452 review, and it is a test about a SENTINEL rather than about a
// device.
//
// `scopedTo` returns `""` for an admin credential. So a filter written as
// `scopedTo(p) === device.udid` is correct exactly as long as `device.udid` is never `""` — and when
// it is, every admin credential matches and this page grows a Remove button for the ADMIN's own
// passkey. That is the wrong SUCCESS the rest of this file exists to prevent, reached by an empty
// string rather than by a wrong device.
//
// UNREACHABLE TODAY, AND THAT IS THE POINT OF PINNING IT. A device page is routed by udid, so the
// invariant holds — but it holds in the ROUTER, not here, and a component whose correctness lives in
// another file is one render-from-somewhere-new away from being wrong.
describe("a device with no udid", () => {
  const adminOnly = [
    row({ id: "admin-1", name: "laptop", scope: null }),
    row({ id: "admin-2", name: "desktop", scope: null }),
  ];

  it("matches nothing, rather than matching every admin credential", async () => {
    stageList(adminOnly);

    const { container } = await renderFor({ udid: "", name: "nowhere" } as Device);

    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByRole("button", { name: /remove/i })).not.toBeInTheDocument();
  });

  // THE CONTROL, and without it the test above is a claim about a component that had not rendered.
  //
  // It is not "the same payload on a real device", because admin credentials match no device — so
  // that would be empty for the honest reason and prove nothing about the sentinel. What it pins
  // instead is that this harness DOES render rows when there are rows to render, on the same mock
  // and the same wait: if it ever stops doing so, the absence above becomes vacuous again and this
  // is what says so.
  it("and the harness renders rows when there ARE rows — the control", async () => {
    stageList([...adminOnly, row({ id: "mine", name: "Household iPhone", scope: { udid: "DEVICE-A" } })]);

    await renderFor(device);

    expect(await screen.findByText("Household iPhone")).toBeInTheDocument();
  });
});
