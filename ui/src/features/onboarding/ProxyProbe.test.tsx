import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ProxyProbe } from "./ProxyProbe";
import { api } from "@/lib/api";

// THE FIRST TEST FILE THIS COMPONENT HAS HAD, which is the short explanation for both defects it
// covers: a field that ignored Enter, and an instruction to *"open this address"* rendered as
// unclickable monospace (quince#1066). Neither is visible to a type-checker and both are the kind of
// thing a suite notices for free once one exists.
//
// THE PROBE IS EXERCISED FOR REAL rather than mocked at the module boundary: `api.get` mints the
// nonce and `fetch` is what fails, which is the actual `unreachable` path. Mocking `runProbe` would
// assert that this component calls a function, which is not the thing that broke.

function typeAddress(value: string) {
  fireEvent.change(screen.getByLabelText(/address you will reach quince at/i), {
    target: { value },
  });
}

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "get").mockResolvedValue({ nonce: "n1" });
  // A REJECTED `fetch` IS THE `unreachable` BRANCH — the one the copy under test lives in.
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))),
  );
});

afterEach(() => vi.unstubAllGlobals());

describe("the proxy probe", () => {
  // ENTER, ASSERTED AS THE TWO FACTS THAT PRODUCE IT. jsdom does not implement implicit form
  // submission, so pressing Enter in a jsdom input fires nothing whatever the markup says — a test
  // that "pressed Enter" would pass against the broken version too. What Enter needs in a real
  // browser is a form with a submit control, so that is what is asserted: submitting runs the check,
  // and the button is that control.
  it("checks the address when the form is submitted", async () => {
    const { container } = render(<ProxyProbe />);
    typeAddress("quince.example.com");

    const form = container.querySelector("form");
    expect(form).not.toBeNull();
    fireEvent.submit(form!);

    expect(await screen.findByRole("status")).toHaveTextContent(/could not reach quince/i);
  });

  it("gives the form a submit button, which is what makes Enter work", () => {
    render(<ProxyProbe />);
    typeAddress("quince.example.com");

    expect(screen.getByRole("button", { name: /Check this address/i })).toHaveAttribute(
      "type",
      "submit",
    );
  });

  // THE ADDRESS THE COPY TELLS YOU TO OPEN IS A LINK. The sentence exists because the browser's own
  // error is the answer this check cannot see; leaving it as text asks somebody on a phone to retype
  // an address to find out what is wrong with it.
  it("links the address it tells you to open", async () => {
    const { container } = render(<ProxyProbe />);
    typeAddress("quince.example.com");
    fireEvent.submit(container.querySelector("form")!);

    await screen.findByRole("status");
    const link = screen.getByRole("link", { name: "https://quince.example.com" });
    expect(link).toHaveAttribute("href", "https://quince.example.com");
    expect(link).toHaveAttribute("target", "_blank");
  });
});
