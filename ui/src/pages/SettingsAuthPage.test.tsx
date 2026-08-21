import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { SettingsAuthPage } from "./SettingsAuthPage";
import { SettingsPage } from "./SettingsPage";
import { api } from "@/lib/api";

// qn.6m slice 6 — quince#841 ruling A: the auth surface is a PAGE linked from Settings, not a fourth
// block inside it.

function renderPage(el: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{el}</MemoryRouter>
    </QueryClientProvider>,
  );
}

// A FULL config document, not a stub — and the difference is load-bearing rather than tidiness.
//
// With a partial one the page's `data` guard never yields a rendered grid, so the two tests below
// that assert on the LOADED page were passing without ever reaching it: "no longer renders the
// passkeys card" would have held against the OLD code too, because the old card lived inside that
// same guard. A test that cannot fail is worse than no test, and this one was found by probing the
// link's guard and watching the wrong tests go red.
const FULL_CONFIG = {
  config: {
    backup: { preferred_transport: "usb", require_encryption: true },
    storage: [{ name: "s" }],
    sessions: { allow_insecure_transport: false },
    reconcile: { interval_minutes: 360 },
    vault: { session_ttl_minutes: 15 },
    ui: { theme: "system" },
  },
  warnings: [],
  source: { path: "/data/config.yml", mtime: null },
};

// A passkey surface that CAN offer its button, so "the card is not here" is a real observation.
const PASSKEYS_OK = { rp_id: "quince.example.com", supported: true, passkeys: [] };

// DISPATCHED BY PATH, NOT ONE BLANKET RESOLVE — and this is the second vacuity this file had.
//
// A single `mockResolvedValue` answers `/api/config` AND `/api/auth/passkeys` with the same body, so
// the passkeys card sees `supported: undefined`, renders its unsupported state, and offers no
// button. "The card is not on this page" then holds whether the card is there or not. Measured:
// with `<Passkeys />` put BACK into SettingsPage, the blanket-mock version of these tests still
// passed. This is the same defect that bit quince#834's settings test.
function mockAPI(config: unknown = FULL_CONFIG, passkeys: unknown = PASSKEYS_OK) {
  vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path.startsWith("/api/auth/passkeys")) return Promise.resolve(passkeys);
    if (path.startsWith("/api/config")) return Promise.resolve(config);
    return Promise.reject(new Error(`unmocked GET ${path}`));
  });
}

beforeEach(() => vi.restoreAllMocks());

