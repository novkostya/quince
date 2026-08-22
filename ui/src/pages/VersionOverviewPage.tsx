import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { FolderTree } from "lucide-react";
import { BackLink } from "@/components/BackLink";
import { Card } from "@/components/ui/card";
import { api, messageFor } from "@/lib/api";
import type { Version, VersionOverview } from "@/lib/types";
import { useDevicesStore } from "@/stores/devices";
import { useVersionsStore } from "@/stores/versions";
import { VersionSummary } from "@/features/overview/VersionSummary";

// VersionOverviewPage is what a version IS — qn.9 slice 10, the rung's primary surface.
//
// IT IS THE VERSION'S PAGE, at `/versions/:id`, and the file browser moved one click behind
// it. The rung opened on "this is just test UI, it's not going to be in the end product" said
// about that browser; D9 is explicit that the answer is to stop it being PRIMARY rather than
// to delete it — it is qn.8's gate, the escape hatch for a domain no viewer models, and the
// only surface that reaches a file nothing models. The link out is below and stays.
//
// NO PASSWORD IS ASKED FOR HERE AND NONE CAN BE. Everything on this screen comes from the
// three unencrypted plists, so the page renders on arrival with no session, no unlock dialog
// and no cache to tear down at lock. That is the tier, not an optimisation.
//
// ROUTED ON THE VERSION for the reason VaultBrowsePage already gives: a session id dies at
// its TTL and a link carrying one is stale within minutes, where a version id is durable.
export function VersionOverviewPage() {
  const { id = "" } = useParams();

  const overview = useQuery({
    queryKey: ["version-overview", id],
    queryFn: () => api.get<VersionOverview>(`/api/versions/${id}/overview`),
    enabled: id !== "",
    // The tier is read off an IMMUTABLE version — a committed version is never mutated, by a
    // hard rule — so this answer cannot go stale while the page is open. The server memoises
    // the same read for the same reason.
    staleTime: Infinity,
  });

  // Same cold-deep-link fallback DeviceDetailsPage and VaultBrowsePage use: the store is
  // empty until the WS connects, and there is no `GET /api/versions/{id}` — the collection
  // route is the one that exists.
  const fromStore = useVersionsStore((s) => s.byId[id]);
  const all = useQuery({
    queryKey: ["versions"],
    queryFn: () => api.get<{ versions: Version[] }>("/api/versions"),
    enabled: !fromStore && id !== "",
  });
  const version = fromStore ?? all.data?.versions.find((v) => v.id === id);
  const deviceName = useDevicesStore((s) => (version ? s.byUdid[version.udid]?.name : undefined));

  return (
    <div className="flex flex-col gap-4">
      <BackLink to={version ? `/devices/${version.udid}` : "/"}>
        {deviceName ?? "Back"}
      </BackLink>

      {overview.isPending ? (
        <Card>
          <p className="text-sm text-muted">Reading this backup…</p>
        </Card>
      ) : overview.isError ? (
        <Card>
          {/* THE SERVER'S OWN MESSAGE, not a generic one. `corrupt_manifest` and `not_found`
              mean different things and have different remedies — a broken backup versus a
              version whose artifact is gone — and collapsing them is the defect this rung is
              named after. */}
          <p className="text-sm text-danger">
            {messageFor(overview.error, "This backup could not be read.")}
          </p>
        </Card>
      ) : (
        <VersionSummary overview={overview.data} deviceName={deviceName} />
      )}

      {/* D9 — THE BROWSER STAYS, one click away. Kept even while the overview is loading or
          has failed: a version whose plists will not parse is exactly when somebody wants the
          file tree, and hiding the escape hatch on error would remove the remedy along with
          the diagnosis. */}
      <Card>
        <Link
          to={`/versions/${id}/browse`}
          className="flex items-center gap-2 text-sm text-accent hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
        >
          <FolderTree size={16} aria-hidden />
          Browse the files in this backup
        </Link>
        <p className="mt-1 text-xs text-muted">
          Every file, as stored. Needs the backup password.
        </p>
      </Card>
    </div>
  );
}
