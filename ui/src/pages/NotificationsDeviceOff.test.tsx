import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NotificationSettings } from "@/features/notifications/NotificationSettings";
import { useDevicesStore } from "@/stores/devices";
import type { Config, Device } from "@/lib/types";

// `device_off` — THE SIXTH STATUS CAUSE (quince#1270).
//
// `qn.12`'s D6 named five causes and quince#1212 built the fifth, `category_off`, because *"quince
// is set up, the phone is subscribed, and nothing will ever arrive"* had no reportable state. A
// per-device switch adds a sixth with exactly that property, and folding it into `category_off`
// would be the quince#940 defect: one true, useless sentence over two states whose remedies are on
// DIFFERENT SCREENS. So the assertions here are mostly about DISTINCTNESS, not about wording.

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

function config(over: Partial<Config["notifications"]> = {}): Config {
  return {
    notifications: {
      staleness_days: 3,
      reminder_cooldown_hours: 24,
      overdue_days: 14,
      backup_available: true,
      backup_overdue: true,
      action_required: true,
      backup_failed: true,
      backup_completed: false,
      ...over,
    },
  } as unknown as Config;
}

function seed(devices: Device[]) {
  useDevicesStore.setState({
    byUdid: Object.fromEntries(devices.map((d) => [d.udid, d])),
    order: devices.map((d) => d.udid),
  });
}

function renderSettings(cfg: Config) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <NotificationSettings config={cfg} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => seed([]));

describe("device_off — the sixth status cause", () => {
  it("says nothing when no device is muted", () => {
    seed([device()]);
    renderSettings(config());
    expect(document.body.textContent).not.toMatch(/will not notify you about/i);
  });

  it("reports a muted device and links to the page that fixes it", () => {
    seed([device({ notifications_enabled: false })]);
    renderSettings(config());

    expect(screen.getByText(/will not notify you about one of your devices/i)).toBeTruthy();
    const link = screen.getByRole("link", { name: "family-iphone" });
    expect(link.getAttribute("href")).toBe("/devices/DEV-1");
  });

  it("counts them when there is more than one", () => {
    seed([
      device({ notifications_enabled: false }),
      device({ udid: "DEV-2", name: "studio-ipad", notifications_enabled: false }),
      device({ udid: "DEV-3", name: "spare-iphone" }),
    ]);
    renderSettings(config());

    expect(screen.getByText(/will not notify you about 2 of your devices/i)).toBeTruthy();
    expect(screen.getByRole("link", { name: "studio-ipad" })).toBeTruthy();
    expect(screen.queryByRole("link", { name: "spare-iphone" })).toBeNull();
  });

  // THE WHOLE POINT. Both causes can be true at once, they are different silences, and their
  // remedies are on different screens — so both render and neither swallows the other.
  it("is reported ALONGSIDE category_off, not folded into it", () => {
    seed([device({ notifications_enabled: false })]);
    renderSettings(
      config({
        backup_available: false,
        backup_overdue: false,
        action_required: false,
        backup_failed: false,
        backup_completed: false,
      }),
    );

    expect(screen.getByText(/quince will not notify you about anything/i)).toBeTruthy();
    expect(screen.getByText(/will not notify you about one of your devices/i)).toBeTruthy();
  });

  // A UDID IS OPERATOR-PRIVATE. It is in the href, which is an address the browser needs; it is
  // never the link's TEXT, which is what a person reads.
  it("never shows a UDID as the link text, even with no name and no model", () => {
    seed([device({ name: "", model: "", notifications_enabled: false })]);
    renderSettings(config());
    expect(screen.getByRole("link", { name: /an unnamed device/i })).toBeTruthy();
  });

  // The muted state does not depend on the CATEGORIES: a device switched off is silent whatever
  // they say, and the notice says so rather than leaving a reader to work out the precedence.
  it("says the categories below do not override it", () => {
    seed([device({ notifications_enabled: false })]);
    renderSettings(config());
    expect(screen.getByText(/whatever\s+the categories below say/i)).toBeTruthy();
  });
});

// THE SCREEN SAYS PER-DEVICE EXCLUSION EXISTS EVEN WHEN NOTHING IS EXCLUDED (Operator, 2026-08-20).
//
// `DeviceCoverageNotice` reports the EXCEPTION and renders nothing when there is none — so in the
// all-enabled, nothing-muted state, which is where a working install sits, the five category
// switches were the whole of the policy a reader could see. The feature was discoverable only to
// somebody who had already found it.
//
// The assertions are deliberately about the QUIET state. A test that only checked the muted state
// would have passed against the old code.
describe("the per-device switch is mentioned even when nothing is muted", () => {
  it("says the categories apply to every device, and points at Devices", () => {
    seed([device(), device({ udid: "DEV-2", name: "studio-ipad" })]);
    renderSettings(config());

    expect(screen.getByText(/these apply to every device/i)).toBeTruthy();
    // The remedy AND where it lives — a sentence that says a device can be silenced without saying
    // where is an orientation line that does not orient.
    const link = screen.getByRole("link", { name: "Devices" });
    expect(link.getAttribute("href")).toBe("/");
  });

  it("names the reason it is not just 'turn the category off'", () => {
    seed([device()]);
    renderSettings(config());
    expect(screen.getByText(/without losing the category for the rest/i)).toBeTruthy();
  });

  // It is orientation, not a report — so it does not come and go with the state, and it does not
  // replace the notice that names which devices are excluded.
  it("stays when a device IS muted, beside the notice rather than instead of it", () => {
    seed([device({ notifications_enabled: false })]);
    renderSettings(config());

    expect(screen.getByText(/these apply to every device/i)).toBeTruthy();
    expect(screen.getByText(/will not notify you about one of your devices/i)).toBeTruthy();
  });
});