describe("the auth page", () => {
  it("carries the passkeys surface and a way back to Settings", async () => {
    mockAPI();
    renderPage(<SettingsAuthPage />);

    expect(screen.getByRole("heading", { name: "Sign-in", level: 1 })).toBeInTheDocument();
    // The passkeys card moved here from Settings. Asserting its ACTION renders rather than matching
    // the word "passkey", which appears several times on this page — and rather than re-asserting
    // the card's internals, which `features/settings/Passkeys.test.tsx` already owns.
    //
    // THE ACTION IS A ROW SINCE qn.6o SLICE 4, not a button that opened a dialog. Its name field is
    // what identifies it, and that is still the card's action rather than its internals.
    expect(await screen.findByLabelText("Passkey name")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute("href", "/settings");
  });
});

describe("Settings links to it", () => {
  it("offers the link", async () => {
    mockAPI();
    renderPage(<SettingsPage />);

    expect(await screen.findByRole("link", { name: /sign-in/i })).toHaveAttribute(
      "href",
      "/settings/auth",
    );
  });

  // THE COST quince#834 ACCEPTED, NOW PAID BACK — and this is the assertion that makes the move
  // worth anything. That card sat inside Settings' `data` guard, so a box whose config failed to
  // LOAD showed no passkey surface at all. Moving the card to its own page only fixes that if the
  // WAY TO REACH IT is not behind the same condition; a link inside the guard would leave somebody
  // with a broken config unable to reach the credentials they get in with.
  it("STILL offers the link when the config cannot be loaded", async () => {
    vi.spyOn(api, "get").mockImplementation((p: string) =>
      p.startsWith("/api/config")
        ? Promise.reject(new Error("config is unreadable"))
        : Promise.resolve(PASSKEYS_OK),
    );
    renderPage(<SettingsPage />);

    expect(await screen.findByText(/could not load configuration/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /sign-in/i })).toHaveAttribute("href", "/settings/auth");
  });

  // The card must not be in BOTH places — a duplicated surface is two things to keep in step, and
  // the one left behind is the one that rots.
  //
  // ASSERTED ON THE REQUEST, NOT ON THE RENDERED BUTTON, and that is the third vacuity this file
  // had. The obvious form — find the link, then `queryByRole` for "Add a passkey" — resolves the
  // instant the link appears, and the link is UNCONDITIONAL, so the assertion runs before the
  // passkeys query has settled and holds whether the card is mounted or not. Measured: with
  // `<Passkeys />` put back into SettingsPage, that version still passed.
  //
  // "Nothing asked for the credential list" is the same claim without the race: if the card is not
  // mounted, nobody fetches it, and `waitFor` gives the query every chance to appear before this
  // concludes it did not.
  it("no longer mounts the passkeys card — nothing here fetches the credential list", async () => {
    mockAPI();
    renderPage(<SettingsPage />);

    await screen.findByRole("link", { name: /sign-in/i });
    await waitFor(() => expect(api.get).toHaveBeenCalledWith("/api/config"));

    const paths = vi.mocked(api.get).mock.calls.map(([p]) => p);
    expect(paths).not.toContain("/api/auth/passkeys");
  });
});

