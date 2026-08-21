import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EnrolPage } from "./EnrolPage";
import { APIError } from "@/lib/api";
import * as webauthn from "@/lib/webauthn";

// qn.13 slice 9d — THE ENROLMENT LANDING PAGE.
//
// WHAT THIS PAGE OWES THAT NO OTHER LAYER CAN. The server keeps the five refusals distinct
// (quince#1428); this is where they become sentences a person acts on. A page that collapsed them
// into "something went wrong" would waste every one of those decisions, and the reader here is an
// unauthenticated household member with no session to reason from.

function renderAt(search: string) {
  return render(
    <MemoryRouter initialEntries={[`/enrol${search}`]}>
      <EnrolPage />
    </MemoryRouter>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

// The page reads `webauthnAvailable`, so every test that wants the button has to stage a browser
// that has passkeys — otherwise it renders the unsupported panel and asserts nothing.
function withPasskeys() {
  vi.stubGlobal("isSecureContext", true);
  vi.spyOn(webauthn, "webauthnAvailable").mockReturnValue(true);
}

it("asks for one tap and no password, username or account chooser", () => {
  withPasskeys();
  renderAt("?secret=S1");

  expect(screen.getByRole("button", { name: /add a passkey/i })).toBeTruthy();
  // D4's shape: the durable credential is a passkey and the secret authorises one registration.
  // A field here would be asking for something quince has no use for.
  expect(document.querySelector("input")).toBeNull();
});

// A LINK WITH NO SECRET IS ITS OWN CASE, not an expiry. Telling somebody who mistyped the address
// that their link expired sends them to ask for a replacement they do not need.
it("says the link is incomplete when the secret is missing, and offers no button", () => {
  withPasskeys();
  renderAt("");

  expect(screen.getByText(/incomplete/i)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /add a passkey/i })).toBeNull();
});

it("says so before the button when the browser cannot do passkeys", () => {
  vi.spyOn(webauthn, "webauthnAvailable").mockReturnValue(false);
  renderAt("?secret=S1");

  expect(screen.getByText(/cannot add a passkey/i)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /add a passkey/i })).toBeNull();
});

// THE FIVE REFUSALS STAY FIVE ON SCREEN. This is the assertion the whole error taxonomy exists for:
// distinct codes in Go are worth nothing if one sentence renders for all of them.
describe("each refusal gets its own sentence", () => {
  const cases: Array<{ code: string; match: RegExp; notRetry?: boolean }> = [
    { code: "enrolment_unknown", match: /not from this quince/i },
    { code: "enrolment_expired", match: /expired/i },
    { code: "enrolment_spent", match: /already been used/i, notRetry: true },
    { code: "enrolment_revoked", match: /cancelled/i },
    { code: "rate_limited", match: /too many attempts/i },
  ];

  for (const c of cases) {
    it(c.code, async () => {
      withPasskeys();
      vi.spyOn(webauthn, "registerPasskey").mockRejectedValue(
        new APIError(429, c.code, "server sentence"),
      );
      renderAt("?secret=S1");

      fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));
      const alert = await screen.findByRole("alert");
      expect(alert.textContent ?? "").toMatch(c.match);

      // AND NEVER THE SERVER'S OWN STRING. The wire sentence is written for an API consumer; this
      // page writes for the person holding the phone.
      expect(alert.textContent ?? "").not.toMatch(/server sentence/);

      if (c.notRetry) {
        // THE ONE REFUSAL THAT IS NOT A RETRY. "Already used" means somebody else enrolled with
        // this link, and the page must say what to do about that rather than offer another go.
        expect(alert.textContent ?? "").toMatch(/tell whoever/i);
      }
    });
  }

  // AND THE CONTROL: an unrecognised code still says something useful rather than rendering blank.
  it("an unknown code falls back rather than showing nothing", async () => {
    withPasskeys();
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValue(
      new APIError(500, "internal", "boom"),
    );
    renderAt("?secret=S1");

    fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent ?? "").toMatch(/did not work/i);
  });
});

