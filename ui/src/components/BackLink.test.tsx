import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { BackLink, clearNavigationHistory, useTrackNavigation } from "./BackLink";

// THE TWO BRANCHES THAT DECLINE TO TRAVERSE ARE WHAT THIS FILE IS FOR.
//
// The happy path — the previous entry is the destination, so `< Home` goes back and the browser
// restores the offset — is covered end to end by `story12`. What was covered by nothing was either
// guard, and the second one is not a nicety: without `idx > 0`, `navigate(-1)` on a freshly loaded
// tab **leaves the application**. A user taps a link inside quince and ends up wherever they were
// before quince. That is a one-expression guard between the product and the exit, and it was
// asserted only by hand (quince#869 review).
//
// DRIVEN BY RENDERING AT PATHS RATHER THAN BY NAVIGATING. `useTrackNavigation` reads `useLocation`
// and writes MODULE state, so a sequence of renders accumulates exactly as a sequence of
// navigations does — and it lets each case state its history in one line instead of clicking its
// way there. `clearNavigationHistory` between cases is what makes that honest; without it the
// second test inherits the first one's idea of where it came from, which is the leakage that helper
// was written for.
//
// `useNavigate` IS THE PROBE. `<Link>` preventDefaults and pushes on its own, so `defaultPrevented`
// cannot tell a traversal from an ordinary click — but only this component calls `navigate(-1)`, so
// that call IS the decision. Mocking the export does not disturb `<Link>`: it resolves its own
// internal binding rather than the one this module imports.
const navigate = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => navigate };
});

function Tracker() {
  useTrackNavigation();
  return null;
}

// One render per navigation, in order. The final entry is where the back link is rendered.
function navigateThrough(...pathnames: string[]): void {
  for (const pathname of pathnames) {
    const view = render(
      <MemoryRouter initialEntries={[pathname]}>
        <Tracker />
      </MemoryRouter>,
    );
    view.unmount();
  }
}

function clickBackTo(to: string): void {
  render(
    <MemoryRouter initialEntries={["/wherever"]}>
      <BackLink to={to}>Home</BackLink>
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByRole("link", { name: "Home" }), { button: 0 });
}

beforeEach(() => {
  navigate.mockReset();
  clearNavigationHistory();
  // A session with a predecessor. React Router's history writes this; jsdom starts with `null`
  // state, which is itself the deep-link shape and is asserted explicitly below.
  window.history.replaceState({ idx: 1 }, "");
});

describe("BackLink", () => {
  it("traverses when the previous entry is the destination it names", () => {
    navigateThrough("/", "/devices/UDID-1");
    clickBackTo("/");
    expect(navigate).toHaveBeenCalledWith(-1);
  });

  // THE LABEL WOULD OTHERWISE LIE. Home → device → storage: going back from the storage page lands
  // on the DEVICE page, while the control says Home. Pushing is the honest answer, and landing at
  // the top of a page you have not seen before is not a defect.
  it("pushes instead when the previous entry is somewhere else", () => {
    navigateThrough("/", "/devices/UDID-1", "/storage/internal");
    clickBackTo("/");
    expect(navigate).not.toHaveBeenCalledWith(-1);
  });

  // THE ONE THAT MATTERS MOST. A deep-linked or reloaded tab has no entry of ours to go back to, and
  // `navigate(-1)` would walk out of the app. `previousPathname` cannot catch this on its own: a
  // reload wipes this module's memory while the browser's history survives, so the two guards fail
  // in different situations and neither substitutes for the other.
  it("pushes on a freshly loaded tab, where going back would leave the app", () => {
    window.history.replaceState({ idx: 0 }, "");
    navigateThrough("/", "/devices/UDID-1");
    clickBackTo("/");
    expect(navigate).not.toHaveBeenCalledWith(-1);
  });

  // The same case as above with the state absent entirely rather than zeroed — what a document that
  // React Router's history has never written to actually looks like.
  it("pushes when there is no history state at all", () => {
    window.history.replaceState(null, "");
    navigateThrough("/", "/devices/UDID-1");
    clickBackTo("/");
    expect(navigate).not.toHaveBeenCalledWith(-1);
  });

  // A tap is button 0. Everything else is "open this somewhere", which is the browser's to answer.
  it("leaves a modifier click alone", () => {
    navigateThrough("/", "/devices/UDID-1");
    render(
      <MemoryRouter initialEntries={["/devices/UDID-1"]}>
        <BackLink to="/">Home</BackLink>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("link", { name: "Home" }), { button: 0, metaKey: true });
    expect(navigate).not.toHaveBeenCalledWith(-1);
  });
});
