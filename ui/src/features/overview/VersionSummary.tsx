import * as React from "react";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { RelativeTime } from "@/components/RelativeTime";
import type { VersionOverview } from "@/lib/types";

// VersionSummary renders the PRE-UNLOCK tier — what this backup is, before it is opened.
//
// IT ASKS FOR NO PASSWORD AND SHOWS NO FILE TREE. That is qn.9's whole point: the rung began
// with "this is just test UI, it's not going to be in the end product" said about a file
// browser, and the answer is a screen that says what is IN a backup rather than which files
// it holds. The browser stays and is linked from here (D9) — it is the escape hatch for a
// domain no viewer models, not a thing this replaces.

// A row of one labelled fact. `value` is a node so a caller can pass a Badge or an explicit
// unknown rather than a string that has to be parsed back.
function Fact({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5">
      <dt className="text-sm text-muted">{label}</dt>
      <dd className="text-sm text-fg text-right">{value}</dd>
    </div>
  );
}

// Unknown is a VALUE, not an empty cell.
//
// A blank space says "there is nothing here"; this says "quince does not know", which is a
// different fact and the one the state-honesty rule asks for. It exists as a component so
// every unknown on this screen looks identical — the moment one of them renders as "—" and
// another as "0", the screen is making a claim it cannot support.
function Unknown({ why }: { why: string }) {
  return (
    <span className="text-muted italic" title={why}>
      unknown
    </span>
  );
}

const KIND_LABEL: Record<VersionOverview["kind"], string> = {
  full: "Full backup",
  incremental: "Incremental",
  // NOT "unknown backup" and not a guess. An adopted version — found on disk with no job
  // record — genuinely has no recorded kind, and Status.plist's IsFullBackup is NOT a
  // fallback: it lies, and substituting it converts "we do not know" into "we are wrong"
  // (quince#1466).
  unknown: "Kind not recorded",
};

export function VersionSummary({
  overview,
  deviceName,
}: {
  overview: VersionOverview;
  deviceName?: string;
}) {
  const { device, backup, apps } = overview;
  // The name recorded IN this backup, falling back to what the device is called now. They
  // differ for a version taken before a rename, which is exactly why the per-version one is
  // worth showing.
  const title = device.present && device.name ? device.name : (deviceName ?? "This backup");

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-medium text-fg">{title}</h2>
          {overview.encrypted ? (
            <Badge tone="ok">Encrypted</Badge>
          ) : (
            <Badge tone="warn">Not encrypted</Badge>
          )}
          <Badge tone="neutral">{KIND_LABEL[overview.kind]}</Badge>
        </div>

        <dl className="mt-3 divide-y divide-line">
          <Fact label="Taken" value={<RelativeTime iso={overview.created_at} />} />
          {device.present ? (
            <>
              <Fact
                label="iOS"
                value={
                  device.ios_version ? (
                    <>
                      {device.ios_version}
                      {device.build_version ? (
                        <span className="text-muted"> ({device.build_version})</span>
                      ) : null}
                    </>
                  ) : (
                    <Unknown why="This backup's Manifest.plist carries no iOS version." />
                  )
                }
              />
              {/* THE RAW MODEL IDENTIFIER, deliberately. quince holds no model-name table and
                  an unmaintained one goes stale quietly, so this is honest rather than
                  confidently wrong (D2). `class` supplies the human word beside it. */}
              <Fact
                label="Model"
                value={
                  device.product_type ? (
                    <>
                      {device.class ? `${device.class} ` : ""}
                      <span className="font-mono text-xs">{device.product_type}</span>
                    </>
                  ) : (
                    device.class || <Unknown why="No model identifier in this backup." />
                  )
                }
              />
            </>
          ) : (
            <Fact
              label="Device"
              value={<Unknown why="This backup carries no Manifest.plist." />}
            />
          )}
          {/* ALWAYS UNKNOWN ON THIS SCREEN, and said rather than omitted. The file list is
              inside the encrypted index, so counting it needs the password — a 0 here would
              be a lie about a perfectly good backup (story 7). */}
          <Fact
            label="Files"
            value={
              overview.file_count === null ? (
                <Unknown why="The file index is encrypted. Unlock this backup to count it." />
              ) : (
                overview.file_count.toLocaleString()
              )
            }
          />
        </dl>
      </Card>

      <AppList apps={apps} />

      <DeviceDetails overview={overview} />

      {backup.present && (backup.state || backup.snapshot_state) ? (
        <Card>
          <h3 className="text-sm font-medium text-fg">As the device recorded it</h3>
          <dl className="mt-2 divide-y divide-line">
            {backup.snapshot_state ? (
              <Fact label="Snapshot" value={backup.snapshot_state} />
            ) : null}
            {backup.state ? <Fact label="State" value={backup.state} /> : null}
            {backup.format_version ? (
              // NAMED "Backup format", never "Version" — the plist calls it Version and it is
              // not the iOS version. Two things wearing one word is a one-word bug.
              <Fact label="Backup format" value={backup.format_version} />
            ) : null}
          </dl>
        </Card>
      ) : null}
    </div>
  );
}

