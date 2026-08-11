import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { SetupPasswordPage } from "./SetupPasswordPage";
import { APIError } from "@/lib/api";
import * as auth from "@/lib/auth";
import * as webauthn from "@/lib/webauthn";

// qn.6m slice 4 — first run is ONE SCREEN. The tests below are almost entirely about what happens
// AFTER the password is set, because that is the half with no second chance: the session exists, the
// form's own submit would now 409, and anything that throws past the handler strands somebody whose
// install is fine on a screen telling them it is not.

const navigate = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => navigate };
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SetupPasswordPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function submit(pw = "hunter2") {
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: pw } });
  fireEvent.click(screen.getByRole("button", { name: /set password and continue/i }));
}

const AUTHED = { state: "authenticated", csrf_token: "t" } as const;

beforeEach(() => {
  vi.restoreAllMocks();
  navigate.mockClear();
  // The browser half of the capability test. jsdom defines neither by default.
  // @ts-expect-error — minimal stand-in for the browser global.
  window.PublicKeyCredential = {};
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  vi.spyOn(auth, "setup").mockResolvedValue({ ...AUTHED });
});

afterEach(() => {
  // @ts-expect-error — removing the stand-in.
  delete window.PublicKeyCredential;
});

describe("both options on one screen", () => {
  it("offers the passkey beside the password, checked by default", () => {
    renderPage();
    const box = screen.getByRole("checkbox", { name: /also set up a passkey/i });
    expect(box).toBeChecked();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
  });

  // THE OFFER IS A CLIENT-SIDE GUESS AND HAS TO BE — `GET /api/auth/passkeys` needs a session, and
  // on this screen creating one is what the button does. So the browser half is tested here and the
  // SERVER half cannot be, which is why the unsupported case below is handled after the fact.
  it("is absent where the browser cannot hold one", () => {
    // @ts-expect-error — removing the stand-in.
    delete window.PublicKeyCredential;
    renderPage();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("is absent on an insecure origin, where WebAuthn cannot run at all", () => {
    Object.defineProperty(window, "isSecureContext", { value: false, configurable: true });
    renderPage();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
});

describe("the happy paths", () => {
  it("sets the password, registers, and lands on Home", async () => {
    const reg = vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
    renderPage();
    submit();

    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/", { replace: true }));
    expect(auth.setup).toHaveBeenCalledWith("hunter2");
    expect(reg).toHaveBeenCalled();
  });

  // SKIPPING IS NORMAL AND UNREMARKED — story 3. Unchecking must not cost a screen.
  it("skips registration entirely when unchecked, with no extra step", async () => {
    const reg = vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
    renderPage();
    fireEvent.click(screen.getByRole("checkbox", { name: /also set up a passkey/i }));
    submit();

    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/", { replace: true }));
    expect(reg).not.toHaveBeenCalled();
  });
});

// EVERY CASE BELOW IS AN INSTALL THAT IS FINE. The password is set and the session issued, so none
// of them may read as a failed setup, and all of them must offer a way onward — otherwise first run
// ends on a screen whose only button now 409s.
describe("when the passkey does not happen", () => {
  it("a dismissed sheet is reported as a fact, not an error, with a way on", async () => {
    // `registerPasskey` resolves FALSE for a cancelled or timed-out sheet — never throws for it.
    vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(false);
    renderPage();
    submit();

    expect(await screen.findByText(/no passkey was added/i)).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /continue to quince/i }));
    expect(navigate).toHaveBeenCalledWith("/", { replace: true });
  });

  // THE SERVER HALF OF THE CAPABILITY TEST, arriving at the only moment it can. It is a fact about
  // the ADDRESS, not the device (qn.6k D2), so the copy must not leave someone blaming their phone.
  it("an address that cannot be a relying party says so, about the address", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValue(
      new APIError(409, "passkeys_unsupported_here", "this address cannot hold a passkey"),
    );
    renderPage();
    submit();

    expect(await screen.findByText(/tied to a domain name/i)).toBeInTheDocument();
    expect(screen.getByText(/bare IP address/i)).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("any other failure still ends on a way forward rather than a dead form", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValue(new Error("network is on fire"));
    renderPage();
    submit();

    expect(await screen.findByRole("button", { name: /continue to quince/i })).toBeInTheDocument();
    // The FORM is gone — re-submitting it would 409 against a password that now exists.
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
  });

  // THE ONE THAT MATTERS MOST. If the registration error escaped this page it would surface in
  // `PasswordForm`'s catch as a red message on a live setup form — telling somebody whose install
  // is complete that it failed, and offering them a button that can only 409 from here on.
  it("NEVER leaves the user on the setup form after the password exists", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValue(new Error("boom"));
    renderPage();
    submit();

    await screen.findByRole("button", { name: /continue to quince/i });
    expect(screen.queryByRole("button", { name: /set password and continue/i })).not.toBeInTheDocument();
  });
});

// FIRST-RUN PASSWORDLESS — qn.6m slice 7, quince#841 item 3, ruling B.
describe("going passwordless at first run", () => {
  it("offers the option, and says what it costs before it is taken", () => {
    renderPage();
    expect(screen.getByRole("button", { name: /use a passkey instead/i })).toBeInTheDocument();
    // The sentence a user cannot work out for themselves: a box they cannot get a shell on is
    // unrecoverable. D7 requires it on the screen that offers the choice.
    expect(screen.getByText(/console or SSH access/i)).toBeInTheDocument();
    expect(screen.getByText(/quince auth reset/)).toBeInTheDocument();
  });

  it("is absent where the browser cannot hold a passkey", () => {
    // @ts-expect-error — removing the stand-in.
    delete window.PublicKeyCredential;
    renderPage();
    expect(screen.queryByRole("button", { name: /use a passkey instead/i })).not.toBeInTheDocument();
  });

  // THE PRE-AUTH PAIR, NOT THE AUTHENTICATED ONE. Registration is session-required and first run has
  // no session; getting this wrong is a 401 on the one path the slice exists to open.
  it("registers against the FIRST-RUN endpoints and never sets a password", async () => {
    const reg = vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(true);
    renderPage();

    fireEvent.click(screen.getByRole("button", { name: /use a passkey instead/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/", { replace: true }));
    expect(reg).toHaveBeenCalledWith("This device", { firstRun: true });
    // NO PASSWORD IS SET, EVER — the rejected alternative was to generate one, register, then
    // delete it, which strands the user behind a password they never saw if registration fails.
    expect(auth.setup).not.toHaveBeenCalled();
  });

  // NOTHING HAS CHANGED after a dismissal — no password, no credential, no session — so the user is
  // exactly where they started and the form is still the way forward.
  it("reports a dismissal and leaves the password form usable", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockResolvedValue(false);
    renderPage();

    fireEvent.click(screen.getByRole("button", { name: /use a passkey instead/i }));

    expect(await screen.findByText(/no passkey was added/i)).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
  });

  // The server's own sentence — `already_configured` and `passkeys_unsupported_here` both name
  // something this client cannot know.
  it("surfaces the server's refusal", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValue(
      new APIError(409, "already_configured", "this quince is already set up"),
    );
    renderPage();

    fireEvent.click(screen.getByRole("button", { name: /use a passkey instead/i }));

    expect(await screen.findByText(/already set up/i)).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });
});
