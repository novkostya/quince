import { useConfig } from "@/lib/config";
import { ConfigView } from "@/features/settings/ConfigView";
import { ConfigEditor } from "@/features/settings/ConfigEditor";
import { Passkeys } from "@/features/settings/Passkeys";

export function SettingsPage() {
  const { data, isLoading, isError } = useConfig();
  return (
    <section>
      <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
      {/* THE TWO EDITING PATHS DIFFER, AND THIS LINE IS WHERE A USER FINDS THAT OUT (`qn.6g`,
          quince#577). It read *"changes apply on restart (live reload lands later)"* — false for
          the form below as of this rung, and this is the only place the difference is stated.

          A change made HERE applies immediately. The SAME change hand-edited into `config.yml` does
          not, because nothing watches the file — file-watch was ruled into its own rung on
          2026-08-04, option (a). Saying only *"changes apply immediately"* would be the wider claim
          this project keeps paying for; saying nothing would leave a hand-editor waiting for an
          effect that never arrives. */}
      <p className="mt-1 text-sm text-muted">
        quince is configured by one file, <code className="font-mono">config.yml</code>. Changes made
        here apply immediately. You can edit the file by hand instead — quince picks those up when it
        restarts.
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
            {/* IN THE SETTINGS COLUMN, ABOVE THE FILE — Operator-directed, and it is the right
                reading of the page. Passkeys are a thing you CHANGE, so they belong beside the other
                things you change rather than after the read-only dump of the file. On a phone the
                columns stack, so this also puts them ahead of a config listing that is long enough
                to bury anything below it — which is how they were first seen, and why it was raised.

                THE COST, STATED: this is inside the `data` guard, so a box whose config fails to
                load now shows no passkey surface either. That was the one argument for keeping it
                outside, and it is the weaker one — a Settings page that cannot read its own config
                is broken in a way the password login already handles. */}
            <Passkeys />
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
