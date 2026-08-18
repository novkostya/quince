import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { TRIAL_WINDOW_SECONDS } from "./CertificateApply";

// THE OFFER CARD STATES THE WINDOW BEFORE THE DAEMON HAS NAMED IT, so one number lives on both sides
// of the wire — and a screen that promises three minutes while quince arms ten is a lie a user only
// discovers by waiting.
//
// IT READS THE GO SOURCE rather than holding a second copy: `certTrialWindow` is the authority, and
// a constant compared to a constant proves nothing. This is the same shape as the check that pins the
// certificate placeholders to `deploy/tls.md`.
//
// EVERY OTHER MENTION IS DERIVED — the live trial and the fallback sentence both read
// `expires_seconds` off the apply response, which is why they are not asserted here.
describe("the trial window the UI promises", () => {
  it("is the window the daemon arms", () => {
    const here = dirname(fileURLToPath(import.meta.url));
    const src = readFileSync(
      resolve(here, "../../../../core/internal/httpapi/cert_trial.go"),
      "utf8",
    );

    const m = /const certTrialWindow = (\d+) \* time\.(Minute|Second)/.exec(src);
    expect(m, "certTrialWindow is no longer a plain literal — teach this test its new shape").not.toBeNull();

    const seconds = Number(m![1]) * (m![2] === "Minute" ? 60 : 1);
    expect(TRIAL_WINDOW_SECONDS).toBe(seconds);
  });
});
