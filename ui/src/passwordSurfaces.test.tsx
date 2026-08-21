import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { PasswordForm } from "@/features/auth/PasswordForm";
import { PasswordControls } from "@/features/settings/PasswordControls";
import { EncryptionDialog } from "@/features/devices/EncryptionDialog";
import { UnlockDialog } from "@/features/vault/UnlockDialog";
import { api } from "@/lib/api";
import { useOpsStore } from "@/stores/ops";
import type { Version } from "@/lib/types";

// THE THREE PASSWORD SURFACES, ASSERTED TOGETHER — quince#819's follow-up comment, which flagged
// this as *"worth its own issue if anyone wants it"* and left it unfiled.
//
// CROSS-FEATURE ON PURPOSE. These live in three different features and each already has its own
// suite, and that is exactly how the regression this file was written for got in: `PasswordControls`
// alone drifted to `className="hidden" aria-hidden="true"` while the other two kept the ruled shape,
// and no per-file test could see that one of three had moved. The property is *"all password
// surfaces agree"*, so it is asserted in one place that fails when any of them stops.
//
// WHAT IS BEING PROTECTED, AND WHY IT IS THE BAD KIND OF FAILURE. The browser's save prompt depends
// on two things that read as implementation detail: the password fields sitting inside a `<form>`
// that emits `submit`, and a location change after it. Operator-measured on a phone, 2026-08-12:
// Safari prompts on the SPA route change alone, so quince needs no `window.location.href` reload —
// and every internal navigation here is client-side, so there is no full-load path that could cover
// for the SPA one if it broke.
//
// `PasswordForm` calls `e.preventDefault()`, which makes the `<form>` look decorative: swapping it
// for a `<div>` with a button handler is the obvious tidy-up, every existing test stays green
// (they all click the button), sign-in keeps working perfectly — and the save prompt simply never
// appears again. Nobody notices for months and there is nothing to bisect against, because no
// behaviour changed. Hence `fireEvent.submit(form)` below rather than a click: a click passes
// against a `<div>`, a submit event does not.
//
// It proves nothing about Safari. It stops the one refactor that would guarantee Safari never
// prompts, which is the most a unit test can do here.

beforeEach(() => {
  vi.restoreAllMocks();
  useOpsStore.setState({ byId: {} });
});

// Each case returns the handler spy, so the shared assertions below can be identical across three
// components with quite different mounting requirements.
type Surface = {
  name: string;
  mount: () => { handler: { mock: { calls: unknown[] } } };
  fill: () => void;
  // The anchor is labelled on every surface, which is itself part of the claim — see below.
  anchorLabel: string;
  anchorValue: string;
};

const surfaces: Surface[] = [
  {
    name: "sign in / first run — features/auth/PasswordForm",
    anchorValue: "quince-admin",
    anchorLabel: "Username",
    mount: () => {
      const onSubmit = vi.fn().mockResolvedValue(undefined);
      // A QUERY CLIENT, as the other two surfaces already build: `AuthPage` now carries the
      // plain-http warning (quince#539) and this form renders inside it. The banner is silent with
      // no health answer, so nothing this file asserts changes.
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      render(
        <QueryClientProvider client={qc}>
          <MemoryRouter>
            <PasswordForm title="Sign in" subtitle="Enter your admin password." cta="Sign in" onSubmit={onSubmit} />
          </MemoryRouter>
        </QueryClientProvider>,
      );
      return { handler: onSubmit };
    },
    fill: () => {
      fireEvent.change(screen.getByLabelText("Password"), { target: { value: "hunter2" } });
    },
  },
  {
    name: "admin change — features/settings/PasswordControls",
    anchorValue: "quince-admin",
    anchorLabel: "Username",
    mount: () => {
      vi.spyOn(api, "get").mockResolvedValue({
        passkeys: [],
        rp_id: "quince.example.com",
        supported: true,
        has_password: true,
      });
      const put = vi.spyOn(api, "put").mockResolvedValue(undefined);
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      render(
        <QueryClientProvider client={qc}>
          <PasswordControls />
        </QueryClientProvider>,
      );
      return { handler: put };
    },
    fill: () => {
      fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old" } });
      fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new" } });
    },
  },
  {
    name: "backup password — features/devices/EncryptionDialog",
    anchorValue: "family-iphone",
    anchorLabel: "Device",
    mount: () => {
      const post = vi.fn().mockResolvedValue({ op_id: "OP1" });
      render(
        <EncryptionDialog udid="DEV-1" deviceName="family-iphone" encryption="on" open onOpenChange={() => {}} post={post} />,
      );
      return { handler: post };
    },
    fill: () => {
      fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old-pw" } });
      fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new-pw" } });
      fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "new-pw" } });
    },
  },
  {
    // THE FOURTH SURFACE, AND IT SHARES A CREDENTIAL WITH THE THIRD. The backup password is
    // SET in EncryptionDialog and TYPED here, so both anchor on `deviceName || udid` — the
    // same value, deliberately. `anchorValue` is identical to the surface above for that
    // reason, and the assertion below that they agree is the point rather than a coincidence.
    name: "unlock a backup — features/vault/UnlockDialog",
    anchorValue: "family-iphone",
    anchorLabel: "Device",
    mount: () => {
      const post = vi.fn().mockResolvedValue({ id: "S1", version_id: "01V", expires_at: "" });
      vi.spyOn(api, "post").mockImplementation(post);
      render(
        <UnlockDialog
          version={{ id: "01V", udid: "DEV-1", encrypted: true } as Version}
          deviceName="family-iphone"
          open
          onOpenChange={() => {}}
          onUnlocked={() => {}}
        />,
      );
      return { handler: post };
    },
    fill: () => {
      fireEvent.change(screen.getByLabelText("Backup password"), { target: { value: "backup-pw" } });
    },
  },
];

