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
    ...overrides,
  };
}

describe("JobHistory", () => {
  it("offers Retry ONLY on the latest intent when it needs attention — not older failed intents", () => {
    const onRetry = vi.fn();
    // Newest intent failed; an OLDER intent also failed. Only the newest gets a Retry.
    const latestFailed = job({ id: "F2", state: "failed", intent_id: "F2", started_at: "2026-07-20T10:00:00Z" });
    const olderFailed = job({ id: "F1", state: "connection_lost", intent_id: "F1", started_at: "2026-07-19T10:00:00Z" });
    render(<JobHistory jobs={[olderFailed, latestFailed]} onRetry={onRetry} />);

    const retries = screen.getAllByTestId("retry-backup");
    expect(retries).toHaveLength(1); // only the latest intent, not the older failed one

    fireEvent.click(retries[0]);
    expect(onRetry).toHaveBeenCalledWith(expect.objectContaining({ id: "F2" }));
  });

  it("shows no Retry when the latest intent succeeded, even if an older intent failed", () => {
    const onRetry = vi.fn();
    const latestOk = job({ id: "S1", state: "succeeded", intent_id: "S1", started_at: "2026-07-20T10:00:00Z" });
    const olderFailed = job({ id: "F1", state: "failed", intent_id: "F1", started_at: "2026-07-19T10:00:00Z" });
    render(<JobHistory jobs={[olderFailed, latestOk]} onRetry={onRetry} />);
    expect(screen.queryByTestId("retry-backup")).toBeNull();
  });

  it("renders no Retry when onRetry is not provided", () => {
    render(<JobHistory jobs={[job({ state: "failed" })]} />);
    expect(screen.queryByTestId("retry-backup")).toBeNull();
  });

  it("caps the history at 3 by default and expands on 'Show all'", () => {
    const jobs = Array.from({ length: 5 }, (_, i) =>
      job({ id: `J${i}`, intent_id: `J${i}`, state: "succeeded", started_at: `2026-07-2${i}T00:00:00Z` }),
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
