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
  it("offers Retry only on a group whose latest attempt failed", () => {
    const onRetry = vi.fn();
    const failed = job({ id: "F1", state: "connection_lost", intent_id: "F1" });
    const succeeded = job({ id: "S1", state: "succeeded", intent_id: "S1" });
    render(<JobHistory jobs={[failed, succeeded]} onRetry={onRetry} />);

    const retries = screen.getAllByTestId("retry-backup");
    expect(retries).toHaveLength(1); // only the failed group

    fireEvent.click(retries[0]);
    expect(onRetry).toHaveBeenCalledWith(expect.objectContaining({ id: "F1" }));
  });

  it("renders no Retry when onRetry is not provided", () => {
    render(<JobHistory jobs={[job({ state: "failed" })]} />);
    expect(screen.queryByTestId("retry-backup")).toBeNull();
  });
});
