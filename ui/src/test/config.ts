import type { Config, ConfigResponse } from "@/lib/types";

// A COMPLETE `Config`, because a partial one hides the bug it would be built to avoid.
//
// `PUT /api/config` is a full-document replace decoded into a zero-valued Go struct, so a key the
// client omits is a key ZEROED on the server (quince#493). A fixture that omits keys lets a test
// pass over a form that would drop them — which is the one class of defect this document's shape
// exists to prevent.
//
// THE COMPLETENESS IS ENFORCED BY THE COMPILER, NOT BY THIS COMMENT — and it was the other way
// round until qn.8's slice 7 tripped over it. The literal ended `} as Config`, and an assertion
// suppresses BOTH halves of the check that matters here: a missing required key and an unknown
// one. So "every key the TS Config declares is here" was a promise nothing verified, and it had
// already gone false — `vault.session_ttl_minutes` was added to Config when the vault session
// registry landed and never added here, so every test built on this fixture was PUTting a config
// that would zero it on the server.
//
// Without the assertion, adding a key to Config fails HERE until it is added here too, which is
// the whole point of a fixture that claims to be complete.
export function testConfig(over: Partial<Config> = {}): Config {
  return {
    backup: { preferred_transport: "usb", require_encryption: true },
    storage: null,
    muxers: null,
    tls: { cert_file: "", key_file: "" },
    sessions: { allow_insecure_transport: false },
    reconcile: { interval_minutes: 360 },
    vault: { session_ttl_minutes: 15 },
    notifications: {
      staleness_days: 3,
      reminder_cooldown_hours: 24,
      overdue_days: 14,
      backup_available: true,
      backup_overdue: true,
      action_required: true,
      backup_failed: true,
      backup_completed: false,
    },
    ui: { theme: "system" },
    ...over,
  };
}

// The `GET /api/config` envelope. `file_text` is only what the user SET (qn.6j), so it is
// deliberately shorter than the resolved document above rather than a rendering of it.
export function testConfigResponse(over: Partial<Config> = {}): ConfigResponse {
  return {
    config: testConfig(over),
    warnings: [],
    source: { path: "/data/config.yml", mtime: "2026-08-18T00:00:00Z" },
    file_text: "notifications:\n  staleness_days: 3\n",
    discarded: false,
  };
}

// routeGet dispatches a mocked `api.get` BY PATH.
//
// IT EXISTS BECAUSE A BLANKET `mockResolvedValue` IS A LIE THAT ONLY SHOWS UP LATER (quince#1212).
// The notification page tests answered EVERY GET with the notifications payload, which was harmless
// only while the page made exactly one call. The moment it also read `/api/config` — for the
// settings form and the rendered file beside it — those tests handed a `NotificationsResponse` to a
// component expecting a `ConfigResponse`, and failed on a field neither the page nor the test is
// about. Routing by path keeps a fixture's answer attached to the question it answers.
export function routeGet(answers: Record<string, unknown>, over: Partial<Config> = {}) {
  return (path: string) => {
    if (path.startsWith("/api/config")) return Promise.resolve(testConfigResponse(over));
    for (const [prefix, body] of Object.entries(answers)) {
      if (path.startsWith(prefix)) return Promise.resolve(body);
    }
    return Promise.reject(new Error(`no fixture staged for GET ${path}`));
  };
}
