import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { ConfigView } from "./ConfigView";
import type { ConfigResponse } from "@/lib/types";

// WHAT THIS PINS, AND WHAT IT CANNOT: quince#631.
//
// On a phone, Settings scrolled sideways. A single long config value widened this pane, its grid
// column and the whole content area, sliding the editor's fields off the left edge. The cause is
// two-sided: a grid item defaults to `min-width: auto` so the column could not shrink, and this
// pane did not wrap. `min-w-0` is asserted in `SettingsPage.test.tsx`; the wrapping is asserted
// here, because NEITHER HALF WORKS ALONE and a test for one is not a test for the pair.
//
// THESE ARE CLASS ASSERTIONS AND THEY CANNOT PROVE THE PAGE STOPS SCROLLING. jsdom has no layout —
// it computes no widths, so nothing at this layer can observe an overflow. Only a browser at a
// narrow viewport can, and there is no mobile-viewport pass in ui-e2e (noted on the issue: this is
// the third mobile-only defect in three days and all three passed every test). What these catch is
// the regression that is actually likely: someone tidying the class list and taking the wrap with
// it, leaving `overflow-auto` looking like it does something.
function response(over: Partial<ConfigResponse> = {}): ConfigResponse {
  return {
    config: {
      backup: { transport: "auto", require_encryption: true },
      storage: null,
      sessions: { ttl_minutes: 60, allow_insecure_transport: false },
      ui: { theme: "system" },
    },
    warnings: [],
    source: { path: "/data/config.yml", mtime: null },
    ...over,
  } as ConfigResponse;
}

describe("ConfigView does not widen the page", () => {
  it("wraps the config dump instead of scrolling it sideways", () => {
    const { container } = render(<ConfigView data={response()} />);
    const cls = container.querySelector("pre")?.className ?? "";
    expect(cls).toContain("whitespace-pre-wrap");
    expect(cls).toContain("break-words");
  });

  // `overflow-auto` STAYS, and this is not redundant with the wrap. `break-words` breaks a token
  // that is too long for its line, but the pane keeps its own scroll for whatever still cannot
  // break. Deleting it because "we wrap now" would remove the last containment.
  it("keeps its own overflow as the net under the wrap", () => {
    const { container } = render(<ConfigView data={response()} />);
    expect(container.querySelector("pre")?.className ?? "").toContain("overflow-auto");
  });

  // The other two arbitrary-length strings on this page. The config PATH and a warning's
  // path+message are server-supplied and unbounded, so they are the same class of content as the
  // dump and get the same treatment — the issue's point that this is a class, not a page.
  it("wraps the source path, which is arbitrary-length too", () => {
    const { container } = render(<ConfigView data={response()} />);
    // `div.font-mono`, not `div` — a plain `div` selector matches the wrapper first, whose
    // textContent also contains the path, and the assertion then passes or fails on an ancestor
    // that was never the subject.
    const path = [...container.querySelectorAll("div.font-mono")].find((d) =>
      d.textContent?.includes("/data/config.yml"),
    );
    expect(path).toBeDefined();
    expect(path?.className ?? "").toContain("break-words");
  });

  it("wraps the configuration-warnings list", () => {
    const { container } = render(
      <ConfigView
        data={response({
          warnings: [{ path: "storage.zfs.hook_cmd", message: "unknown key" }],
        } as Partial<ConfigResponse>)}
      />,
    );
    expect(container.querySelector("ul")?.className ?? "").toContain("break-words");
  });
});
