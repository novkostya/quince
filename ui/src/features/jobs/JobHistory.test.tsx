import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { JobHistory } from "./JobHistory";
import type { Job } from "@/lib/types";

function job(overrides: Partial<Job>): Job {
  return {
    id: "J1",
    udid: "DEV-1",
    kind: "backup",
    transport: "wifi",
    state: "succeeded",
    progress: { phase: "done", percent: 100, bytes_done: 0, bytes_total: 0, files_received: 0, liveness: "active" },
    started_at: "2026-07-20T00:00:00Z",
    finished_at: "2026-07-20T00:01:00Z",
    error: null,
    retry_of: null,
    intent_id: "J1",
    attempt: 1,
    version_id: null,
    storage_id: null,
    ...overrides,
  };
}

describe("JobHistory", () => {
  it("offers Retry ONLY on the latest intent when it needs attention — not older failed intents", () => {
    const onRetry = vi.fn();
    // Newest intent failed; an OLDER intent also failed. Only the newest gets a Retry.
    // finished_at set per job, not left at the shared default: it is what the row displays and the
    // list sorts on (quince#813), and the default described a job finishing before it started.
    const latestFailed = job({
      id: "F2",
      state: "failed",
      intent_id: "F2",
      started_at: "2026-07-20T10:00:00Z",
      finished_at: "2026-07-20T10:05:00Z",
    });
    const olderFailed = job({
      id: "F1",
      state: "connection_lost",
      intent_id: "F1",
      started_at: "2026-07-19T10:00:00Z",
      finished_at: "2026-07-19T10:05:00Z",
    });
    render(<JobHistory jobs={[olderFailed, latestFailed]} onRetry={onRetry} />);

    const retries = screen.getAllByTestId("retry-backup");
    expect(retries).toHaveLength(1); // only the latest intent, not the older failed one

    fireEvent.click(retries[0]);
    expect(onRetry).toHaveBeenCalledWith(expect.objectContaining({ id: "F2" }));
  });

  it("shows no Retry when the latest intent succeeded, even if an older intent failed", () => {
    const onRetry = vi.fn();
    const latestOk = job({
      id: "S1",
      state: "succeeded",
      intent_id: "S1",
      started_at: "2026-07-20T10:00:00Z",
      finished_at: "2026-07-20T10:05:00Z",
    });
    const olderFailed = job({
      id: "F1",
      state: "failed",
      intent_id: "F1",
      started_at: "2026-07-19T10:00:00Z",
      finished_at: "2026-07-19T10:05:00Z",
    });
    render(<JobHistory jobs={[olderFailed, latestOk]} onRetry={onRetry} />);
    expect(screen.queryByTestId("retry-backup")).toBeNull();
  });

  // The Retry must NOT ride on the display order (quince#813). These two intents overlap, so the
  // failed one finished LAST and therefore sorts first, while the succeeded one STARTED last — which
  // is the key DeviceCard.tsx:70 uses to decide whether the device needs attention. The device card
  // says "no attention"; this must agree, whatever order the rows are in.
  it("keys Retry on the last-STARTED intent, not the top row, so it agrees with the device card", () => {
    const onRetry = vi.fn();
    const longFailed = job({
      id: "F1",
      state: "failed",
      intent_id: "F1",
      started_at: "2026-07-20T05:00:00Z",
      finished_at: "2026-07-20T06:30:00Z",
    });
    const quickOk = job({
      id: "S1",
      state: "succeeded",
      intent_id: "S1",
      started_at: "2026-07-20T05:30:00Z",
      finished_at: "2026-07-20T05:40:00Z",
    });
    render(<JobHistory jobs={[longFailed, quickOk]} onRetry={onRetry} />);

    // The failed intent is the FIRST row — it finished most recently.
    const times = Array.from(document.querySelectorAll("time")).map((t) => t.getAttribute("datetime"));
    expect(times).toEqual(["2026-07-20T06:30:00Z", "2026-07-20T05:40:00Z"]);
    // …and it still gets no Retry, because a later backup has started since.
    expect(screen.queryByTestId("retry-backup")).toBeNull();
  });

  it("renders no Retry when onRetry is not provided", () => {
    render(<JobHistory jobs={[job({ state: "failed" })]} />);
    expect(screen.queryByTestId("retry-backup")).toBeNull();
  });

  // quince#813. The <time dateTime> is what is asserted, not the rendered "28 minutes ago": the
  // relative text moves with the wall clock, the instant behind it does not.
  it("timestamps a completed row with the FINISH, and does not call it a start", () => {
    const { container } = render(
      <JobHistory
        jobs={[
          job({ id: "S1", state: "succeeded", started_at: "2026-08-10T05:41:00Z", finished_at: "2026-08-10T06:10:40Z" }),
        ]}
      />,
    );
    expect(container.querySelector("time")?.getAttribute("datetime")).toBe("2026-08-10T06:10:40Z");
    expect(screen.queryByText("started")).toBeNull();
  });

  it("timestamps a running row with the start, and SAYS it is a start", () => {
    const { container } = render(
      <JobHistory
        jobs={[job({ id: "R1", state: "backing_up", started_at: "2026-08-10T06:20:00Z", finished_at: null })]}
      />,
    );
    expect(container.querySelector("time")?.getAttribute("datetime")).toBe("2026-08-10T06:20:00Z");
    expect(screen.getByText("started")).toBeInTheDocument();
  });

  it("caps the history at 3 by default and expands on 'Show all'", () => {
    const jobs = Array.from({ length: 5 }, (_, i) =>
      job({
        id: `J${i}`,
        intent_id: `J${i}`,
        state: "succeeded",
        started_at: `2026-07-2${i}T00:00:00Z`,
        finished_at: `2026-07-2${i}T00:05:00Z`,
      }),
    );
    render(<JobHistory jobs={jobs} />);
    expect(screen.getAllByText(/backup completed/i)).toHaveLength(3); // capped
    const toggle = screen.getByTestId("history-toggle");
    expect(toggle.textContent).toMatch(/show all 5/i);
    fireEvent.click(toggle);
    expect(screen.getAllByText(/backup completed/i)).toHaveLength(5); // expanded
    expect(screen.getByTestId("history-toggle").textContent).toMatch(/show less/i);
  });
});