// A DISMISSED SHEET IS NOT A FAILURE. `registerPasskey` resolves false for a cancelled or timed-out
// prompt — the person is exactly where they started, so a red message would be quince inventing a
// problem out of somebody changing their mind.
it("shows no error when the passkey sheet is dismissed", async () => {
  withPasskeys();
  vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(false);
  renderAt("?secret=S1");

  fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));

  await waitFor(() =>
    expect(screen.getByRole("button", { name: /add a passkey/i })).toBeTruthy(),
  );
  expect(screen.queryByRole("alert")).toBeNull();
});

// THE CONFINEMENT IS STATED TO THE PERSON IT BINDS, on success and before it. Somebody who has just
// been given access should learn what they have without having to discover the edges by bumping
// into refusals.
it("says the holder will see one device, on success", async () => {
  withPasskeys();
  vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
  renderAt("?secret=S1");

  expect(screen.getByText(/one device/i)).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));
  expect(await screen.findByText(/you are set up/i)).toBeTruthy();
  expect(screen.getByText(/one device/i)).toBeTruthy();
});

// THE SECRET REACHES THE CEREMONY. Without this the page could render perfectly and enrol nothing.
it("passes the secret from the URL to the ceremony", async () => {
  withPasskeys();
  const reg = vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
  renderAt("?secret=SECRET-FROM-THE-QR");

  fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));
  await waitFor(() => expect(reg).toHaveBeenCalled());
  expect(reg.mock.calls[0][1]).toMatchObject({ enrolmentSecret: "SECRET-FROM-THE-QR" });
});

// THE HTTP CASE IS NOT A BROWSER PROBLEM (quince#1431 review).
//
// `webauthnAvailable()` is false over plain http too, because WebAuthn is secure-context-only — so
// one message for both told a household member on an http address that THEIR browser was at fault
// and to try Safari. Their browser is fine; Safari will not help; https will. quince supports plain
// http deliberately, so this is the whole pre-TLS population rather than an edge case.
describe("an unavailable authenticator says WHICH cause it is", () => {
  it("plain http names https and never blames the browser", () => {
    vi.spyOn(webauthn, "webauthnAvailable").mockReturnValue(false);
    vi.stubGlobal("isSecureContext", false);
    renderAt("?secret=S1");

    expect(screen.getByText(/secure address/i)).toBeTruthy();
    // Named more than once (the emphasis and the remedy), so this counts rather than assuming one.
    expect(screen.getAllByText(/https/i).length).toBeGreaterThan(0);
    // THE ASSERTION THAT MATTERS: the advice that would waste their time is absent.
    expect(screen.queryByText(/Safari/i)).toBeNull();
    expect(screen.queryByText(/this browser cannot/i)).toBeNull();
  });

  it("a secure context that still lacks passkeys does blame the browser, which is then true", () => {
    vi.spyOn(webauthn, "webauthnAvailable").mockReturnValue(false);
    vi.stubGlobal("isSecureContext", true);
    renderAt("?secret=S1");

    expect(screen.getByText(/this browser cannot add a passkey/i)).toBeTruthy();
    expect(screen.queryByText(/secure address/i)).toBeNull();
  });
});

// THE SUCCESS STATE IS NOT A DEAD END. The finish call sets the session cookies, so this person is
// signed in — and a screen with no way onward reads as finished when it is not.
it("offers a way onward after enrolling", async () => {
  withPasskeys();
  vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
  renderAt("?secret=S1");

  fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));
  await screen.findByText(/you are set up/i);
  expect(screen.getByRole("button", { name: /open your device/i })).toBeTruthy();
});

// THE PAGE SENDS NO NAME. The stored label is derived from the scope on the server, so a
// client-supplied one would be a household member naming a credential the ADMIN has to identify.
it("sends no credential name", async () => {
  withPasskeys();
  const reg = vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
  renderAt("?secret=S1");

  fireEvent.click(screen.getByRole("button", { name: /add a passkey/i }));
  await waitFor(() => expect(reg).toHaveBeenCalled());
  expect(reg.mock.calls[0][0]).toBe("");
});
