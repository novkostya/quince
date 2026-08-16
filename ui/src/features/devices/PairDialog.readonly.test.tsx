import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, it, expect, beforeEach } from "vitest";
import { PairDialog } from "./PairDialog";
import { useDevicesStore } from "@/stores/devices";

// qn.6p D7. When quince cannot write pairing records, the Pair control must be VISIBLE and
// disabled with an explanation — not hidden. A missing button explains nothing, and this state is
// usually deliberate: another tool owns the records and quince mounts them read-only.
function renderPair() {
  return render(
    <MemoryRouter>
      <PairDialog udid="SYNTHETIC-UDID-AAAA-0001" />
    </MemoryRouter>,
  );
}

describe("pairing when records cannot be written", () => {
  beforeEach(() => {
    useDevicesStore.setState({ pairing: { writable: true } });
  });

  it("offers Pair when records are writable", () => {
    renderPair();
    expect(screen.getByRole("button", { name: "Pair" })).toBeEnabled();
  });

  it("disables Pair and says why, in its own words", () => {
    useDevicesStore.setState({
      pairing: { writable: false, reason: "/var/lib/lockdown is not writable: permission denied" },
    });
    renderPair();

    expect(screen.getByRole("button", { name: "Pair" })).toBeDisabled();

    // The primary sentence is the UI's, not the server's: a user should not have to read an errno
    // to understand what is wrong. It must also say what still WORKS, because the common cause of
    // this state is another tool owning the records deliberately.
    expect(screen.getByText(/can’t save pairing records/i)).toBeInTheDocument();
    expect(screen.getByText(/still backs up/i)).toBeInTheDocument();

    // The server's words are present as DETAIL — an operator needs the path to fix it.
    expect(screen.getByText(/permission denied/)).toBeInTheDocument();
  });

  it("omits the detail line when the server sent no reason", () => {
    useDevicesStore.setState({ pairing: { writable: false } });
    renderPair();
    expect(screen.getByRole("button", { name: "Pair" })).toBeDisabled();
    expect(screen.getByText(/can’t save pairing records/i)).toBeInTheDocument();
  });
});
