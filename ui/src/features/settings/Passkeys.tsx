import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import { AddPasskeyDialog } from "./AddPasskeyDialog";
import { forgetPasskey } from "@/lib/passkeyHint";
import { RelativeTime } from "@/components/RelativeTime";

// The passkey management surface — qn.6k slice 5b, stories 2, 3, 4 and 8.
//
// It has one job the login form does not: TELLING THE TRUTH ABOUT WHERE A CREDENTIAL WORKS. A
// passkey is bound to a domain, the access path is a user choice, and nothing in the protocol warns
// when they stop agreeing — the phone still lists a credential that can no longer sign in anywhere.
// This is the only screen that can say so, and it is why the list endpoint returns the server's own
// rpId rather than letting the browser guess from `location.hostname`.

type Passkey = {
  id: string;
  name: string;
  rp_id: string;
  created_at: string;
  last_used_at: string | null;
};

export type PasskeyList = {
  passkeys: Passkey[];
  rp_id: string;
  supported: boolean;
  // quince#855 — whether an admin PASSWORD exists. On this payload rather than on the pre-auth
  // `auth/status`, so it is disclosed only to a session that is already the admin.
  has_password: boolean;
};

// EXPORTED since qn.6m slice 6b: `PasswordControls` sits on the same page and going passwordless
// changes what this list should say about itself, so it must be able to invalidate it. Shared from
// here rather than redeclared there — two spellings of one query key is a cache that silently
// splits in two.
export const passkeysKey = ["auth", "passkeys"] as const;
const key = passkeysKey;

// ONE QUERY, TWO CONSUMERS. `PasswordControls` needs `has_password` from this same payload, and a
// second `useQuery` on the same key would be a second definition of the fetch to keep in step. React
// Query dedupes by key, so both components share one request and one cache entry.
export function usePasskeyList() {
  return useQuery({ queryKey: key, queryFn: () => api.get<PasskeyList>("/api/auth/passkeys") });
}



export function Passkeys() {
  const qc = useQueryClient();
  const list = usePasskeyList();
  const [adding, setAdding] = React.useState(false);

  const invalidate = () => void qc.invalidateQueries({ queryKey: key });

  const remove = useMutation({
    mutationFn: (id: string) => api.del<void>(`/api/auth/passkeys/${encodeURIComponent(id)}`),
    onSuccess: (_data, id) => {
      // REMOVING THE LAST ONE STOPS THE UNPROMPTED SHEET. Otherwise the next visit fires a sheet
      // with nothing to offer — the exact wrong-guess the hint exists to prevent, caused by us.
      //
      // Only when it was the last: with others left, a passkey can still work in this browser, and
      // the removed one may not even have been this device's.
      if (rows.length <= 1 && rows.some((p) => p.id === id)) forgetPasskey();
      invalidate();
    },
  });


  // DEFENSIVE ABOUT THE SHAPE, AND NOT AS A STYLE CHOICE. This card is mounted beside the config
  // editor, so a render that throws here takes the WHOLE Settings page with it — including the
  // editor, on a box whose passkey list happens to be malformed or served by an older daemon that
  // has no such endpoint. A missing array must degrade to "no passkeys yet", never to a blank page.
  // Found by an existing test whose shared `api.get` mock returns the config body for every call,
  // which is exactly the malformed-response case.
  const data = list.data;
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  const rpID = typeof data?.rp_id === "string" ? data.rp_id : "";
  // `supported` absent is treated as NOT supported, so the add button stays disabled rather than
  // offering a ceremony this address may not be able to complete.
  const supported = data?.supported === true;

  return (
    <div className="mt-8">
      <h2 className="text-sm font-semibold text-muted">Passkeys</h2>
      <p className="mt-3 text-sm text-muted">
        Sign in with Face ID or Touch ID instead of typing your password. Your password keeps working
        — a passkey is an addition, never a replacement.
      </p>

      {/* STORY 4: REFUSE THE TIER RATHER THAN OFFER A BUTTON THAT CANNOT WORK. An rpId must be a
          domain, so a bare IP cannot host passkeys and no certificate fixes it. Saying so here is
          the difference between "quince does not support this" and "quince is broken". */}
      {data && !supported ? (
        <p className="mt-3 rounded-card border border-line bg-bg px-3 py-2 text-sm text-warn">
          Passkeys need a domain name over https, and you have reached quince at{" "}
          <span className="font-mono">{rpID}</span>. A reverse proxy or Tailscale gives you
          one; an address like this cannot hold a passkey.
        </p>
      ) : null}

      {list.isLoading ? <p className="mt-3 text-sm text-muted">Loading…</p> : null}
      {list.isError ? <p className="mt-3 text-sm text-danger">Could not load passkeys.</p> : null}


      {data && rows.length === 0 ? (
        <p className="mt-3 text-sm text-muted">No passkeys yet.</p>
      ) : null}

      {data && rows.length > 0 ? (
        <ul className="mt-3 flex flex-col gap-2">
          {rows.map((p) => (
            <li
              key={p.id}
              className="flex flex-wrap items-center justify-between gap-2 rounded-card border border-line bg-bg px-3 py-2"
            >
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{p.name}</div>
                <div className="text-xs text-muted">
                  added <RelativeTime iso={p.created_at} />
                  {p.last_used_at ? (
                    <>
                      {" · last used "}
                      <RelativeTime iso={p.last_used_at} />
                    </>
                  ) : (
                    // NEVER USED IS A FACT WORTH SHOWING, not an empty cell: a credential nobody
                    // has signed in with is exactly the one worth removing.
                    " · never used"
                  )}
                </div>
                {/* THE HAZARD, ON THE ROW IT APPLIES TO. Moving between access paths breaks every
                    passkey while the phone still lists them, and nothing in the protocol says so.
                    Comparing against the SERVER's rpId rather than location.hostname is what makes
                    this agree with what would actually happen. */}
                {rpID && p.rp_id !== rpID ? (
                  <div className="text-xs text-warn">
                    set up for <span className="font-mono">{p.rp_id}</span> — will not work at{" "}
                    <span className="font-mono">{rpID}</span>
                  </div>
                ) : null}
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => remove.mutate(p.id)}
                disabled={remove.isPending}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="mt-4">
        <Button type="button" onClick={() => setAdding(true)} disabled={!supported}>
          Add a passkey
        </Button>
      </div>

      <AddPasskeyDialog open={adding} onOpenChange={setAdding} onAdded={invalidate} />

      {/* THE rpId HAZARD, STATED WHERE THE CREDENTIAL IS CREATED — the ruling asks for exactly this,
          and not only in the docs. Nothing in WebAuthn warns about it, and the failure looks like
          "no credential here" rather than like a move. */}
      {supported ? (
        <p className="mt-3 text-xs text-muted">
          A passkey is tied to the address you set it up on —{" "}
          <span className="font-mono">{rpID}</span>. If you later reach quince by a different
          name, your passkeys stop working there and you sign in with your password instead.
        </p>
      ) : null}
    </div>
  );
}
