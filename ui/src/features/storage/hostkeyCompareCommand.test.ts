import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// THE COMPARE COMMAND HAS BEEN WRONG THREE TIMES AND NOTHING PINNED IT.
//
// It is the one instruction in the host-key ceremony whose entire job is to be runnable on the
// operator's machine, and it has shipped as: a filename derived from the key type (impossible for
// ecdsa, whose type carries the curve and whose filename does not); a `/etc/ssh` glob (assumes the
// directory `sshd_config` need not use); and now `ssh-keyscan`, which asks the running sshd and so
// names no path at all.
//
// THE RULE, NOT THE STRING. Asserting the exact command would pin today's wording and would have
// passed for all three of those. What every attempt was reaching for is that quince must not guess
// where a host keeps its keys — so that is what is asserted, and a fourth attempt that reintroduces
// a path fails here whatever it looks like.
//
// A SOURCE ASSERTION RATHER THAN A RENDER ONE, and that is a declared compromise. Reaching this
// panel in a render test needs the probe, the key and the scan all mocked, and the component has no
// test harness yet; a text guard costs nothing and covers the property that actually regressed. A
// render test is still owed — it is what would catch the command disappearing entirely.
const source = readFileSync(
  // Resolved from the vitest root (`ui/`) rather than from `import.meta.url`, which is not a file
  // URL under this config.
  resolve(process.cwd(), "src/features/storage/AddStorageForm.tsx"),
  "utf8",
);

// The rendered command, extracted from the block it lives in rather than from the whole file —
// otherwise the surrounding comment, which legitimately discusses `/etc/ssh` and `ssh_host_*`, would
// be what the assertions read.
function compareCommand(): string {
  const marker = 'data-testid="hostkey-compare-command"';
  const at = source.indexOf(marker);
  expect(at, "the compare command block is gone — the ceremony has no comparison step").not.toBe(-1);
  const open = source.indexOf(">", at);
  const close = source.indexOf("</pre>", at);
  const cmd = source.slice(open + 1, close).trim();
  // NON-EMPTY IS ASSERTED HERE, because an extraction bug makes every "does not contain" assertion
  // below pass vacuously — which is exactly what happened on the first run of this file.
  expect(cmd, "extracted an empty command — the block markers moved").not.toBe("");
  return cmd;
}

describe("the host-key compare command", () => {
  it("asks the running sshd rather than reading a file", () => {
    expect(compareCommand()).toContain("ssh-keyscan");
  });

  it("names no filesystem path — quince cannot know where a host keeps its keys", () => {
    const cmd = compareCommand();
    expect(cmd).not.toContain("/etc/");
    expect(cmd).not.toContain("ssh_host_");
    // A key TYPE in the command means a filename was derived from one, which is the first
    // instance's exact defect.
    expect(cmd).not.toContain("ed25519");
    expect(cmd).not.toContain("ecdsa");
  });

  it("reads several keys from stdin, because `ssh-keygen -lf` takes exactly one file", () => {
    expect(compareCommand()).toMatch(/ssh-keygen\s+-lf\s+-/);
  });
});
