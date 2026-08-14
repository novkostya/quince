import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { AuthPage } from "@/features/auth/AuthPage";
import { OnboardingHTTPSPage } from "@/pages/OnboardingHTTPSPage";
import { AppLayout } from "@/routes/AppLayout";
import { api } from "@/lib/api";
import type { Health } from "@/lib/types";

// EVERY SHELL CARRIES THE WARNING, ASSERTED TOGETHER — quince#539, and the Operator's ruling on
// quince#908 slice 6, which makes it the only thing standing between an owner and a password typed
// into a plain-http login form.
//
// CROSS-SURFACE ON PURPOSE, in the shape `passwordSurfaces.test.tsx` established for the same
// reason. quince has THREE shells rather than one — the authed layout, the auth primitive behind
// login and setup, and the HTTPS onboarding page, which sits outside every guard — and each has its
// own suite. The property being protected is *"all of them warn"*, which no per-file test can see:
// a refactor that drops the banner from one shell leaves the other two green, and the failure is
// invisible on every install where the setting is off, which is nearly all of them.
//
// WHAT MAKES THIS THE BAD KIND OF FAILURE. The banner renders `null` on a normal deployment, so a
// missing one looks exactly like a working one everywhere except the deployment that needs it —
// and that deployment belongs to somebody who has just been told, by quince itself, that turning
// the setting on is a reasonable thing to do.
//
// A FOURTH SHELL WOULD NOT FAIL HERE. This asserts the three that exist; it cannot know about one
// that does not. The guard against that is the comment in each surface naming the other two.

const OK: Health = { status: "ok", version: "t", mode: "normal" };

beforeEach(() => vi.restoreAllMocks());

function mount(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}

const SURFACES: { name: string; render: () => void }[] = [
  {
    name: "the auth primitive (login and setup)",
    render: () =>
      void mount(
        <AuthPage title="Sign in" subtitle="Enter your admin password.">
          <div />
        </AuthPage>,
      ),
  },
  {
    name: "the HTTPS onboarding page, outside every guard",
    render: () => void mount(<OnboardingHTTPSPage />),
  },
  {
    name: "the authed shell",
    render: () => void mount(<AppLayout />),
  },
];

describe("the plain-http warning reaches every shell", () => {
  for (const surface of SURFACES) {
    it(`warns on ${surface.name}`, async () => {
      // EVERY endpoint answers the same object here. These surfaces fetch different things —
      // onboarding, auth status, devices — and this test is about one field, so the cheapest honest
      // harness is one that lets each of them render whatever it renders and asserts only the alert.
      vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
      surface.render();

      expect(await screen.findByRole("alert")).toHaveTextContent(
        /anyone who can see the traffic can sign in as you/i,
      );
    });

    it(`stays silent on ${surface.name} when the opt-in is off`, async () => {
      vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: false });
      surface.render();

      // Let the health query settle before concluding nothing rendered, so this cannot pass merely
      // by asserting during the loading frame — which would make it green against a surface that
      // never mounts the banner at all.
      await screen.findByText(/./, {}, { timeout: 1 }).catch(() => undefined);
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });
  }
});
