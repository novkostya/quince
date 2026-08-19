import { describe, it, expect, beforeEach } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { DeviceCard } from "./DeviceCard";
import type { DueState } from "@/lib/backupDue";
import { useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";
import type { Device, Job } from "@/lib/types";

// qn.4c findings (iv)+(v): the dashboard card must narrate the phase it is actually in — NOT
// linger on "Backing up 100%" through verify+commit — and must show a device's real last backup
// instead of "No backups yet" once the server tells the truth (`last_backup` derived from the
// newest committed version, contracts §2 (bz)).

function device(over: Partial<Device> = {}): Device {
  return {
    udid: "DEV-1",
    name: "test-iphone",
    model: "iPhone16,1",
    ios_version: "26.0.1",
    transports: { usb: "2026-07-20T00:00:00Z" },
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "unknown",
    notifications_enabled: true,
    last_seen: "2026-07-20T00:00:00Z",
    last_backup: null,
    ...over,
  };
}

function job(state: Job["state"], percent: number | null): Job {
  return {
    id: "J1",
    udid: "DEV-1",
    kind: "backup",
    transport: "usb",
    state,
    progress: {
      phase: state,
      percent,
      bytes_done: 2,
      bytes_total: 2,
      files_received: 9,
      liveness: "active",
    },
    started_at: "2026-07-20T00:00:00Z",
    finished_at: null,
    error: null,
    retry_of: null,
    intent_id: "J1",
    attempt: 1,
    version_id: null,
    storage_id: null,
  };
}

function renderCard(dev: Device, due?: DueState) {
  return render(
    <MemoryRouter>
      <DeviceCard device={dev} due={due} />
    </MemoryRouter>,
  );
}

describe("DeviceCard", () => {
  beforeEach(() => {
    useJobsStore.setState({ byId: {}, logByJobId: {} });
    useVersionsStore.setState({ byId: {}, order: [] });
  });

  it("narrates verify and commit instead of lingering on 'Backing up' at 100% (finding iv)", () => {
    useJobsStore.getState().upsert(job("backing_up", 100));
    renderCard(device());
    // WAS `getByText("Backing up")`, and the new expectation serves this test's own title better
    // than the old one did. A job at 100% that is still `backing_up` now reads "Finishing up":
    // idevicebackup2 latches its progress once the device reports 100 and goes on working
    // (idevicebackup2.c:2523, tag 1.4.0), so "Backing up" beside a full bar WAS the lingering this
    // test is named for. Measured on the lab rig 2026-08-16: 50 s in that state, of which verify
    // and commit were 3 s (quince#808).
    expect(screen.getByText("Finishing up")).toBeTruthy();
    expect(screen.queryByText("Backing up")).toBeNull();

    // The live WS path: a job.updated arrives, the store changes, the card re-renders itself.
    act(() => useJobsStore.getState().upsert(job("verifying", 100)));
    expect(screen.queryByText("Backing up")).toBeNull();
    expect(screen.getByText("Verifying")).toBeTruthy();

    act(() => useJobsStore.getState().upsert(job("committing", 100)));
    expect(screen.getByText("Committing")).toBeTruthy();
  });

  it("shows the real last backup once the job is done, not 'No backups yet' (finding v)", () => {
    useJobsStore.getState().upsert({ ...job("succeeded", 100), finished_at: "2026-07-20T01:00:00Z" });
    renderCard(device({ last_backup: { at: "2026-07-20T01:00:00Z", job_id: "J1", status: "succeeded" } }));
    expect(screen.queryByText(/no backups yet/i)).toBeNull();
    expect(screen.getByText(/last backup/i)).toBeTruthy();
    expect(screen.queryByText("Backing up")).toBeNull();
  });

  it("says 'No backups yet' only when the device really has none", () => {
    renderCard(device());
    expect(screen.getByText(/no backups yet/i)).toBeTruthy();
  });

  it("renders a last backup derived from an adopted version (null job_id, contracts §2)", () => {
    renderCard(device({ last_backup: { at: "2026-07-19T00:00:00Z", job_id: null, status: "succeeded" } }));
    expect(screen.getByText(/last backup/i)).toBeTruthy();
  });

  // qn.6a offline devices: a device with no transports shows an "Offline" badge, a last-seen line,
  // and a DISABLED "Back up now" that still explains why (never a dead button, Operator ruling (ch)).
  // THE REASON IS THE BUTTON'S LABEL, and the ruling it serves is unchanged: never a dead button
  // (quince#1202). What changed is where the reason lives — a caption line above it cost 25px, and
  // measured, that was the only thing in normal operation raising the device row.
  //
  // `aria-disabled` RATHER THAN `disabled` IS THE ASSERTION THAT MATTERS. A truly disabled button
  // leaves the tab order and screen readers skip it, so putting the reason inside one would delete
  // it for the users who cannot see the styling. Pinning both halves — the attribute present, the
  // DOM property false — is what stops a future edit "tidying" this back to `disabled`.
  it("an offline device carries the reason in the button, focusably", () => {
    renderCard(
      device({ transports: {}, last_seen: "2026-07-19T00:00:00Z", last_backup: { at: "2026-07-19T00:00:00Z", job_id: "J0", status: "succeeded" } }),
    );
    expect(screen.getByText("Offline")).toBeTruthy();
    expect(screen.getByText(/last seen/i)).toBeTruthy();
    const btn = screen.getByTestId("card-backup-now") as HTMLButtonElement;
    expect(btn.textContent).toMatch(/connect to back it up/i);
    expect(btn.getAttribute("aria-disabled")).toBe("true");
    expect(btn.disabled).toBe(false); // focusable, so the reason is reachable
    expect(screen.queryByText(/connect it over usb or wi-fi/i)).toBeNull();
  });

  // qn.6a #6 (CORE): a failed newest attempt must be visible — the card shows a "needs attention"
  // line with a Retry, while last_backup still shows the older SUCCESS (a soak whose failures are
  // invisible is worthless, (cj)).
  it("surfaces a needs-attention Retry when the newest attempt failed", () => {
    useJobsStore.getState().upsert({
      ...job("connection_lost", 41),
      id: "J9",
      started_at: "2026-07-21T00:00:00Z",
      finished_at: "2026-07-21T00:01:00Z",
    });
    renderCard(device({ last_backup: { at: "2026-07-20T00:00:00Z", job_id: "J1", status: "succeeded" } }));
    expect(screen.getByText(/needs attention/i)).toBeTruthy();
    expect(screen.getByTestId("card-retry")).toBeTruthy();
    // Retry is the SINGLE primary action — it REPLACES "Back up now", not sits beside it (soak fix).
    expect(screen.queryByTestId("card-backup-now")).toBeNull();
    // last_backup (last SUCCESS) is still shown — the failure line is context, not a mutation.
    expect(screen.getByText(/last backup/i)).toBeTruthy();
  });

  // No needs-attention line when the newest attempt succeeded.
  it("shows no needs-attention line when the newest attempt succeeded", () => {
    useJobsStore.getState().upsert({ ...job("succeeded", 100), id: "J8", finished_at: "2026-07-21T00:01:00Z" });
    renderCard(device({ last_backup: { at: "2026-07-21T00:01:00Z", job_id: "J8", status: "succeeded" } }));
    expect(screen.queryByText(/needs attention/i)).toBeNull();
  });

  // The card's "N backups" count comes from the versions store (non-missing versions for this udid).
  it("counts the device's non-missing versions", () => {
    act(() => {
      useVersionsStore.getState().replaceAll([
        { ...ver("V1", "DEV-1"), is_latest: true },
        ver("V2", "DEV-1"),
        { ...ver("V3", "DEV-1"), missing: true }, // a dead version doesn't count as a backup you have
        ver("VX", "OTHER"),
      ]);
    });
    renderCard(device());
    expect(screen.getByText(/^2 backups$/)).toBeTruthy();
  });
});

function ver(id: string, udid: string) {
  return {
    id,
    udid,
    backend: "reflink" as const,
    zfs_snapshot: null,
    browse_root: `/backups/${udid}/latest`,
    created_at: "2026-07-20T00:00:00Z",
    job_id: "J1",
    kind: "full" as const,
    encrypted: true,
    is_latest: false,
    structure_verified_at: "2026-07-20T00:00:00Z",
    content_verified_at: null,
    logical_bytes: 100,
    missing: false,
    storage_id: null,
  };
}

// THE BADGE HAD NO TEST AT ALL, IN EITHER DIRECTION, AND THAT IS THE FINDING (quince#625).
//
// The issue expected to trade one assertion for another — *"a test asserting `Encrypted` renders
// comes out; a test asserting `Not encrypted` renders must stay and is the one that matters."*
// Neither existed. The only `Encrypted` / `Not encrypted` assertions in the suite are in
// `OnboardingHTTPSPage.test.tsx`, which is about HTTPS transport encryption — a different subject
// that happens to share the words, and is untouched by this change.
//
// So nothing was removed here and this is net-new coverage on the branch that now carries the whole
// user consequence. `off` is the only state that renders anything; if it ever stops, a device whose
// backups are unencrypted becomes indistinguishable on Home from one that is fine.
describe("DeviceCard encryption badge", () => {
  beforeEach(() => {
    useJobsStore.setState({ byId: {}, logByJobId: {} });
    useVersionsStore.setState({ byId: {}, order: [] });
  });

  // THE ONE THAT MATTERS. The card must interrupt for an unencrypted device.
  it("warns when backups are NOT encrypted", () => {
    renderCard(device({ backup_encryption: "off" }));
    expect(screen.getByText("Not encrypted")).toBeTruthy();
  });

  // Quiet when healthy — the whole point of quince#625. An `ok` badge in the loudest slot on the
  // card reported the expected state of a standing setting and never changed between visits.
  it("says nothing when backups ARE encrypted", () => {
    renderCard(device({ backup_encryption: "on" }));
    expect(screen.queryByText("Encrypted")).toBeNull();
    expect(screen.queryByText("Not encrypted")).toBeNull();
  });

  // `unknown` rendered nothing before this change and still does. Asserted so the two silent states
  // are pinned as silent for DIFFERENT reasons, and a future edit cannot quietly give one a badge.
  it("says nothing when the encryption state is unknown", () => {
    renderCard(device({ backup_encryption: "unknown" }));
    expect(screen.queryByText("Encrypted")).toBeNull();
    expect(screen.queryByText("Not encrypted")).toBeNull();
  });
});

// THE BUTTON IS THE LAST CHILD OF EVERY BRANCH — quince#512, ruled option (a).
//
// `mt-auto` pins the WRAPPER to the card bottom and says nothing about where the button sits inside
// it. The branches disagreed — offline `[Button, reason]`, attention `[message, Button, error?]`,
// default `[Button, error?]` — so the wrapper's bottom edge aligned and the button's did not,
// across three baselines. Two cards side by side put the same control at visibly different heights.
//
// THIS IS THE INVARIANT A FUTURE BRANCH WILL BREAK, which is why it is asserted structurally rather
// than per-branch: whatever the action area renders, its LAST element must be the control. A new
// branch that appends a caption fails here instead of on the Operator's screen.
//
// WHAT IT CANNOT DO: jsdom has no layout, so this cannot measure that two cards' buttons share a
// baseline. It asserts the ordering the alignment is derived FROM. The defect was reported from a
// screen and every test passed at the time — that is the shape of it, and a visual check is
// quince#371's territory, not this test's.
function actionArea(container: HTMLElement): HTMLElement {
  const el = container.querySelector(".mt-auto");
  if (!el) throw new Error("no action area — the mt-auto wrapper is what pins the row");
  return el as HTMLElement;
}

function lastControl(container: HTMLElement): Element | null {
  const area = actionArea(container);
  // The branch either renders the control directly or wraps it in a flex column.
  const inner = area.children.length === 1 && area.firstElementChild?.tagName === "DIV"
    ? (area.firstElementChild as HTMLElement)
    : area;
  return inner.lastElementChild;
}

describe("DeviceCard action area ends with its button", () => {
  beforeEach(() => {
    useJobsStore.setState({ byId: {}, logByJobId: {} });
    useVersionsStore.setState({ byId: {}, order: [] });
  });

  it("default branch: the button is last", () => {
    const { container } = renderCard(device());
    expect(lastControl(container)?.tagName).toBe("BUTTON");
  });

  // THE REPORTED PAIR. An offline card and an online card side by side was the screenshot.
  it("offline branch: the button is last and IS the reason", () => {
    const { container } = renderCard(device({ transports: {} }));
    const last = lastControl(container);
    expect(last?.tagName).toBe("BUTTON");
    // The ruling is a button WITH a reason, never a dead one — the reason is now the label itself,
    // so the alignment invariant (every branch ENDS with its button) holds with nothing above it.
    expect(last?.textContent).toMatch(/connect to back it up/i);
  });

  it("attention branch: the button is last, below its message", () => {
    useJobsStore.getState().upsert(job("failed", 41));
    const { container } = renderCard(device());
    const last = lastControl(container);
    expect(last?.tagName).toBe("BUTTON");
    expect(last?.textContent).toMatch(/retry/i);
    expect(screen.getByText(/needs attention/i)).toBeTruthy();
  });

  it("pair branch: unchanged, the control is already last", () => {
    const { container } = renderCard(device({ paired: "no" }));
    // asChild renders a Link, so the control is an anchor here rather than a button.
    expect(lastControl(container)?.textContent).toMatch(/pair/i);
  });

  // THE ACTIVE BRANCH IS DELIBERATELY EXEMPT. A card that is backing up has no button at all and is
  // in a different state — it must not acquire one to satisfy an alignment rule.
  it("active branch renders progress and no button, on purpose", () => {
    useJobsStore.getState().upsert(job("backing_up", 50));
    const { container } = renderCard(device());
    expect(actionArea(container).querySelector("button")).toBeNull();
  });
});

// quince#836 REGRESSION GUARD, and it is structural rather than cosmetic. The card is a grid item,
// and a grid item defaults to `min-width: auto` — so it will not shrink below the intrinsic width
// of its widest line. A model name is arbitrary-length text off the wire; when the map learned
// "iPad Pro 11-inch (3rd generation)" the card stopped fitting, took its whole grid column with it,
// and Home scrolled sideways by 55px at 320px. The `truncate` on the subtitle could not help,
// because it cannot engage while the card itself is free to widen.
//
// jsdom HAS NO LAYOUT, so nothing here can prove the page stopped scrolling — only `story12` at a
// real 320px viewport does that, and it is what caught this. What this asserts is the CLASS, which
// is what quince#631 settled on one page over for exactly the same reason: a bare `min-w-0` with no
// test reads as decoration and gets tidied away by a later pass.
describe("DeviceCard grid containment", () => {
  it("can shrink below its content, so one long model name cannot widen the row", () => {
    const { container } = renderCard(device({ model: "iPad13,4" }));
    const card = container.querySelector<HTMLElement>('[data-testid="device-card"]');
    expect(card?.className.split(/\s+/)).toContain("min-w-0");
  });

  // The other half of the chain, one level in. Both are required and neither is sufficient alone.
  it("keeps the name column able to shrink too", () => {
    const { container } = renderCard(device({ model: "iPad13,4" }));
    const subtitle = container.querySelector<HTMLElement>(".truncate");
    expect(subtitle?.textContent).toBe("iPad Pro 11-inch (3rd generation) · iOS 26.0.1");
    expect(subtitle?.parentElement?.className.split(/\s+/)).toContain("min-w-0");
  });
});

// qn.12 D7.2 — THE DUE BADGE, which is the assisted model's in-app half.
//
// For a Lockdown Mode user this is the whole loop: WebKit disables Web Push declaratively on any
// certificate, so no notification can reach them (quince#510) and the card is all there is. For
// everyone else it is a second way to see what the notifier already decided.
describe("DeviceCard due badge", () => {
  it("says nothing about a device that is fresh, or when quince has no thresholds yet", () => {
    renderCard(device({ last_backup: { at: "2026-07-21T00:00:00Z", job_id: "J1", status: "succeeded" } }), "fresh");
    expect(screen.queryByText(/^Due$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Overdue$/)).not.toBeInTheDocument();

    cleanup();
    // No `due` prop at all — config still loading, or the request failed. Silence, not a guess.
    renderCard(device({ last_backup: { at: "2026-07-21T00:00:00Z", job_id: "J1", status: "succeeded" } }));
    expect(screen.queryByText(/^Due$/)).not.toBeInTheDocument();
  });

  it("marks a due device and an overdue one differently", () => {
    renderCard(device({}), "due");
    expect(screen.getByText(/^Due$/)).toBeInTheDocument();
    cleanup();
    renderCard(device({}), "overdue");
    expect(screen.getByText(/^Overdue$/)).toBeInTheDocument();
  });

  // NEVER-BACKED-UP IS SAID ONCE, IN THE BODY, AND NOT JUDGED IN THE HEADER (quince#1195).
  //
  // This asserted `Not backed up` was PRESENT until the badge was removed for saying the same thing
  // as `No backups yet` eight lines away. Rewritten to pin the PROPERTY rather than the mechanism:
  // the state is stated exactly once, and it is not coloured as a problem. A device somebody just
  // paired is in the ordinary state, and first run must not look broken.
  //
  // The `getAllByText(...).length === 1` is the load-bearing half — a plain `getByText` would pass
  // if some future header badge reintroduced the duplication, because it only fails on ZERO matches.
  it("says never-backed-up once, in the body, and does not judge it", () => {
    renderCard(device({ last_backup: null }), "never");
    expect(screen.getAllByText(/No backups yet/)).toHaveLength(1);
    expect(screen.queryByText(/Not backed up/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Overdue$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Due$/)).not.toBeInTheDocument();
  });

  // THE COUNT IS SUPPRESSED AT ZERO. The other half of the claim — that a non-zero count still
  // prints — is `counts the device's non-missing versions` above, which asserts `2 backups`. Both
  // halves matter: this one alone would pass a component that had simply stopped counting.
  it("does not print a zero backup count beside No backups yet", () => {
    renderCard(device({ last_backup: null }), "never");
    expect(screen.queryByText(/^0 backups$/)).not.toBeInTheDocument();
  });

  // AN UNREADABLE TIMESTAMP IS THE ABSENCE OF A CLAIM. The card still shows whatever it has under
  // the transports; it just does not judge it.
  it("does not judge a device whose last-backup time it cannot read", () => {
    renderCard(device({}), "unknown");
    expect(screen.queryByText(/^Due$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Overdue$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Not backed up/)).not.toBeInTheDocument();
  });
});
