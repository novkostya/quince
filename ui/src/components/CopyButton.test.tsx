import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { CopyButton } from "./CopyButton";

// quince#818 follow-up — the first clipboard use in the product.
//
// THE FALLBACK IS THE POINT, not the modern path. `navigator.clipboard` needs a SECURE CONTEXT, and
// quince is routinely reached over plain http at a LAN address — there is a whole onboarding page
// about it. So on the deployment this button most needs to work on, the modern API is undefined.
const realClipboard = navigator.clipboard;
afterEach(() => {
  Object.defineProperty(navigator, "clipboard", { value: realClipboard, configurable: true });
  vi.restoreAllMocks();
});

function setClipboard(value: unknown) {
  Object.defineProperty(navigator, "clipboard", { value, configurable: true });
}

describe("CopyButton", () => {
  it("uses navigator.clipboard where it exists", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard({ writeText });

    render(<CopyButton value="the-line" />);
    fireEvent.click(screen.getByTestId("copy-button"));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("the-line"));
    await waitFor(() => expect(screen.getByTestId("copy-button").dataset.state).toBe("copied"));
  });

  // PLAIN HTTP: the API is simply not there. Falling back rather than doing nothing is the whole
  // reason this component exists instead of a one-line onClick.
  it("falls back to execCommand when there is no clipboard API", async () => {
    setClipboard(undefined);
    const exec = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });

    render(<CopyButton value="the-line" />);
    fireEvent.click(screen.getByTestId("copy-button"));

    await waitFor(() => expect(exec).toHaveBeenCalledWith("copy"));
    await waitFor(() => expect(screen.getByTestId("copy-button").dataset.state).toBe("copied"));
  });

  // A REJECTED WRITE IS NOT A FAILURE YET. Denied permission, or a context that lied about being
  // secure, is exactly what the fallback is for — so it must not be reported until that has failed too.
  it("a rejected clipboard write still tries the fallback", async () => {
    setClipboard({ writeText: vi.fn().mockRejectedValue(new Error("denied")) });
    const exec = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });

    render(<CopyButton value="the-line" />);
    fireEvent.click(screen.getByTestId("copy-button"));

    await waitFor(() => expect(exec).toHaveBeenCalledWith("copy"));
    await waitFor(() => expect(screen.getByTestId("copy-button").dataset.state).toBe("copied"));
  });

  // AND WHEN BOTH REFUSE IT SAYS SO. A copy button reporting success it did not achieve is worse
  // than no button: the user walks away believing they hold the line and pastes whatever was there
  // before — which for an authorized_keys entry means pasting something else onto a storage host.
  it("reports failure rather than claiming a copy it did not make", async () => {
    setClipboard(undefined);
    Object.defineProperty(document, "execCommand", {
      value: vi.fn().mockReturnValue(false),
      configurable: true,
    });

    render(<CopyButton value="the-line" />);
    fireEvent.click(screen.getByTestId("copy-button"));

    await waitFor(() => expect(screen.getByTestId("copy-button").dataset.state).toBe("failed"));

    // IT MUST SAY WHAT TO DO. The state alone is not a remedy — this is the one moment the component
    // has to hand the operator back the job it failed at.
    const label = screen.getByTestId("copy-button").textContent ?? "";
    expect(label).toContain("by hand");

    // AND NOT NAME A KEY THE DEVICE DOES NOT HAVE. This copy read `Press ⌘C` until quince#885's
    // review: a Mac shortcut, on a screen whose own comment says it is used from a phone. A remedy
    // the user cannot follow is the same defect as a silent failure (qn.6g), so the rule is asserted
    // here rather than left to the next person to re-derive from the same wrong instinct.
    expect(label).not.toContain("⌘");
  });
});
