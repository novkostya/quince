import { useConfig } from "@/lib/config";
import { ConfigView } from "@/features/settings/ConfigView";
import { ConfigEditor } from "@/features/settings/ConfigEditor";

export function SettingsPage() {
  const { data, isLoading, isError } = useConfig();
  return (
    <section>
      <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
      <p className="mt-1 text-sm text-muted">
        quince is configured by one file, <code className="font-mono">config.yml</code>. Edit safe
        keys here or by hand; changes apply on restart (live reload lands later).
      </p>

      {isLoading ? <div className="mt-6 text-sm text-muted">Loading…</div> : null}
      {isError ? <div className="mt-6 text-sm text-danger">Could not load configuration.</div> : null}

      {data ? (
        // `min-w-0` ON BOTH COLUMNS IS THE STRUCTURAL HALF OF quince#631, and it is not decoration.
        //
        // A grid item defaults to `min-width: auto`, so a column will not shrink below the INTRINSIC
        // width of its widest child. One long line in the config dump therefore widened this column,
        // this grid, and the whole content area with it — sliding the editor's own fields off the
        // left edge on a phone, so you had to scroll back to reach them.
        //
        // `AppLayout` already installs this guard one level up and states the intent in a comment:
        // `min-w-0` on `<main>` so wide children scroll inside themselves rather than moving the
        // page. THE CHAIN WAS BROKEN HERE — `<main>` could shrink, this column could not, and a
        // guard is only as strong as the shortest link below it.
        //
        // Both columns, not only the one that overflowed. The editor renders config VALUES too and
        // is one long default away from the same behaviour; fixing the column that happened to
        // break first leaves the same bug waiting on the other.
        <div className="mt-6 grid gap-8 lg:grid-cols-2">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-muted">Edit</h2>
            <div className="mt-3">
              <ConfigEditor config={data.config} />
            </div>
          </div>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-muted">Current configuration</h2>
            <div className="mt-3">
              <ConfigView data={data} />
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}
