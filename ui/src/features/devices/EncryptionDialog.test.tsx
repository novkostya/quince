import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { EncryptionDialog } from "./EncryptionDialog";
import { useOpsStore } from "@/stores/ops";

beforeEach(() => useOpsStore.setState({ byId: {} }));

const noop = () => {};

describe("EncryptionDialog", () => {
  it("enable: blocks on mismatch, then posts the enable action", async () => {
    const post = vi.fn().mockResolvedValue({ op_id: "OP2" });
    render(
      <EncryptionDialog udid="DEV-1" encryption="off" open onOpenChange={noop} post={post} />,
    );

    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "corrct-horse" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "mismatch" } });
    fireEvent.click(screen.getByRole("button", { name: /enable backup encryption/i }));
    await screen.findByText(/don't match/i);
    expect(post).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "corrct-horse" } });
    fireEvent.click(screen.getByRole("button", { name: /enable backup encryption/i }));
    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/encryption", {
      action: "enable",
      password: "corrct-horse",
    });
  });

  it("change_password: posts old + new when encryption is on", async () => {
    const post = vi.fn().mockResolvedValue({ op_id: "OP3" });
    render(<EncryptionDialog udid="DEV-1" encryption="on" open onOpenChange={noop} post={post} />);

    // Default mode is "change" when encryption is on.
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old-pw" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new-pw" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "new-pw" } });
    fireEvent.click(screen.getByRole("button", { name: /change backup password/i }));
    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/encryption", {
      action: "change_password",
      old_password: "old-pw",
      new_password: "new-pw",
    });
  });

  // THE REASON THE DIALOG IS A FORM AT ALL — quince#819. A save prompt is driven by submission, so
  // the password fields and the submit button have to sit in one form and Enter has to reach it.
  // Asserted through the FORM rather than through the button's onClick, because the button no longer
  // has one: if the wrap were lost, this is the case that would fail.
  it("submitting the form — not just clicking — posts, so Enter in a password field works", async () => {
    const post = vi.fn().mockResolvedValue({ op_id: "OP5" });
    render(<EncryptionDialog udid="DEV-1" encryption="on" open onOpenChange={noop} post={post} />);

    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old-pw" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new-pw" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "new-pw" } });
    fireEvent.submit(screen.getByLabelText("Current password").closest("form")!);

    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/encryption", {
      action: "change_password",
      old_password: "old-pw",
      new_password: "new-pw",
    });
  });

  // THE TRAP THE WRAP OPENS, PINNED — quince#819. `components/ui/button.tsx` sets no `type`, so a
  // `Button` inside a form is `type="submit"` by the HTML default: without the explicit
  // `type="button"` on each of these, clicking one posts the form. quince#819 named only the two
  // mode switchers; `Cancel` is the third and the one a user would actually hit.
  //
  // EVERY FIELD IS FILLED FIRST, AND THAT IS THE WHOLE TEST. Filling only `Current password` makes
  // all three cases pass whether the fix is present or not — `submit()` returns early on the missing
  // `New password`, so validation, not the button's type, is what stopped the post. Measured: with
  // `Cancel`'s `type` deleted the under-filled version stayed green. jsdom does dispatch `submit`
  // from a submit-button click (probed on jsdom 25.0.1), so with the form valid these fail red.
  it.each([
    [/^change password$/i],
    [/^disable$/i],
    [/^cancel$/i],
  ])("clicking %s does not submit the form", (pattern) => {
    const post = vi.fn().mockResolvedValue({ op_id: "OP6" });
    render(<EncryptionDialog udid="DEV-1" encryption="on" open onOpenChange={noop} post={post} />);

    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old-pw" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new-pw" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "new-pw" } });
    fireEvent.click(screen.getByRole("button", { name: pattern }));

    expect(post).not.toHaveBeenCalled();
  });

  it("disable: switches to disable mode and posts the current password with a warning shown", async () => {
    const post = vi.fn().mockResolvedValue({ op_id: "OP4" });
    render(<EncryptionDialog udid="DEV-1" encryption="on" open onOpenChange={noop} post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /^disable$/i }));
    // The discouraging copy is shown.
    await screen.findByText(/discouraged/i);
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old-pw" } });
    fireEvent.click(screen.getByRole("button", { name: /disable backup encryption/i }));
    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/encryption", {
      action: "disable",
      password: "old-pw",
    });
  });
});
