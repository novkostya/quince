import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { registerPasskey, webauthnAvailable } from "@/lib/webauthn";
import { APIError } from "@/lib/api";

// EnrolPage — what a household member lands on after scanning the admin's QR (qn.13 D4, slice 9d).
//
// OUTSIDE EVERY GUARD, and for the login page's reason rather than a new one: the person here has
// no session, and obtaining one is what this page leads to. Its bound is the enrolment secret in
// its own URL — single-use, minutes-long, and revocable by the admin while this page is open.
//
// IT ASKS FOR ONE TAP AND NOTHING ELSE. No password field, no username, no account chooser: D4's
// whole shape is that the durable credential is a passkey and the secret authorises exactly one
// registration. A form here would be asking for something quince has no use for.
//
// THE NAME IS THE DEVICE'S, NOT THE PERSON'S. `name` labels the credential in the ADMIN's passkey
// list, and the admin already knows which device they issued this for. Asking a household member to
// invent a label would be asking them to name something they will never see again — and a personal
// name is exactly what the privacy rule keeps out of stored fields.

// THE FIVE REFUSALS THE SERVER CAN SEND, and each gets its own sentence rather than one apology.
//
// The person reading this is holding a link somebody handed them and has no session to reason from,
// so *what do I do now* has a different answer in every case — including one that is not a retry at
// all. Collapsing them into "something went wrong" is the defect the troubleshooting rule names.
const REFUSALS: Record<string, { title: string; body: string }> = {
  enrolment_unknown: {
    title: "This link is not from this quince",
    body: "Check the address you opened, or ask for a new QR code.",
  },
  enrolment_expired: {
    title: "This link has expired",
    body: "They are deliberately short-lived. Ask for a new QR code.",
  },
  enrolment_spent: {
    title: "This link has already been used",
    body:
      "A passkey was already added with it. If that was not you, tell whoever looks after this " +
      "quince — they can remove it.",
  },
  enrolment_revoked: {
    title: "This link was cancelled",
    body: "Ask for a new QR code.",
  },
  rate_limited: {
    title: "Too many attempts",
    body: "Wait a minute and try again. Trying again straight away makes this last longer.",
  },
};

function refusalFor(err: unknown): { title: string; body: string } {
  const code = err instanceof APIError ? err.code : "";
  return (
    REFUSALS[code] ?? {
      title: "That did not work",
      body: "Ask for a new QR code, or tell whoever looks after this quince.",
    }
  );
}

export function EnrolPage() {
  const [params] = useSearchParams();
  const secret = params.get("secret") ?? "";
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [refusal, setRefusal] = useState<{ title: string; body: string } | null>(null);

  // A LINK WITH NO SECRET IS ITS OWN CASE. Somebody typed the address by hand or the QR did not
  // scan cleanly, and telling them a link expired would send them to ask for a replacement they do
  // not need.
  if (!secret) {
    return (
      <Shell title="This link is incomplete">
        <p>
          Scan the QR code again — the part after the address is missing, so quince cannot tell which
          device this is for.
        </p>
      </Shell>
    );
  }

  // A BROWSER THAT CANNOT DO THIS SAYS SO BEFORE THE BUTTON, not after a tap that goes nowhere.
  if (!webauthnAvailable()) {
    return (
      <Shell title="This browser cannot add a passkey">
        <p>
          Open the same link in Safari on iPhone or iPad, or in a browser that supports passkeys.
        </p>
      </Shell>
    );
  }

  if (done) {
    return (
      <Shell title="You are set up">
        <p>
          This device is now yours to back up. You will only see this one device — nothing else on
          this quince.
        </p>
      </Shell>
    );
  }

  async function add() {
    setBusy(true);
    setRefusal(null);
    try {
      // THE NAME IS NOT ASKED FOR — see the header. The server labels the credential with the
      // device it is scoped to, and this is the fallback the admin sees if it cannot.
      const ok = await registerPasskey("household passkey", { enrolmentSecret: secret });
      // `false` IS A DISMISSED SHEET, NOT A FAILURE. `registerPasskey` returns it for a cancelled
      // or timed-out prompt, and the person is exactly where they started — so the page stays put
      // with no red message, and the button is still there.
      if (ok) setDone(true);
    } catch (err) {
      setRefusal(refusalFor(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Shell title="Set up this device">
      <p>
        Add a passkey so you can back this device up. You will see this one device and nothing else
        on this quince.
      </p>
      {refusal && (
        <div role="alert" className="rounded-lg border border-danger/40 bg-danger/5 p-3 text-sm">
          <p className="font-medium">{refusal.title}</p>
          <p className="text-fg-muted">{refusal.body}</p>
        </div>
      )}
      <Button onClick={add} disabled={busy}>
        {busy ? "Waiting for your device…" : "Add a passkey"}
      </Button>
    </Shell>
  );
}

function Shell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center gap-4 p-6">
      <h1 className="text-xl font-semibold">{title}</h1>
      {children}
    </div>
  );
}
