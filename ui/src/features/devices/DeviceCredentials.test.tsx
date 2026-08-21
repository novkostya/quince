import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DeviceCredentials } from "./DeviceCredentials";
import { api } from "@/lib/api";
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
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <DeviceCredentials device={device} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
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

    renderSection();

    expect(await screen.findByText("Household iPhone")).toBeInTheDocument();
  });

  it("does NOT list a credential scoped to another device", async () => {
    // THE *WITHOUT TOUCHING THE OTHERS* HALF. A filter that let this row through would put a
    // Remove button for the kitchen iPad on the hallway tablet's page — a wrong SUCCESS, which is
    // worse than a wrong refusal.
    stageList([row({ id: "theirs", name: "Kitchen iPad", scope: { udid: "DEVICE-B" } })]);

    const { container } = renderSection();

    await waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(screen.queryByText("Kitchen iPad")).not.toBeInTheDocument();
    // And nothing at all renders, rather than an empty panel.
    expect(container).toBeEmptyDOMElement();
  });

  it("does NOT list the ADMIN's credential", async () => {
    // The admin's passkey reaches every device, so it is not "a passkey for this device" and
    // removing it here would be removing the admin's own way in from a page about a phone.
    stageList([row({ id: "admin", name: "laptop", scope: null })]);

    const { container } = renderSection();

    await waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when nobody holds a credential for this device", async () => {
    // NOT AN EMPTY-STATE CARD. The common device has no scoped credential and never will, so a
    // permanent panel would be noise that makes the real case harder to notice.
    stageList([]);

    const { container } = renderSection();

    await waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("shows only this device's rows when several devices have been shared", async () => {
    stageList([
      row({ id: "a", name: "Household iPhone", scope: { udid: "DEVICE-A" } }),
      row({ id: "b", name: "Kitchen iPad", scope: { udid: "DEVICE-B" } }),
      row({ id: "c", name: "laptop", scope: null }),
    ]);

    renderSection();

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

    renderSection();

    fireEvent.click(await screen.findByRole("button", { name: /remove/i }));

    await waitFor(() => expect(del).toHaveBeenCalled());
    expect(String(vi.mocked(del).mock.calls[0][0])).toContain("cred-1");
  });

  it("says removing signs that person out straight away", async () => {
    // quince#1001 landed, so this is TRUE rather than reassuring — and D9 requires the screen to say
    // so, or to say plainly that revocation is not immediate. There is no third option.
    stageList([row({ id: "cred-1", name: "Household iPhone", scope: { udid: "DEVICE-A" } })]);

    renderSection();

    expect(await screen.findByText(/signs that person out straight away/i)).toBeInTheDocument();
  });
});
