import * as React from "react";
import { useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { AddPasskeyDialog } from "@/features/settings/AddPasskeyDialog";

// The onboarding passkey offer — qn.6k slice 6, story 9. Ruled on quince#657: "offered in
// onboarding; added later in Settings."
//
// SKIPPING IS THE NORMAL PATH, AND THE PAGE HAS TO LOOK LIKE IT. A passkey is an addition and never
// a replacement — the ruling is explicit, and the reason is that a lost phone must not lock the user
// out of their own backups. A step that pressed for one here would be selling the wrong story on the
// screen where the user forms their idea of what quince expects.



type Support = { rp_id: string; supported: boolean };

export function OnboardingPasskeyPage() {
  const nav = useNavigate();
  const [support, setSupport] = React.useState<Support | null>(null);
  const [adding, setAdding] = React.useState(false);

  const done = React.useCallback(() => nav("/", { replace: true }), [nav]);

  React.useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const s = await api.get<Support>("/api/auth/passkeys");
        if (!cancelled) setSupport({ rp_id: s.rp_id ?? "", supported: s.supported === true });
      } catch {
        // AN UNREACHABLE LIST MEANS "DO NOT OFFER", not an error on the screen after someone has
        // just set their password. The step is optional; a failure here makes it absent.
        if (!cancelled) setSupport({ rp_id: "", supported: false });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);


  // NOT OFFERED WHERE IT CANNOT WORK — story 4, and here it means skipping the step entirely rather
  // than showing a refusal. On a tier that cannot hold a passkey there is nothing for the user to
  // decide, and a first-run screen explaining a capability they cannot have is worse than no screen.
  React.useEffect(() => {
    if (support && !support.supported) done();
  }, [support, done]);

  if (!support || !support.supported) return null;

  return (
    <div className="flex min-h-dvh items-start justify-center bg-bg pb-6 pl-[max(1.5rem,env(safe-area-inset-left))] pr-[max(1.5rem,env(safe-area-inset-right))] pt-[max(4rem,env(safe-area-inset-top))] text-fg sm:items-center sm:py-6">
      <div className="w-full max-w-sm rounded-card border border-line bg-card p-6">
        <div className="text-lg font-semibold tracking-tight">quince</div>
        <h1 className="mt-4 text-base font-semibold">Sign in with Face ID?</h1>
        <p className="mt-1 text-sm text-muted">
          Add a passkey and you can sign in with Face ID or Touch ID instead of typing your password
          on a phone keyboard.
        </p>
        <p className="mt-3 text-sm text-muted">
          Your password keeps working either way — a passkey is an addition, never a replacement.
        </p>
        {/* THE HAZARD, WHERE THE CREDENTIAL IS CREATED. Ruled explicitly: not only in the docs. */}
        <p className="mt-3 text-xs text-muted">
          A passkey is tied to the address you set it up on —{" "}
          <span className="font-mono">{support.rp_id}</span>. If you later reach quince by a
          different name, you sign in with your password instead.
        </p>



        <div className="mt-5 flex flex-col gap-2">
          <Button type="button" onClick={() => setAdding(true)}>
            Add a passkey
          </Button>
          {/* SKIP IS A FIRST-CLASS BUTTON, not a link in the corner. It is the normal answer, and
              the user can add one later from Settings whenever they like. */}
          <Button type="button" variant="outline" onClick={done}>
            Not now
          </Button>
        </div>

        {/* THE SAME DIALOG THE SETTINGS SURFACE USES. One naming step, one ceremony, one set of
            error messages — a second copy is how the two would drift, and this ceremony has already
            cost one bug that only one copy would have carried. */}
        <AddPasskeyDialog open={adding} onOpenChange={setAdding} onAdded={done} />
      </div>
    </div>
  );
}
