import { describe, it, expect } from "vitest";
import { encryptionBlocksBackup, unencryptedConsequence } from "./encryptionPolicy";
import type { ConfigResponse, Device } from "@/lib/types";

// quince#889. The two facts are on the wire already; what was missing is that nothing joined them,
// so the page offered a backup that could only fail at preflight and told the user their data was
// being filtered when none of it was being written.

function device(encryption: Device["backup_encryption"]): Device {
  return {
    udid: "DEV-1",
    name: "test-iphone",
    model: "iPhone16,1",
    ios_version: "26.0.1",
    transports: { usb: "t" },
    paired: "yes",
    backup_encryption: encryption,
    wifi_sync: "unknown",
    notifications_enabled: true,
    last_seen: "2026-07-20T00:00:00Z",
    last_backup: null,
  };
}

// Partial by design, cast at the boundary exactly as the other config-shaped fixtures do
// (ConfigView.test.tsx): this function reads two fields, and a full document would be forty lines
// of noise that go stale the next time the schema grows a key.
function config(requireEncryption: boolean): ConfigResponse {
  return {
    config: {
      backup: { preferred_transport: "usb", require_encryption: requireEncryption },
      storage: null,
    },
    warnings: [],
    source: { path: "/data/config.yml", mtime: null },
    file_text: "",
    discarded: false,
  } as unknown as ConfigResponse;
}

describe("encryptionBlocksBackup", () => {
  it("blocks an unencrypted device where encryption is required", () => {
    expect(encryptionBlocksBackup(device("off"), config(true))).toBe(true);
  });

  it("allows it where the policy permits unencrypted backups", () => {
    expect(encryptionBlocksBackup(device("off"), config(false))).toBe(false);
  });

  // `unknown` is "we could not ask the device", which a retry CAN resolve — so it is not the
  // knowable-in-advance class and must not disable the button (ruled with the rest, 2026-08-13).
  it("does not block an unknown encryption state, even under the strict policy", () => {
    expect(encryptionBlocksBackup(device("unknown"), config(true))).toBe(false);
  });

  it("says undefined — not false — until the config has arrived", () => {
    expect(encryptionBlocksBackup(device("off"), undefined)).toBeUndefined();
    expect(encryptionBlocksBackup(undefined, config(true))).toBeUndefined();
  });
});

describe("the banner's consequence clause", () => {
  it("says nothing is backed up when the policy forbids it", () => {
    expect(unencryptedConsequence(true)).toMatch(/nothing is being backed up/);
    expect(unencryptedConsequence(true)).not.toMatch(/omitted/);
  });

  it("keeps the omitted-data sentence where it is true", () => {
    expect(unencryptedConsequence(false)).toMatch(/Health, Keychain, and saved passwords are omitted/);
  });

  // The claim the whole issue is about: a consequence must not be printed before its premise is
  // known. While the policy is in flight the banner states the fact and stops.
  it("states only the fact while the policy is unknown", () => {
    expect(unencryptedConsequence(undefined)).toBe(".");
  });
});