// quince#889 item 3: `Retry` on a backup the daemon refused for encryption is an affordance for a
// fix that is not the fix — nothing about pressing it again changes the device's encryption state.
describe("a failure a retry cannot change", () => {
  const encryptionRefused = job({
    id: "E1",
    state: "failed",
    intent_id: "E1",
    started_at: "2026-07-20T10:00:00Z",
    finished_at: "2026-07-20T10:00:02Z",
    error: { code: "encryption_required", message: "encrypted backups are required" },
  });

  it("offers the remedy instead of Retry", () => {
    render(<JobHistory jobs={[encryptionRefused]} onRetry={vi.fn()} />);
    expect(screen.queryByTestId("retry-backup")).toBeNull();
    expect(screen.getByTestId("retry-futile")).toHaveTextContent(/encryption/i);
  });

  // The half that keeps the first honest: every OTHER terminal failure still gets its Retry, which
  // is the whole affordance this rung is not allowed to damage.
  it("still offers Retry for a failure that could go the other way next time", () => {
    const other = job({
      ...encryptionRefused,
      id: "E2",
      intent_id: "E2",
      error: { code: "storage_unreachable", message: "the disk went away" },
    });
    render(<JobHistory jobs={[other]} onRetry={vi.fn()} />);
    expect(screen.getByTestId("retry-backup")).toBeTruthy();
    expect(screen.queryByTestId("retry-futile")).toBeNull();
  });

  // A pre-qn.6 row carries no error object at all. It must not lose its Retry to an undefined read.
  it("still offers Retry when the failure carries no error object", () => {
    const noError = job({ ...encryptionRefused, id: "E3", intent_id: "E3", error: null });
    render(<JobHistory jobs={[noError]} onRetry={vi.fn()} />);
    expect(screen.getByTestId("retry-backup")).toBeTruthy();
  });
});
