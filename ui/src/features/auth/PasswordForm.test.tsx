import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { MemoryRouter } from "react-router-dom";
import { PasswordForm } from "./PasswordForm";
import { APIError } from "@/lib/api";

function renderForm(onSubmit: (p: string) => Promise<void>) {
  return render(
    <MemoryRouter>
      <PasswordForm title="Sign in" subtitle="Enter your admin password." cta="Sign in" onSubmit={onSubmit} />
    </MemoryRouter>,
  );
}

function submit(password = "hunter2") {
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

// THE CREDENTIAL ANCHOR — quince#819. quince asks for two different passwords on one origin, and
// neither declared a username, so iCloud Keychain filed them together. The value is a constant
// because quince is single-admin: what it differentiates is this surface from the per-device backup
// password, not one admin from another.
describe("the password form's username anchor", () => {
  it("declares a readOnly quince-admin username the manager can key on", () => {
    renderForm(() => Promise.resolve());

    const anchor = screen.getByLabelText("Username");
    expect(anchor).toHaveValue("quince-admin");
    expect(anchor).toHaveAttribute("autocomplete", "username");
    // `readOnly`, never `disabled` — a disabled control is excluded from submission and skipped by
    // autofill, which would defeat the field's whole purpose.
    expect(anchor).toHaveAttribute("readonly");
    expect(anchor).not.toBeDisabled();
  });

  // The anchor must not become the thing the user lands on: `autoFocus` stays on the password, so
  // typing still works the moment the page appears.
  it("leaves the password field the one that takes focus", () => {
    renderForm(() => Promise.resolve());
    expect(screen.getByLabelText("Password")).toHaveFocus();
  });

  // AND IT MUST NOT BE REACHABLE BY TAB EITHER — quince#824. A read-only input defaults to
  // `tabindex=0`, so the anchor sat in the tab order ahead of the password and could hold the focus
  // ring in front of the only field on the page with anything to type into. `-1` removes it from
  // the sequence without making it `disabled`, which would take it out of autofill's sight too.
  it("keeps the anchor out of the tab order without disabling it", () => {
    renderForm(() => Promise.resolve());

    const anchor = screen.getByLabelText("Username");
    expect(anchor).toHaveAttribute("tabindex", "-1");
    expect(anchor).not.toBeDisabled();
    // The property, not the attribute — the password carries no explicit `tabindex` and must not
    // acquire one; 0 is what "still in the natural tab order" looks like.
    expect((screen.getByLabelText("Password") as HTMLInputElement).tabIndex).toBe(0);
  });
});

describe("the password form's error", () => {
  // The whole point of quince#497's 426: no password will ever work over this connection, so the
  // form has to offer somewhere else to go. Every OTHER failure here has an action the user can
  // take in this box, which is why this is the only one that gets a link.
  it("links to the HTTPS page on insecure_origin", async () => {
    renderForm(() => Promise.reject(new APIError(426, "insecure_origin", "this connection is not encrypted")));
    submit();

    expect(await screen.findByText(/this connection is not encrypted/i)).toBeInTheDocument();
    const link = screen.getByRole("link", { name: "How to fix this" });
    expect(link).toHaveAttribute("href", "/onboarding/https");
  });

  // THE DISCRIMINATING CASE, and the reason the link is keyed on the CODE rather than on
  // `window.location.protocol`. A wrong password over plain http is still just a wrong password:
  // the connection is fine as far as the cookie is concerned, and sending the user off to fix
  // their TLS would be a wild goose chase started by the login form.
  it("does not link on a wrong password, even though the message is also a 4xx", async () => {
    renderForm(() => Promise.reject(new APIError(401, "bad_password", "incorrect password")));
    submit();

    expect(await screen.findByText(/incorrect password/i)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "How to fix this" })).not.toBeInTheDocument();
  });

  // A non-APIError must still render, and must not acquire a link. The catch-all branch was the
  // only one before this change, so this pins that it survived the refactor.
  it("renders a plain Error with no link", async () => {
    renderForm(() => Promise.reject(new Error("network is down")));
    submit();

    expect(await screen.findByText(/network is down/i)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "How to fix this" })).not.toBeInTheDocument();
  });
});
