import { describe, expect, it } from "vitest";
import { needsZFSConfig } from "./AddStorageForm";

// RE-ADDING A FORGOTTEN ZFS STORAGE MUST ASK FOR ITS TRANSPORT AGAIN (Operator, 2026-08-14,
// quince#966).
//
// Forget removes a storage's declaration from `config.yml` and leaves its marker on the disk, so the
// next add probes as `adopt`. The old test was `isNew && backend === "zfs"`, which excluded exactly
// that case: no parent dataset, no ssh fields, no key, no helper, no host-key step — and `canSave`'s
// `!needsZFS` arm went vacuous, so Save was enabled for a request the daemon must refuse.
//
// The backend selector stays hidden on an adopt and that is right: a backend is written into the
// marker at creation and is immutable. The transport is not — it is config, it lives in
// `config.yml`, and it is precisely what a re-add has lost.
describe("needsZFSConfig", () => {
  it("asks on a NEW zfs storage", () => {
    expect(needsZFSConfig("new", "zfs")).toBe(true);
  });

  it("asks on an ADOPT of a zfs storage — the case this exists for", () => {
    expect(needsZFSConfig("adopt", "zfs")).toBe(true);
  });

  it("does not ask for the namespace backends, which need no transport", () => {
    for (const b of ["reflink", "hardlink", "copy"]) {
      expect(needsZFSConfig("new", b), b).toBe(false);
      expect(needsZFSConfig("adopt", b), b).toBe(false);
    }
  });

  // A REFUSAL DECLARES NOTHING. `unreadable` and `backend_mismatch` carry a backend field too, so
  // gating on the backend alone would light the whole ceremony up underneath an error the operator
  // has to clear first.
  it("does not ask on a refusal, whatever backend it reports", () => {
    for (const o of ["unreadable", "backend_mismatch", "not_a_directory", undefined]) {
      expect(needsZFSConfig(o, "zfs"), String(o)).toBe(false);
    }
  });

  // Before a probe there is no outcome and no backend; the form shows the path field alone.
  it("does not ask before anything has been checked", () => {
    expect(needsZFSConfig(undefined, "")).toBe(false);
  });
});