describe.each(surfaces)("$name", (surface) => {
  // THE FORM ANCESTOR, PER FIELD RATHER THAN PER SURFACE. A surface can grow a second password
  // input outside the form — the manager then sees a field it cannot attribute — so this walks every
  // password-token input rather than checking that some form exists somewhere on screen.
  it("keeps every password field inside a form", async () => {
    surface.mount();
    await waitFor(() => expect(screen.getAllByLabelText(/password/i).length).toBeGreaterThan(0));

    const fields = document.querySelectorAll<HTMLInputElement>('input[type="password"]');
    expect(fields.length).toBeGreaterThan(0);
    for (const f of fields) {
      expect(f.closest("form"), `${f.id || f.name || "a password field"} is outside any <form>`).not.toBeNull();
    }
  });

  // SUBMIT, NOT CLICK. This is the assertion the existing suites cannot make: they all click the
  // button, which works just as well against a `<div>` with an onClick. Only a `submit` event
  // distinguishes a real form, and only a real form makes the browser offer to save.
  it("runs its handler on a form submit event, not merely on a button click", async () => {
    const { handler } = surface.mount();
    await waitFor(() => expect(screen.getAllByLabelText(/password/i).length).toBeGreaterThan(0));
    surface.fill();

    const form = document.querySelector<HTMLFormElement>("form");
    expect(form, "no <form> on this surface at all").not.toBeNull();
    fireEvent.submit(form as HTMLFormElement);

    await waitFor(() => expect(handler.mock.calls.length, "the submit event ran no handler").toBeGreaterThan(0));
  });

  // THE ANCHOR IS EXPOSED, WHICH IS WHERE `PasswordControls` HAD DRIFTED. It carried
  // `className="hidden" aria-hidden="true"` with no label — `display:none` plus removal from the
  // accessibility tree — and quince#819 ruled against exactly that variant: the report came from
  // Safari, no authoritative WebKit source was found either way, and a `display:none` field is the
  // one most likely to be ignored.
  //
  // ASSERTED STRUCTURALLY, NOT WITH `toBeVisible()`, and this is the trap worth knowing. **jsdom
  // loads no Tailwind stylesheet**, so `className="hidden"` computes to nothing here and
  // `toBeVisible()` passes happily on the exact markup this test exists to reject. What jsdom CAN
  // see is the accessibility tree: an `aria-hidden` field has no accessible name, so `getByLabelText`
  // does not find it. That is the assertion, plus the class itself, pinned because the tell is
  // invisible to computed style in this environment.
  //
  // THE LABEL AND VALUE ARE PER-SURFACE, WHICH IS THE POINT RATHER THAN AN EXCEPTION. Two surfaces
  // anchor on the admin (`Username` / `quince-admin`, a constant because quince is single-admin) and
  // the third on the device (`Device` / its name, falling back to the udid). What must agree across
  // all three is the `username` autocomplete token and the exposed, read-only, out-of-tab-order
  // shape — a password manager keys on the token, not on our label.
  it("exposes the username anchor rather than hiding it", async () => {
    surface.mount();

    const anchor = await screen.findByLabelText(surface.anchorLabel);
    expect(anchor).toHaveValue(surface.anchorValue);
    expect(anchor).toHaveAttribute("autocomplete", expect.stringContaining("username"));
    expect(anchor).not.toHaveAttribute("aria-hidden");
    expect(anchor.className.split(/\s+/)).not.toContain("hidden");
    // `readOnly`, never `disabled`: a disabled control is excluded from submission and skipped by
    // autofill, which would defeat the field's whole purpose (quince#819). Out of the tab order so
    // it cannot take the focus ring ahead of the field the user types in (quince#824).
    expect(anchor).toHaveAttribute("readonly");
    expect(anchor).not.toBeDisabled();
    expect(anchor).toHaveAttribute("tabindex", "-1");
  });
});

// THE TWO BACKUP-PASSWORD SURFACES MUST ANCHOR ON THE SAME VALUE, and this is the dimension
// the per-surface checks above cannot see.
//
// Those assert the SHAPE of an anchor — present, labelled, `autocomplete="username"`, not
// hidden. A fourth surface satisfies all of that while anchoring on something else entirely,
// and the result is silent: the browser files saved passwords under (origin, username), so two
// anchors mean two entries for one secret. Encryption keeps working. Unlock keeps working.
// Every test here stays green. The password the user saved when they turned encryption on is
// simply never offered on the screen that asks for it, with no error and nothing to bisect.
//
// That is one level above the drift this file was written for — `PasswordControls` moving
// alone to `aria-hidden` — and it is the same lesson: the property is "these agree", so it has
// to be asserted somewhere that fails when one of them stops.
it("anchors both backup-password surfaces on the same credential", () => {
  const backupSurfaces = surfaces.filter((s) => s.anchorLabel === "Device");
  expect(
    backupSurfaces.length,
    "expected the encryption dialog and the unlock dialog, both anchored on the device",
  ).toBe(2);

  const [first, ...rest] = backupSurfaces;
  for (const s of rest) {
    expect(
      s.anchorValue,
      `${s.name} anchors on ${s.anchorValue} while ${first.name} anchors on ${first.anchorValue}; ` +
        "the browser would file two saved passwords for one backup password",
    ).toBe(first.anchorValue);
  }
});