// THE ORDER IS THE CLAIM — quince#1316, Operator 2026-08-19: *"In passwordless setup Passkeys
// section must be the first — going passwordless is your choice and we should respect that."*
//
// ASSERTED AS A SEQUENCE, NOT AS PRESENCE, and that distinction is the whole point of these tests.
// Every section below was already on the page before this change; a `getByRole` per section would
// have passed against the FIXED order it replaces. `toEqual` on the list of headings is the only
// form that can fail when the order is wrong and everything is present, which is exactly the
// regression this rung is about.
describe("the auth page orders its sections by credential state", () => {
  const HERE = "quince.example.com";
  const ELSEWHERE = "quince.example.net";

  function passkeyAt(rpID: string) {
    return { id: rpID, name: "phone", rp_id: rpID, created_at: "2026-08-01T00:00:00Z", last_used_at: null };
  }

  function renderAt(hasPassword: boolean, passkeys: ReturnType<typeof passkeyAt>[]) {
    mockAPI(FULL_CONFIG, { rp_id: HERE, supported: true, has_password: hasPassword, passkeys });
    return renderPage(<SettingsAuthPage />);
  }

  // The `<h2>` of every section, in document order. `SectionHeading` is the one heading primitive on
  // this page (quince#1155), so this reads the page's structure rather than a list of test ids.
  function sections() {
    return screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
  }

  it("leads with the password when that is what signs you in", async () => {
    renderAt(true, [passkeyAt(HERE)]);
    // WAITED ON A DATA-DEPENDENT ELEMENT. The Add row renders before the list answers, so awaiting
    // it asserts the order of a LOADING page — which passed for the wrong reason. This sentence is
    // rendered from `supported`, which only the payload carries.
    await screen.findByText(/a passkey is tied to the address/i);

    expect(sections()).toEqual([
      "Change your password",
      "Passkeys",
      "Sign in with a passkey only",
      "Signing in over plain HTTP",
    ]);
  });

  // THE RULED CASE. A passwordless install chose to have no password, and leading with a password
  // form asks it to undo that choice before showing what it actually uses.
  it("leads with Passkeys on a passwordless install", async () => {
    renderAt(false, [passkeyAt(HERE)]);
    // WAITED ON A DATA-DEPENDENT ELEMENT. The Add row renders before the list answers, so awaiting
    // it asserts the order of a LOADING page — which passed for the wrong reason. This sentence is
    // rendered from `supported`, which only the payload carries.
    await screen.findByText(/a passkey is tied to the address/i);

    const order = sections();
    expect(order[0]).toBe("Passkeys");
    expect(order).toEqual(["Passkeys", "You sign in with a passkey only", "Signing in over plain HTTP"]);
  });

  // NO OFFER TO REMOVE A PASSWORD THAT IS NOT THERE, and none where no passkey could confirm it.
  // Same section list as the row above minus row 4, which is what makes the two rows different.
  it("offers no removal when there is no password", async () => {
    renderAt(false, [passkeyAt(HERE)]);
    // WAITED ON A DATA-DEPENDENT ELEMENT. The Add row renders before the list answers, so awaiting
    // it asserts the order of a LOADING page — which passed for the wrong reason. This sentence is
    // rendered from `supported`, which only the payload carries.
    await screen.findByText(/a passkey is tied to the address/i);

    expect(sections()).not.toContain("Sign in with a passkey only");
  });

  it("offers no removal when every passkey is bound elsewhere", async () => {
    renderAt(true, [passkeyAt(ELSEWHERE)]);
    // WAITED ON A DATA-DEPENDENT ELEMENT. The Add row renders before the list answers, so awaiting
    // it asserts the order of a LOADING page — which passed for the wrong reason. This sentence is
    // rendered from `supported`, which only the payload carries.
    await screen.findByText(/a passkey is tied to the address/i);

    expect(sections()).toEqual(["Change your password", "Passkeys", "Signing in over plain HTTP"]);
  });

  // PASSKEYS STILL LEAD, EVEN THOUGH NONE OF THEM WORKS HERE. They are what this install signs in
  // with everywhere else, and the section that explains the address problem is the password half
  // below — putting a password form first would bury the explanation under a form that cannot help.
  it("leads with Passkeys when they exist but none is bound here", async () => {
    renderAt(false, [passkeyAt(ELSEWHERE)]);
    // WAITED ON A DATA-DEPENDENT ELEMENT. The Add row renders before the list answers, so awaiting
    // it asserts the order of a LOADING page — which passed for the wrong reason. This sentence is
    // rendered from `supported`, which only the payload carries.
    await screen.findByText(/a passkey is tied to the address/i);

    // THE WHOLE SEQUENCE, like its three siblings — quince#1321 review. This was `order[0]` plus a
    // `toContain`, which is the pattern the commit above calls out: two weaker claims about one
    // list, neither of which pins where the third section sits.
    expect(sections()).toEqual([
      "Passkeys",
      "No passkey of yours works at this address",
      "Signing in over plain HTTP",
    ]);
  });
  // AN INSTALL WITH NOTHING TO SIGN IN WITH LEADS WITH THE PANEL THAT SAYS SO. This is the one
  // no-password state where passkeys do NOT lead, because there are none — leading with an empty
  // list would open the page on the section with the least to say.
  //
  // THE SENTENCE ABOVE AND THE ASSERTION BELOW AGREE FOR THE FIRST TIME HERE — quince#1319 review.
  // In the previous slice the panel was a statement section BELOW a "Set a password" form section,
  // so `order[0]` named the form while this comment named the panel; both were defensible readings
  // of "leads" and they pointed at different headings. Combining the two into one section settles
  // it: the panel's heading IS the section's heading, so there is only one thing "leads" can mean.
  it("leads with the honest panel when there are no credentials at all", async () => {
    renderAt(false, []);
    // WAITED ON A DATA-DEPENDENT ELEMENT. The Add row renders before the list answers, so awaiting
    // it asserts the order of a LOADING page — which passed for the wrong reason. This sentence is
    // rendered from `supported`, which only the payload carries.
    await screen.findByText(/a passkey is tied to the address/i);

    // THE WHOLE SEQUENCE, not `order[0]` plus an `indexOf`. Two weaker assertions about one list
    // were what let the prose and the code drift apart in the first place.
    expect(sections()).toEqual([
      "This quince has no way to sign in",
      "Passkeys",
      "Signing in over plain HTTP",
    ]);
  });
});

