import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";

// CopyButton puts one string on the clipboard, and it is the first clipboard use in the product.
//
// IT CANNOT JUST CALL `navigator.clipboard`, and that is a property of how quince is deployed rather
// than caution. `navigator.clipboard` requires a SECURE CONTEXT — https, or localhost. quince is
// routinely reached over plain http at a LAN address; there is a whole onboarding page about it
// (`/onboarding/https`), and the session cookie's own `Secure` handling exists for the same reason.
// So on the deployment this button most needs to work on, `navigator.clipboard` is **undefined**.
//
// THREE RUNGS, AND THE LAST ONE IS THE POINT:
//
//  1. `navigator.clipboard.writeText` where it exists — the modern path, and the only one on https.
//  2. a hidden textarea + `document.execCommand("copy")` — deprecated, and it is what actually
//     works over plain http. Kept because the alternative is a button that silently does nothing on
//     half of this product's real deployments.
//  3. **`failed`, said out loud.** If both refuse, the button says so and the text stays selectable
//     by hand. A copy button that reports success it did not achieve is worse than no button: the
//     user walks away believing they have the line, and pastes whatever was on the clipboard before.
//
// RUNG 3 IS NOT A NICETY, AND `execCommand` IS DEPRECATED. When browsers finally remove it, rung 2
// disappears and rung 3 BECOMES the plain-http path — the honest failure is what keeps this screen
// usable, not a corner nobody reaches. Its wording is load-bearing for the same reason: a remedy the
// user cannot follow is the same defect as a silent failure (qn.6g), so it must not name a key that
// the phone this screen is used from does not have.
//
// `copied` REVERTS AFTER A MOMENT so the control is reusable, and because a permanently-green button
// stops being a report about the last press.
type CopyState = "idle" | "copied" | "failed";

export function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [state, setState] = useState<CopyState>("idle");
  // The timer is cleared on unmount: this button lives on a form that navigates away on success, and
  // a setState after that is a React warning nobody can act on.
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
    },
    [],
  );

  function settle(next: CopyState) {
    setState(next);
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = setTimeout(() => setState("idle"), 2000);
  }

  async function copy() {
    // RUNG 1. `?.` rather than a `window.isSecureContext` test: what matters is whether the API is
    // there, and a browser that exposes it in a context we did not predict should be used.
    try {
      if (navigator.clipboard?.writeText !== undefined) {
        await navigator.clipboard.writeText(value);
        settle("copied");
        return;
      }
    } catch {
      // Falls through. A rejected write — denied permission, a context that lied — is exactly the
      // case rung 2 exists for, so it is not reported until rung 2 has also failed.
    }

    // RUNG 2. The textarea must be IN the document and focusable for `execCommand` to see a
    // selection, so it is placed off-screen rather than `display: none`, which cannot be selected.
    try {
      const ta = document.createElement("textarea");
      ta.value = value;
      ta.setAttribute("readonly", "");
      ta.style.position = "fixed";
      ta.style.top = "-9999px";
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand("copy");
      document.body.removeChild(ta);
      settle(ok ? "copied" : "failed");
    } catch {
      settle("failed");
    }
  }

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={() => void copy()}
      data-testid="copy-button"
      data-state={state}
      // THE LABEL IS THE STATE. A tooltip would be invisible on the phone this screen is used from.
      aria-label={label}
    >
      {state === "copied" ? <Check size={14} /> : <Copy size={14} />}
      {state === "copied" ? "Copied" : state === "failed" ? "Copy it by hand" : label}
    </Button>
  );
}
