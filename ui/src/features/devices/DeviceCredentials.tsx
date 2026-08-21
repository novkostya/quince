import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { RelativeTime } from "@/components/RelativeTime";
import { messageFor } from "@/lib/api";
import { ReauthChallenge } from "@/features/auth/ReauthChallenge";
import { usePasskeyList, scopedTo } from "@/features/settings/Passkeys";
import { usePasskeyRemoval } from "@/features/settings/usePasskeyRemoval";
import type { Device } from "@/lib/types";

// DeviceCredentials — who currently holds a passkey for THIS device, and the admin's revoke.
//
// D9, IN ITS OWN WORDS: "the admin revokes ONE scoped credential without touching the others, from
// the device page it was issued from." The Settings list can already remove any credential; what it
// cannot do is answer *who has access to the hallway tablet* without the admin reading every row
// and matching badges. This section is that question asked where it arises.
//
// IT SITS BESIDE THE OUTSTANDING CODES DELIBERATELY. `DeviceEnrolment` lists authority that has been
// HANDED OUT and not yet used; this lists authority that has been USED and is live. Together they
// are the whole answer to *what have I issued for this device*, which is the question D9 says the
// admin must be able to answer — and either half alone is a confident, incomplete answer.
//
// NO NEW ENDPOINT AND NO NEW QUERY. `usePasskeyList` is the same request `/settings/auth` makes, and
// React Query dedupes it by key, so opening this page costs nothing extra and the two surfaces
// cannot disagree about what exists. Filtering client-side is right here because the payload is the
// whole list either way: a `?udid=` filter would be a second server behaviour to keep in step for a
// list bounded by how many passkeys one household has.
//
// ADMIN-ONLY BY THE ROUTE IT IS ON, and by the API underneath it. A scoped holder never reaches this
// page's admin surface, and `GET /api/auth/passkeys` refuses them regardless — this component makes
// no authorization decision and must not look like it does.
export function DeviceCredentials({ device }: { device: Device }) {
  const list = usePasskeyList();

  // DEFENSIVE ABOUT THE SHAPE, for the reason `Passkeys.tsx` gives about its own list: this renders
  // inside the device page beside the job history and the version list, so a throw here takes a
  // page the admin needs for entirely unrelated reasons.
  const all = Array.isArray(list.data?.passkeys) ? list.data.passkeys : [];
  const rows = all.filter((p) => scopedTo(p) === device.udid);

  const { remove, challenge, setChallenge, ceremonyErr } = usePasskeyRemoval({
    onRemoved: () => void list.refetch(),
    // NOT THIS SURFACE'S QUESTION. "Was that the last credential this browser can use" is about the
    // whole install, and this component sees one device's rows — so it declines to answer rather
    // than answering from a subset, and the hook's credential-id check carries the case that
    // matters here: the admin revoking the passkey THIS browser remembers.
    isLastAt: () => false,
  });

  // NOTHING AT ALL WHEN NOBODY HOLDS ONE, rather than an empty-state card. The common device has no
  // scoped credential and never will, and a permanent "no shared access" panel on every device page
  // is noise that makes the real case harder to notice. `DeviceEnrolment` above is what offers the
  // action, so there is no dead end here.
  if (rows.length === 0) return null;

  return (
    <div className="mt-4 flex flex-col gap-2">
      <p className="text-sm font-medium">Passkeys for this device</p>
      {/* WHAT REVOKING MEANS, SAID BEFORE IT IS DONE. quince#1001 landed, so a removed credential's
          sessions end with it — that is a real and immediate consequence, and the admin is entitled
          to know it is immediate rather than discovering it from a household member. */}
      <p className="text-sm text-muted">
        Each of these signs in to this device and nothing else. Removing one signs that person out
        straight away.
      </p>
      <ul className="flex flex-col gap-2">
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
                  // NEVER USED IS THE INTERESTING ROW HERE, more than in Settings: a scoped
                  // credential nobody has signed in with may mean the QR was scanned by the wrong
                  // person, which is the case worth revoking on sight.
                  " · never used"
                )}
              </div>
            </div>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => remove.mutate({ id: p.id })}
              disabled={remove.isPending}
            >
              Remove
            </Button>
          </li>
        ))}
      </ul>

      {/* THE REFUSAL HAS TO LAND SOMEWHERE — the same rule the Settings list follows, and the same
          reason: a `DELETE` that is refused and says nothing is the silent-fallback shape the hard
          rules forbid. The server's own sentence, because it knows why and this client does not. */}
      {remove.isError ? (
        <p className="text-sm text-danger">{messageFor(remove.error, "Could not remove the passkey.")}</p>
      ) : null}

      {/* THE CEREMONY'S OWN FAILURE, which the mutation cannot carry: when the assertion is what
          went wrong, `remove` was never called. Never shown for a DISMISSED sheet. */}
      {ceremonyErr ? <p className="text-sm text-danger">{ceremonyErr}</p> : null}

      {/* MANAGING EVERY CREDENTIAL IS STILL SETTINGS' JOB. This section is deliberately about one
          device, so the way to the whole list is a link rather than a duplicated surface. */}
      <p className="text-xs text-muted">
        <Link to="/settings/auth" className="underline underline-offset-2">
          All passkeys
        </Link>{" "}
        are in Sign-in settings.
      </p>

      {challenge ? (
        <ReauthChallenge
          operation="remove_passkey"
          target={challenge.id}
          accepts={challenge.accepts}
          title="Confirm it is you"
          subtitle="Removing a passkey changes who can reach this device, so quince needs a different credential you have right now."
          onCancel={() => {
            setChallenge(null);
            remove.reset();
          }}
          onProved={async (present) => {
            await remove.mutateAsync({ id: challenge.id, present });
          }}
        />
      ) : null}
    </div>
  );
}
