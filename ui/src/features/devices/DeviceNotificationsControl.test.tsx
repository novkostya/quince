import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { DeviceNotificationsControl } from "./DeviceNotificationsControl";
import { useDevicesStore } from "@/stores/devices";
import type { Device } from "@/lib/types";

// A `put` seam typed as the contract. The render-only tests never click, so what it resolves to
// is irrelevant to them — but a bare `vi.fn()` resolves to `undefined`, which no longer matches
// the seam's type now that the component READS the response (quince#1294 review).
function putOk(enabled = true) {
  return vi.fn().mockResolvedValue({ enabled });
}

function device(over: Partial<Device> = {}): Device {
  return {
    udid: "DEV-1",
    name: "family-iphone",
    model: "iPhone17,2",
    ios_version: "26.0.1",
    transports: { wifi: "2026-08-20T00:00:00Z" },
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "on",
    notifications_enabled: true,
    last_seen: "2026-08-20T00:00:00Z",
    last_backup: null,
    ...over,
  };
}

beforeEach(() => {
  useDevicesStore.setState({ byUdid: {}, order: [] });
});

describe("DeviceNotificationsControl", () => {
  it("puts enabled:false when the box is unticked", async () => {
    const put = vi.fn().mockResolvedValue({ enabled: false });
    render(<DeviceNotificationsControl device={device()} put={put} />);

    fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/devices/DEV-1/notifications", { enabled: false }),
    );
  });

  it("puts enabled:true when a muted device is unticked back on", async () => {
    const put = vi.fn().mockResolvedValue({ enabled: true });
    render(<DeviceNotificationsControl device={device({ notifications_enabled: false })} put={put} />);

    fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/devices/DEV-1/notifications", { enabled: true }),
    );
  });

  // THE LABEL NAMES THE SUBJECT DEVICE AND NEVER SAYS "THIS DEVICE".
  //
  // quince's notification settings already say "this device" about the SUBSCRIBER axis — which
  // browser receives — and `NotificationsThisDevice.test.tsx` records what that collision cost the
  // first time. A switch for the SUBJECT axis, on a page about one device, must not borrow the
  // phrase: the two would be indistinguishable on screen and mean opposite things.
  it("names the device and never says 'this device'", () => {
    render(<DeviceNotificationsControl device={device()} put={putOk()} />);
    expect(screen.getByText(/family-iphone/)).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/this device/i);
  });

  it("falls back to the model when the device has no name, exactly as the notification would", () => {
    render(<DeviceNotificationsControl device={device({ name: "" })} put={putOk()} />);
    expect(screen.getByText(/iPhone17,2/)).toBeTruthy();
  });

  // A UDID IS OPERATOR-PRIVATE AND IS MEANINGLESS TO A READER. `notify.deviceName` refuses to show
  // one and so does this, which is why the last fallback is a generic phrase rather than the id.
  it("never shows the UDID, even with no name and no model", () => {
    render(<DeviceNotificationsControl device={device({ name: "", model: "" })} put={putOk()} />);
    expect(document.body.textContent).not.toMatch(/DEV-1/);
  });

  // THE MUTED STATE EXPLAINS ITS OWN BLAST RADIUS. "No silent caps or fallbacks" is the rule, and a
  // ticked-off box that says nothing is exactly a cap the user cannot see the edges of: it covers
  // reminders AND failures, on EVERY subscribed browser, and only this device.
  it("says what muting covers, and what it does not", () => {
    render(<DeviceNotificationsControl device={device({ notifications_enabled: false })} put={putOk()} />);
    const text = document.body.textContent ?? "";
    expect(text).toMatch(/not a reminder/i);
    expect(text).toMatch(/not a failure/i);
    expect(text).toMatch(/every browser you have subscribed/i);
    expect(text).toMatch(/no other\s+device is affected/i);
  });

  it("says nothing extra while notifications are on", () => {
    render(<DeviceNotificationsControl device={device()} put={putOk()} />);
    expect(document.body.textContent).not.toMatch(/not a reminder/i);
  });

  // A FAILED WRITE LEAVES THE BOX WHERE IT WAS AND SAYS WHY. The control renders the STORED value,
  // so a refusal cannot leave the screen claiming a setting quince does not hold — and the reason is
  // rendered rather than swallowed, which is the difference between "quince refused" and "quince
  // silently did nothing".
  it("keeps the box where it was on a failed write, and shows the reason", async () => {
    const put = vi.fn().mockRejectedValue(new Error("the preference could not be saved"));
    render(<DeviceNotificationsControl device={device()} put={put} />);

    fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() => expect(screen.getByText(/could not be saved/i)).toBeTruthy());
    expect(screen.getByRole("checkbox")).toBeChecked();
    expect(useDevicesStore.getState().byUdid["DEV-1"]).toBeUndefined();
  });

  // THE STORE IS UPDATED FROM THE CONFIRMED WRITE. The daemon also publishes `device.updated` with
  // the same value, but a page whose socket is down would otherwise sit on the old value with no
  // sign anything happened.
  it("writes the confirmed value into the devices store", async () => {
    const put = vi.fn().mockResolvedValue({ enabled: false });
    render(<DeviceNotificationsControl device={device()} put={put} />);

    fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() =>
      expect(useDevicesStore.getState().byUdid["DEV-1"].notifications_enabled).toBe(false),
    );
  });

  // THE STORE TAKES THE RESPONSE'S VALUE, NOT THE ONE THIS COMPONENT ASKED FOR — and this is the
  // only test that can tell the two apart (quince#1294 review).
  //
  // Every other assertion here sends `enabled` and the mock returns the same `enabled`, which passes
  // whichever of the two the component reads. quince#1292 made `PUT …/notifications` return what
  // quince RECORDED by construction, precisely so a client never has to assume its own request
  // succeeded; this asserts that the client actually consumes it. The mock is therefore made to
  // disagree with the request — an arrangement no storage policy produces today, which is the point:
  // it pins the SHAPE of the guarantee, not a behaviour anything currently exhibits.
  it("follows the RESPONSE when it disagrees with the request", async () => {
    const put = vi.fn().mockResolvedValue({ enabled: true });
    render(<DeviceNotificationsControl device={device()} put={put} />);

    fireEvent.click(screen.getByRole("checkbox"));
    await waitFor(() => expect(put).toHaveBeenCalled());
    // Guard on the guard: if the request never carried `false`, the assertion below proves nothing
    // about which of the two values the store took.
    expect(put).toHaveBeenCalledWith("/api/devices/DEV-1/notifications", { enabled: false });
    await waitFor(() =>
      expect(useDevicesStore.getState().byUdid["DEV-1"].notifications_enabled).toBe(true),
    );
  });
});
