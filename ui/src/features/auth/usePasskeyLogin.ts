import { useEffect, useRef } from "react";

import { signInWithPasskey, webauthnAvailable } from "@/lib/webauthn";
import { hasPasskeyHint } from "@/lib/passkeyHint";
import type { AuthStatus } from "@/lib/types";

// THROWAWAY VARIANT — NEVER MERGE. Staging only, for the Operator to judge.
//
// This fires the MODAL on page load instead of arming conditional mediation. The two are mutually
// exclusive on iOS: the platform grants exactly ONE gesture-free `navigator.credentials.get()` per
// page load, and whichever path runs first spends it.
//
// What it buys: open quince, Face ID, in. No tap, no hunting for the key icon on the keyboard —
// which is how the conditional version was found on hardware, past a suggestion list whose first
// entry was a password.
//
// What it costs, and this is the thing to judge on the screen rather than from my description: the
// sheet fires at anyone WITHOUT a passkey too, because credential presence is undetectable. On this
// stand that means a fresh install, a box after `quince auth reset`, and the public demo.
//
// NOT GATED on "does this quince have any credential at all", deliberately — that gate is the real
// design and it needs a pre-auth endpoint, which is a decision rather than a test build.
export function usePasskeyLogin(onSuccess: (status: AuthStatus) => void | Promise<void>): void {
  const armed = useRef(false);

  useEffect(() => {
    if (armed.current) return;
    armed.current = true;

    // The same question every visible control now asks, through the same expression — this hook was
    // the ONLY place in the tree that asked it, which is how three visible surfaces went on offering
    // ceremonies the browser could not start (quince#1076).
    if (!webauthnAvailable()) return;

    // THE GATE. Only fire an unprompted sheet where a passkey has already been created or used in
    // THIS browser. A device that has never had one never sees a modal — which is the fresh
    // install, the box after , and the public demo, all removed without a
    // device heuristic and without asking the server anything.
    //
    // The BUTTON is unconditional and is what the first time looks like.
    if (!hasPasskeyHint()) return;

    let cancelled = false;
    void (async () => {
      try {
        const status = (await signInWithPasskey({ conditional: false })) as AuthStatus;
        if (!cancelled) await onSuccess(status);
      } catch {
        // Silent, as the conditional version was: on this path a failure is usually "this device has
        // no passkey", and the user is looking at a working password form. Whether a DISMISSED sheet
        // deserves a message is one of the things this build exists to let the Operator feel.
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [onSuccess]);
}
