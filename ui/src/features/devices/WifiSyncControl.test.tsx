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
  it("warns that turning sync off over Wi-Fi will disconnect the device", () => {
    render(
      <WifiSyncControl
        device={device({ wifi_sync: "on", transports: { wifi: "2026-07-31T00:00:00Z" } })}
        post={vi.fn()}
      />,
    );
    // The consequence is not actionable, so it lives in the button title rather than as standing
    // text under a control nobody is looking at — but it must still be SOMEWHERE the user can find.
    expect(
      screen.getByRole("button", { name: /turn off wi-fi sync/i }).getAttribute("title"),
    ).toMatch(/will disconnect it/i);
  });

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
