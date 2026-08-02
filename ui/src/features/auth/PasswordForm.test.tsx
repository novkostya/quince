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
