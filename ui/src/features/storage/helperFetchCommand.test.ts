import { describe, expect, it } from "vitest";
import { helperFetchCommand } from "./AddStorageForm";

// THE FETCH COMMAND RUNS AS ROOT ON SOMEBODY ELSE'S STORAGE HOST, WHICH IS WHY IT IS PINNED.
//
// It is the one instruction on this form quince cannot check the result of: the operator runs it on
// a machine quince only reaches through a constrained forced command, and a command that half-works
// leaves a file that looks installed. The failures worth naming are all silent ones.
//
// THE RULES, NOT THE STRING. Asserting the exact line would pin today's wording and break on any
// reword; each case below is a property that has a way of going wrong.
describe("helperFetchCommand", () => {
  const cmd = helperFetchCommand(
    "https://nas.local",
    "/zfs/helper",
    "/usr/local/sbin/quince-zfs-helper",
  );

  it("fetches from the origin the operator is already using, at the served path", () => {
    expect(cmd).toContain("https://nas.local/zfs/helper");
  });

  it("writes to the path the forced command pins, and makes it executable", () => {
    expect(cmd).toContain("-o /usr/local/sbin/quince-zfs-helper");
    expect(cmd).toContain("chmod 0755 /usr/local/sbin/quince-zfs-helper");
  });

  // AN ABSOLUTE MODE, NOT `+x` (Operator, 2026-08-14). `+x` ADDS execute to whatever mode the file
  // already has, and `curl -o` creates it with `0666 & ~umask` — so on a permissive umask the result
  // is `0777`: a world-writable script that root executes on every backup, which any local user
  // could then rewrite. It is invisible on a normal umask, which is exactly why it is pinned.
  it("sets an absolute mode rather than adding a bit to whatever the umask produced", () => {
    expect(cmd).not.toMatch(/chmod\s+[+\-=]/);
    expect(cmd).toMatch(/chmod\s+0[0-7]{3}\s/);
  });

  // -f IS THE ONE THAT PREVENTS A SILENT WRONG INSTALL. Without it, curl writes an error page to the
  // destination and exits 0 — so a typo in the address installs the SPA's HTML as the helper, chmods
  // it, and the operator learns about it as `unreachable` on a later screen.
  it("fails on an HTTP error instead of saving the error page", () => {
    expect(cmd).toMatch(/curl\s+-[a-zA-Z]*f/);
  });

  // && RATHER THAN ; — a failed fetch must not go on to chmod whatever was already at that path.
  it("does not chmod when the fetch failed", () => {
    expect(cmd).toContain("&&");
    expect(cmd).not.toMatch(/;\s*chmod/);
  });

  // NOTHING IS EXECUTED BY THIS LINE. The suspicion a fetch-and-run one-liner earns is the whole
  // reason the script is also rendered in full above it; piping to a shell would earn that suspicion
  // for real, and would make the readable copy beside it decorative.
  it("never pipes into a shell", () => {
    expect(cmd).not.toMatch(/\|\s*(sudo\s+)?(sh|bash)/);
  });

  // The panel says "run this on the ZFS host" and does not know how they become root there.
  it("does not guess at sudo", () => {
    expect(cmd).not.toContain("sudo");
  });

  // The origin arrives from `window.location.origin`, which carries no trailing slash — so joining
  // is concatenation. Asserted because a double slash still works and would go unnoticed until it
  // appeared in a screenshot.
  it("joins the origin and the path without doubling the slash", () => {
    expect(cmd).not.toContain("//zfs/helper");
  });

  it("is one line — it is pasted into a terminal", () => {
    expect(cmd).not.toContain("\n");
  });
});