// EACH SECTION CARRIES ITS OWN REMEDY — quince#1316's second acceptance bullet: no section points
// at a form that is not adjacent to it.
//
// ASSERTED AS CONTAINMENT, NOT ADJACENCY ON SCREEN. `within(section)` is what "inline" means
// structurally, and it is the form that survives a layout change: a test that measured pixel order
// would pass on a page whose sections had been re-nested wrongly.
describe("the section that describes a state carries the form that ends it", () => {
  const HERE = "quince.example.com";
  const ELSEWHERE = "quince.example.net";

  function passkeyAt(rpID: string) {
    return { id: rpID, name: "phone", rp_id: rpID, created_at: "2026-08-01T00:00:00Z", last_used_at: null };
  }

  function renderAt(hasPassword: boolean, passkeys: ReturnType<typeof passkeyAt>[]) {
    mockAPI(FULL_CONFIG, { rp_id: HERE, supported: true, has_password: hasPassword, passkeys });
    return renderPage(<SettingsAuthPage />);
  }

  async function sectionNamed(name: string) {
    const heading = await screen.findByRole("heading", { name, level: 2 });
    const section = heading.closest("section");
    if (!section) throw new Error(`heading "${name}" is not inside a section`);
    return within(section);
  }

  it("puts the set-password form inside the passwordless section", async () => {
    renderAt(false, [passkeyAt(HERE)]);

    const section = await sectionNamed("You sign in with a passkey only");
    expect(section.getByLabelText("New password")).toBeInTheDocument();
    expect(section.getByRole("button", { name: "Set password" })).toBeInTheDocument();
  });

  it("puts the set-password form inside the no-credentials section", async () => {
    renderAt(false, []);

    const section = await sectionNamed("This quince has no way to sign in");
    expect(section.getByLabelText("New password")).toBeInTheDocument();
  });

  // AND THE ONE STATE THAT OFFERS NO FORM SAYS WHY, rather than merely omitting it — the third
  // acceptance bullet. Rule 1 refuses a credential change from a caller that cannot prove a present
  // credential, so a form here would 4xx every time it was submitted.
  //
  // THIS IS WHAT HOLDS THE GUARD IN PLACE, AND IT WAS MEASURED RATHER THAN ASSUMED — quince#1321
  // review. Deleting the `elsewhere-only` ternary in `PasswordControls` and running the suite turns
  // exactly this test red, and nothing else: `Tests 1 failed | 814 passed`.
  //
  // AN ABSENCE ASSERTION NEEDS A CONTROL, which is the two tests directly above: the SAME query, on
  // the same page, returns the field in `passwordless` and in `unconfigured`. Without them this
  // would pass against a component that rendered nothing at all.
  it("offers no form where one cannot succeed, and says so", async () => {
    renderAt(false, [passkeyAt(ELSEWHERE)]);

    const section = await sectionNamed("No passkey of yours works at this address");
    expect(section.queryByLabelText("New password")).not.toBeInTheDocument();
    expect(section.getByText(/there is no password form here/i)).toBeInTheDocument();
    // The remedy is still named — both of them, the cheaper one first.
    expect(section.getByText(ELSEWHERE)).toBeInTheDocument();
    expect(section.getByText(/quince auth reset/)).toBeInTheDocument();
  });

  it("keeps the change form in the has-password section", async () => {
    renderAt(true, [passkeyAt(HERE)]);

    const section = await sectionNamed("Change your password");
    expect(section.getByLabelText("Current password")).toBeInTheDocument();
    expect(section.getByRole("button", { name: "Change password" })).toBeInTheDocument();
  });
});
