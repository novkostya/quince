import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import type { SessionOverview } from "@/lib/types";
import { UnlockedContents } from "./UnlockedContents";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).

const NOTES = "com.example.notes";
const READER = "com.example.reader";

function ov(over: Partial<SessionOverview> = {}): SessionOverview {
  return {
    capabilities: [],
    adapter_version: "test",
    warnings: [],
    unsupported_reason: null,
    page: {
      items: [
        { domain: `AppDomain-${NOTES}`, files: 10, bytes: 1000 },
        { domain: "HomeDomain", files: 5, bytes: 500 },
      ],
    },
    totals: { files: 15, bytes: 1500, domain_count: 2 },
    ...over,
  };
}

describe("UnlockedContents", () => {
  // STORY 3 — the hazard is a zero, not a blank. A size that is not known yet must not read
  // as a measurement, and every app on the screen would be wrong for the length of the walk.
  it("shows sizes as counting rather than as zero while the walk is running", () => {
    render(<UnlockedContents overview={null} bundleIDs={[NOTES, READER]} loading />);
    expect(screen.getAllByText("counting…").length).toBeGreaterThan(0);
    expect(screen.queryByText("0 B")).not.toBeInTheDocument();
  });

  // Still pending while pages are arriving, even though SOME rows are in hand — a partial
  // total presented as the total is the thing this guards.
  it("stays pending while more pages are still coming", () => {
    render(<UnlockedContents overview={ov()} bundleIDs={[NOTES]} loading />);
    expect(screen.getAllByText("counting…").length).toBeGreaterThan(0);
  });

  // STORY 4 / G3 — apps plus the remainder equal the total, and the remainder is a NAMED row
  // rather than a silent gap.
  it("shows the remainder as its own row once settled", () => {
    const { container } = render(
      <UnlockedContents overview={ov()} bundleIDs={[NOTES]} loading={false} />,
    );
    expect(screen.getByText("Everything else")).toBeInTheDocument();
    // HomeDomain's bytes are not an app's and must be visible as the remainder.
    // textContent rather than getByText: the app row deliberately splits size and file
    // count across elements, so an element-equality matcher cannot see either alone.
    expect(container.textContent).toContain("500 B");   // the remainder
    expect(container.textContent).toContain("1.0 KB");  // the app
  });
  // A DISAGREEMENT IS DISCLOSED. If the walk finished and the arithmetic does not reconcile,
  // the screen says so rather than showing a plausible wrong total.
  it("says so when the rows do not add up to the server's totals", () => {
    const bad = ov({ totals: { files: 999, bytes: 9999, domain_count: 2 } });
    render(<UnlockedContents overview={bad} bundleIDs={[NOTES]} loading={false} />);
    expect(screen.getByText(/do not add up/)).toBeInTheDocument();
  });

  // An installed app with no data is distinct from a small one, and from a pending one.
  it("distinguishes an app with no data from one that is still counting", () => {
    render(<UnlockedContents overview={ov()} bundleIDs={[NOTES, READER]} loading={false} />);
    expect(screen.getByText("no data in this backup")).toBeInTheDocument();
    expect(screen.queryByText("counting…")).not.toBeInTheDocument();
  });

  // STORY 2 — the app list is the SAME list the locked screen showed. It is rendered from the
  // pre-unlock bundle ids, so it cannot disappear and come back when the session opens.
  it("renders every installed app even before any sizes exist", () => {
    render(<UnlockedContents overview={null} bundleIDs={[NOTES, READER]} loading />);
    expect(screen.getByText(NOTES)).toBeInTheDocument();
    expect(screen.getByText(READER)).toBeInTheDocument();
  });
});
