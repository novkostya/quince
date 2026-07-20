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
