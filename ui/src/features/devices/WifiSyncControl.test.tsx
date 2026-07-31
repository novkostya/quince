import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { WifiSyncControl } from "./WifiSyncControl";
import type { Device } from "@/lib/types";

function device(over: Partial<Device> = {}): Device {
  return {
    udid: "DEV-1",
    name: "test-iphone",
    model: "iPhone17,2",
    ios_version: "26.0.1",
    transports: { usb: "2026-07-31T00:00:00Z" },
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "off",
    last_seen: "2026-07-31T00:00:00Z",
    last_backup: null,
    ...over,
  };
}

describe("WifiSyncControl", () => {
  it("offers to turn sync ON over USB, and posts enable", () => {
    const post = vi.fn().mockResolvedValue({ op_id: "op-1" });
    render(<WifiSyncControl device={device()} post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /turn on wi-fi sync/i }));
    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/wifi-sync", { action: "enable" });
  });

  it("offers to turn sync OFF when it is on, and posts disable", () => {
    const post = vi.fn().mockResolvedValue({ op_id: "op-2" });
    render(<WifiSyncControl device={device({ wifi_sync: "on" })} post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /turn off wi-fi sync/i }));
    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/wifi-sync", { action: "disable" });
  });

  // The finding that makes this control different from the encryption one. MEASURED 2026-07-31:
  // with Wi-Fi sync off the device stops announcing over mDNS, so a device that NEEDS it turned on
  // cannot be reached over Wi-Fi. Offering the button and failing would be the dishonest shape.
  it("DISABLES enable on a Wi-Fi-only device and says why", () => {
    const post = vi.fn();
    render(
      <WifiSyncControl
        device={device({ wifi_sync: "off", transports: { wifi: "2026-07-31T00:00:00Z" } })}
        post={post}
      />,
    );

    expect(screen.getByRole("button", { name: /turn on wi-fi sync/i })).toBeDisabled();
    expect(screen.getByText(/connect by cable to turn this on/i)).toBeTruthy();
  });

  // Turning sync OFF over Wi-Fi severs the transport the op runs on. The write lands first, but the
  // device then vanishes from the page — which reads as a failure unless it was predicted.

  // `unknown` means quince has not read the flag. Rendering a direction from it would be guessing.
  it("renders nothing when the state is unknown", () => {
    const { container } = render(
      <WifiSyncControl device={device({ wifi_sync: "unknown" })} post={vi.fn()} />,
    );
    expect(container.textContent).toBe("");
  });

  // The path is asserted against the OTHER device-op callers rather than against this component's
  // own source, because the first version of these tests was written from the implementation and
  // pinned its bug: the component posted to `/devices/…` with no `/api` prefix, five tests agreed
  // with it, and it surfaced on hardware as a generic "Something went wrong" — generic precisely
  // because a non-API path never returns the structured error the UI knows how to render.
  //
  // PairDialog posts `/api/devices/${udid}/pair`; EncryptionDialog `/api/devices/${udid}/encryption`.
  // Deriving the expectation from that shared prefix is what a typo in this file cannot satisfy.
  it("posts to the same /api/devices prefix the other device ops use", () => {
    const post = vi.fn().mockResolvedValue({ op_id: "op-3" });
    render(<WifiSyncControl device={device()} post={post} />);
    fireEvent.click(screen.getByRole("button", { name: /turn on wi-fi sync/i }));

    const [path] = post.mock.calls[0];
    expect(path.startsWith("/api/devices/")).toBe(true);
    expect(path).toBe(`/api/devices/${device().udid}/wifi-sync`);
  });
});

describe("disable confirmation", () => {
  // The Operator hit this on hardware: tapped disable over Wi-Fi, the device was cut off mid-action
  // and could only be recovered with a cable. A warning sentence was not enough.
  it("does NOT post immediately when disabling would disconnect the device", () => {
    const post = vi.fn();
    render(
      <WifiSyncControl
        device={device({ wifi_sync: "on", transports: { wifi: "2026-07-31T00:00:00Z" } })}
        post={post}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /turn off wi-fi sync/i }));

    expect(post).not.toHaveBeenCalled();
    expect(screen.getByText(/turn off wi-fi sync\?/i)).toBeTruthy();
  });

  it("posts disable only after the confirmation is accepted", () => {
    const post = vi.fn().mockResolvedValue({ op_id: "op-4" });
    render(
      <WifiSyncControl
        device={device({ wifi_sync: "on", transports: { wifi: "2026-07-31T00:00:00Z" } })}
        post={post}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /turn off wi-fi sync/i }));
    // The dialog's own button, not the trigger — getAllByRole picks both, the last is the dialog's.
    const buttons = screen.getAllByRole("button", { name: /turn off wi-fi sync/i });
    fireEvent.click(buttons[buttons.length - 1]);

    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/wifi-sync", { action: "disable" });
  });

  // Scoped to the destructive case. Over USB nothing is severed, so a confirmation would be
  // ceremony — and ceremony on a harmless action is how people learn to click through real ones.
  it("does NOT confirm when the device is on USB, because nothing gets cut off", () => {
    const post = vi.fn().mockResolvedValue({ op_id: "op-5" });
    render(<WifiSyncControl device={device({ wifi_sync: "on" })} post={post} />);
    fireEvent.click(screen.getByRole("button", { name: /turn off wi-fi sync/i }));

    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/wifi-sync", { action: "disable" });
  });
});

// The disconnect consequence must be RENDERED somewhere a touch user meets it — the earlier review
// rejected a `title` for exactly that reason. It now lives in the confirmation dialog, which is
// where a warning about a click belongs: at the moment of the click rather than as ambient prose.
it("explains the disconnect inside the confirmation, not as standing text", () => {
  render(
    <WifiSyncControl
      device={device({ wifi_sync: "on", transports: { wifi: "2026-07-31T00:00:00Z" } })}
      post={vi.fn()}
    />,
  );
  // Nothing ambient before the click.
  expect(screen.queryByText(/will disconnect it/i)).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: /turn off wi-fi sync/i }));
  expect(screen.getByText(/will disconnect it immediately/i)).toBeTruthy();
});

// Weight follows direction (quince#352): enable is the onboarding action this rung exists for and
// gets a real button; disable is a setting nobody reaches for and recedes. Asserted because a
// single weight is wrong half the time, and which half is not obvious from reading the component.
it("gives ENABLE a prominent button and DISABLE a quiet one", () => {
  const { unmount } = render(<WifiSyncControl device={device({ wifi_sync: "off" })} post={vi.fn()} />);
  const enable = screen.getByRole("button", { name: /turn on wi-fi sync/i }).className;
  expect(enable).toContain("border"); // outline
  unmount();

  render(<WifiSyncControl device={device({ wifi_sync: "on" })} post={vi.fn()} />);
  const disable = screen.getByRole("button", { name: /turn off wi-fi sync/i }).className;
  expect(disable).not.toContain("border");
});
