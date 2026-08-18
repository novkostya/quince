import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// EVERY TEXT FIELD LIVES IN A FORM, SO RETURN ALWAYS DOES SOMETHING.
//
// This is a class of defect rather than a bug: a screen is built as a div with an input and a
// button, it works under a mouse, and Return silently does nothing. It has been reported by hand
// three times on the onboarding flow alone — the proxy field, the certificate fields, the passkey
// name — each time found by a person walking the product, which is the most expensive way to find
// anything.
//
// SO THE CHECK IS STRUCTURAL AND CRUDE ON PURPOSE. It does not parse JSX or reason about nesting: if
// a file renders a text input, it must also render a `<form`. That is coarse enough to be wrong in
// principle and has been exactly right in practice — the failure mode it catches is somebody adding
// a field to a surface that has no form at all.
//
// WHEN IT FIRES AND YOU BELIEVE IT IS WRONG, the fix is a form with an `onSubmit`, not an entry in
// the allowlist below. Every allowlist entry is a place where Return does nothing, and each one
// needs a reason that is about the CONTENT rather than about the effort.
const ALLOWED = new Map<string, string>([
  // The primitives themselves: each renders an input and belongs to whatever form its caller
  // provides.
  ["components/ui/input.tsx", "the shared <Input>, which has no page of its own"],
  // Its fields sit inside the <form> AuthPage renders when handed an onSubmit, and its action is a
  // <Button type="submit"> — so Return works here; the form simply lives one component up.
  ["features/auth/PasswordForm.tsx", "submitted by AuthPage's form, via type=\"submit\""],
]);

const SRC = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function tsxFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((entry) => {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) return tsxFiles(full);
    return full.endsWith(".tsx") && !full.endsWith(".test.tsx") ? [full] : [];
  });
}

describe("every text field can be submitted with Return", () => {
  it("has no input outside a form", () => {
    const offenders: string[] = [];

    for (const file of tsxFiles(SRC)) {
      const rel = relative(SRC, file).split("\\").join("/");
      if (ALLOWED.has(rel)) continue;

      const src = readFileSync(file, "utf8");
      // A TEXT FIELD, NOT EVERY INPUT. Checkboxes and radios have no implicit submission to lose,
      // and demanding a form around them would be noise that teaches people to pad the allowlist.
      const hasTextField = /<Input[\s/>]/.test(src) || /<input(?![^>]*type="(checkbox|radio)")/.test(src);
      if (hasTextField && !src.includes("<form")) offenders.push(rel);
    }

    expect(offenders, "these render a text field with no <form>, so Return does nothing").toEqual([]);
  });

  // AND THE ALLOWLIST DOES NOT ROT. An entry for a file that no longer has an input is a claim
  // nobody checked, and the next reader treats the whole list as folklore.
  it("keeps no stale exemptions", () => {
    for (const [rel] of ALLOWED) {
      const src = readFileSync(join(SRC, rel), "utf8");
      const hasInput = /<Input[\s/>]/.test(src) || /<input/.test(src);
      expect(hasInput, `${rel} is exempted but renders no input`).toBe(true);
    }
  });
});
