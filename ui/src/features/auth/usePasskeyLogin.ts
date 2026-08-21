import { useEffect, useRef } from "react";

import { signInWithPasskey, webauthnAvailable } from "@/lib/webauthn";
import { hasPasskeyHint } from "@/lib/passkeyHint";
import type { AuthStatus } from "@/lib/types";

// The on-load sign-in sheet: open quince, Face ID, in.
//
// IT FIRES THE MODAL ON PAGE LOAD RATHER THAN ARMING CONDITIONAL MEDIATION, and the two are
// mutually exclusive on iOS: the platform grants exactly ONE gesture-free
// `navigator.credentials.get()` per page load, and whichever path runs first spends it. The
// conditional version was found on hardware only by tapping the key icon on the keyboard, past a
// suggestion list whose first entry was a password — undiscoverable, which is what decided this.
//
// WHAT IT COSTS is that credential presence is undetectable, so an unprompted sheet would fire at
// people who have none — a fresh install, a box after `quince auth reset`, the public demo. The
// memory gate below is what removes every one of those cases without a device heuristic and without
// asking the server anything.
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
    // install, the box after `quince auth reset`, and the public demo, all removed without a
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
