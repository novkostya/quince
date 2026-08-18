import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import { ProbeResult } from "./AddStorageForm";
import type { StorageProbe } from "@/lib/types";

// THE ADOPT PANEL SPEAKS ABOUT THE DISK WITHOUT CLAIMING ANYTHING ABOUT THE LIST — quince#716.
//
// The defect was one word. *"This is **already** a quince storage"* is true of the folder and reads
// as a refusal to somebody who has just pressed Forget: *already* asserts a CURRENT declaration,
// which is exactly what that reader knows to be false — and the screen then offered a button to add
// the thing it had just told them they had.
//
// TWO READERS, AND THE COPY CANNOT KNOW WHICH ONE IT HAS. The person adopting a replugged disk never
// forgot anything; the person re-adding what they forgot five minutes ago did. Forget deliberately
// keeps no state that would distinguish them (contracts §1, qn.6d gap B — the declaration goes, the
// marker on the disk does not), so a sentence true of only one of them is wrong half the time.
//
// THE ABSENCE ASSERTION IS THE POINT OF THIS FILE. Pinning the new sentence alone would let the next
// well-meaning "be precise" edit reintroduce *already* beside it. quince#1073 records the same
// technique for the plain-HTTP banner, and for the same reason: the word is the defect, so the word
// is what the gate has to forbid.
function probe(over: Partial<StorageProbe> = {}): StorageProbe {
  return {
    path: "/backups",
    clean_path: "/backups",
    outcome: "adopt",
    reason: "/backups is an existing quince storage",
    backend: "reflink",
    backend_reason: "recorded at this storage's creation moment; a storage's backend is immutable",
    marker: { storage_id: "01JA", backend: "reflink", created_at: "2026-07-18T08:00:00Z" },
    non_empty: true,
    zfs: "none",
    filesystem_free_bytes: 0,
    filesystem_total_bytes: 0,
    ...over,
  };
}

describe("the adopt panel", () => {
  it("says quince has used the folder before, in the past tense", () => {
    render(<ProbeResult probe={probe()} />);

    expect(screen.getByText("quince has used this folder before")).toBeInTheDocument();
  });

  it("never claims the storage is ALREADY one, which is the word that reads as a refusal", () => {
    const { container } = render(<ProbeResult probe={probe()} />);

    // Over the whole panel rather than one node: the claim is about what the reader sees, and a
    // future edit that moves the word into the subtitle would pass a node-scoped assertion.
    expect(container.textContent).not.toMatch(/already/i);
  });

  it("says what pressing the button will do, which the old copy left to be guessed", () => {
    render(<ProbeResult probe={probe()} />);

    expect(screen.getByText(/picks up where it left off/i)).toBeInTheDocument();
    expect(screen.getByText(/nothing is created or overwritten/i)).toBeInTheDocument();
  });

  // The daemon's own sentence still renders under it. It names the path, which is the one thing the
  // client cannot compose — and after a forget it is how the reader tells WHICH folder was found.
  it("still renders the daemon's reason verbatim", () => {
    render(<ProbeResult probe={probe({ reason: "/mnt/usb is an existing quince storage" })} />);

    expect(screen.getByText("/mnt/usb is an existing quince storage")).toBeInTheDocument();
  });

  // THE IMMUTABLE-BACKEND FACTS SURVIVE THE REWRITE. They were already there and are what make the
  // panel more than a sentence; a copy change that dropped them would be a regression this file
  // would otherwise not notice.
  it("still names the recorded backend and when the storage was created", () => {
    render(<ProbeResult probe={probe()} />);

    expect(screen.getByText("reflink")).toBeInTheDocument();
    expect(screen.getByText(/created 2026-07-18/)).toBeInTheDocument();
  });

  // THE CONTROL. Without it, "does not say already" and "says quince has used this folder before"
  // would both pass against a component that rendered nothing at all for an adopt.
  it("renders the refusal branch untouched for a non-adopt outcome", () => {
    render(
      <ProbeResult
        probe={probe({ outcome: "corrupt_marker", reason: "/backups holds an unreadable marker" })}
      />,
    );

    expect(screen.getByTestId("probe-refusal")).toBeInTheDocument();
    expect(screen.getByText("/backups holds an unreadable marker")).toBeInTheDocument();
    expect(screen.queryByText("quince has used this folder before")).not.toBeInTheDocument();
  });
});