// AppList renders the USER-INSTALLED apps and says so in as many words.
//
// "APPS" MEANS ONE OF FOUR COUNTS AND THE LABEL MUST SAY WHICH (D3). One measured tablet
// yields 21 user-installed bundles, 1,203 bundles with a container, 1,205 app domains holding
// files and 1,264 domains in total. All four are true of that backup and they answer
// different questions, so a bare "1,205 apps" is the collapsed diagnostic the troubleshooting
// rule forbids EVEN THOUGH every word of it is true. This one is the list a person could
// check against their own home screen.
function AppList({ apps }: { apps: VersionOverview["apps"] }) {
  if (!apps.present) {
    return (
      <Card>
        <h3 className="text-sm font-medium text-fg">Apps you installed</h3>
        {/* ABSENT IS NOT EMPTY. No Info.plist means quince cannot know the list, which is a
            different statement from a backup that genuinely holds no apps. */}
        <p className="mt-1 text-sm text-muted">
          This backup carries no <span className="font-mono text-xs">Info.plist</span>, so the
          installed-app list is not recorded in it.
        </p>
      </Card>
    );
  }
  return (
    <Card>
      <div className="flex items-baseline justify-between gap-4">
        <h3 className="text-sm font-medium text-fg">Apps you installed</h3>
        <span className="text-sm text-muted">{apps.bundle_ids.length}</span>
      </div>
      {apps.bundle_ids.length === 0 ? (
        <p className="mt-1 text-sm text-muted">No apps were installed when this backup ran.</p>
      ) : (
        <ul className="mt-2 flex flex-col gap-1">
          {apps.bundle_ids.map((id) => (
            <li key={id} className="font-mono text-xs text-fg">
              {id}
            </li>
          ))}
        </ul>
      )}
      {/* SIZES ARRIVE WITH THE UNLOCK, and the screen says so rather than showing zeroes.
          Per-app sizes come from the aggregate over the encrypted index (story 2/3). */}
      <p className="mt-3 text-xs text-muted">
        Sizes for each app need the backup password.
      </p>
    </Card>
  );
}

// DeviceDetails holds the identifiers that are IN SCOPE and NOT IN THE DEFAULT VIEW.
//
// D1's ruling puts everything the format yields without a password in scope, and D10 makes a
// taste decision inside that ruling rather than a narrowing of it: a serial number, an IMEI,
// an ICCID and a phone number are never the answer to "what is in this backup", and a
// screenshot is the likeliest way any of it leaves the Operator's machine. So they are one
// click away rather than on the screen.
//
// A <details>, NOT a dialog and not a route. It is a disclosure of facts already loaded — no
// second fetch, no state, and closed is its resting state on every visit. It is also not a
// PERMISSION boundary and must not be mistaken for one: a scoped holder who can see this
// version can open this element, which is D8's decision and not this component's.
function DeviceDetails({ overview }: { overview: VersionOverview }) {
  const { device, apps } = overview;
  const rows: { label: string; value: string }[] = [];
  if (device.present) {
    if (device.serial_number) rows.push({ label: "Serial number", value: device.serial_number });
    if (device.unique_device_id) rows.push({ label: "Device ID", value: device.unique_device_id });
  }
  if (apps.present) {
    if (apps.cellular.phone_number)
      rows.push({ label: "Phone number", value: apps.cellular.phone_number });
    if (apps.cellular.imei) rows.push({ label: "IMEI", value: apps.cellular.imei });
    if (apps.cellular.iccid) rows.push({ label: "ICCID", value: apps.cellular.iccid });
    if (apps.itunes_version) rows.push({ label: "Backed up by", value: apps.itunes_version });
  }
  // Nothing to disclose — a tablet has no cellular identifiers at all, and an empty
  // disclosure inviting a click that reveals nothing is worse than no control.
  if (rows.length === 0) return null;

  return (
    <Card>
      <details className="group">
        <summary className="cursor-pointer list-none text-sm font-medium text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent">
          Device identifiers
          <span className="ml-2 text-xs font-normal text-muted">
            ({rows.length}) — shown only when you ask
          </span>
        </summary>
        <dl className="mt-2 divide-y divide-line">
          {rows.map((r) => (
            <Fact
              key={r.label}
              label={r.label}
              value={<span className="font-mono text-xs">{r.value}</span>}
            />
          ))}
        </dl>
      </details>
    </Card>
  );
}
