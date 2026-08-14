import { describe, it, expect, vi, beforeEach } from "vitest";
import { render as rtlRender, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";

import { AuthPage } from "./AuthPage";
import { SetupPasswordPage } from "@/pages/SetupPasswordPage";
import { LoginPage } from "@/pages/LoginPage";

// A QUERY CLIENT AROUND EVERY RENDER, BECAUSE THIS PRIMITIVE NOW CARRIES THE PLAIN-HTTP WARNING
// (quince#539). The second describe below already built one for the two PAGES it mounts; the shape
// assertions above it rendered `AuthPage` bare, which stopped being a configuration that ships.
//
// It changes nothing these tests assert: with no health answer the banner renders `null`, so `box()`
// still finds the wordmark's parent and the class assertions are untouched.
function render(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

// qn.6m slice 3 — ruling A on quince#841: the auth surface is a plain PAGE, not a card. D1 narrows
// it: the SETUP surface takes the page shape, and `/login` deliberately keeps its card because it is
// a recurring destination on an existing install rather than a first-run step.
//
// Asserted on the CLASS rather than a rendered pixel because jsdom computes no layout. That is a
// real limit of these tests and the reason story 1 pins the same thing in a browser.

function box(container: HTMLElement): HTMLElement {
  // The wordmark is the first thing inside the box in both variants, so its parent IS the box —
  // which avoids reaching for a test id on a purely presentational wrapper.
  const mark = screen.getByText("quince");
  expect(container).toContainElement(mark);
  return mark.parentElement as HTMLElement;
}

describe("the two shapes", () => {
  it("page: full width, no card chrome", () => {
    const { container } = render(
      <AuthPage variant="page" title="Set an admin password" subtitle="s">
        <div>fields</div>
      </AuthPage>,
    );
    const b = box(container);
    // `max-w-2xl` since 2026-08-13 (Operator): the onboarding flow is sized by its widest step,
    // which is the storage one — it renders the whole helper script and was clipping it at `xl`.
    expect(b.className).toContain("max-w-2xl");
    expect(b.className).not.toContain("rounded-card");
    expect(b.className).not.toContain("max-w-sm");
  });

  it("card: narrow, bordered — and it is the DEFAULT, so a caller that forgets stays as it was", () => {
    const { container } = render(
      <AuthPage title="Sign in" subtitle="s">
        <div>fields</div>
      </AuthPage>,
    );
    const b = box(container);
    expect(b.className).toContain("max-w-sm");
    expect(b.className).toContain("rounded-card");
    expect(b.className).not.toContain("max-w-xl");
  });

  it("the heading grows with the box", () => {
    const { unmount } = render(
      <AuthPage variant="page" title="T" subtitle="s">
        <div />
      </AuthPage>,
    );
    expect(screen.getByRole("heading", { level: 1 }).className).toContain("text-xl");
    unmount();

    render(
      <AuthPage title="T" subtitle="s">
        <div />
      </AuthPage>,
    );
    expect(screen.getByRole("heading", { level: 1 }).className).toContain("text-base");
  });
});

// A surface with several independent actions — the settings-side page in slice 6 — has no single
// thing to submit, and a <form> around it would make every Button a submit by default. That defect
// has been paid for twice already on this project (quince#824, quince#828), so the box is only a
// form when somebody actually passes a submit handler.
describe("the box is a form only when there is something to submit", () => {
  it("renders a form when onSubmit is given", () => {
    const { container } = render(
      <AuthPage title="T" subtitle="s" onSubmit={() => {}}>
        <div />
      </AuthPage>,
    );
    expect(container.querySelector("form")).not.toBeNull();
  });

  it("renders NO form when it is not", () => {
    const { container } = render(
      <AuthPage title="T" subtitle="s">
        <div />
      </AuthPage>,
    );
    expect(container.querySelector("form")).toBeNull();
  });
});

// THE RULING ITSELF, asserted through the real pages rather than the primitive — because the thing
// that can regress is a page forgetting to pass the variant, not the primitive forgetting to honour
// it. `variant` defaults to `card`, so a dropped prop is silent everywhere except here.
describe("which surface gets which shape", () => {
  beforeEach(() => vi.restoreAllMocks());

  // The provider moved to the shared `render` above, which every case in this file now goes
  // through; this keeps only the router these two pages need.
  function renderPage(el: ReactElement) {
    return render(<MemoryRouter>{el}</MemoryRouter>);
  }

  it("setup is a PAGE — ruling A", () => {
    const { container } = renderPage(<SetupPasswordPage />);
    expect(box(container).className).toContain("max-w-2xl");
    expect(box(container).className).not.toContain("rounded-card");
  });

  it("login is still a CARD — D1's narrowing of it", () => {
    const { container } = renderPage(<LoginPage />);
    expect(box(container).className).toContain("max-w-sm");
    expect(box(container).className).toContain("rounded-card");
  });
});
