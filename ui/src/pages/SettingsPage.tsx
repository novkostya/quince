import { useConfig } from "@/lib/config";
import { ConfigView } from "@/features/settings/ConfigView";
import { ConfigEditor } from "@/features/settings/ConfigEditor";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";

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

      {/* THE PASSKEYS CARD HAS MOVED TO `/settings/auth` — quince#841 ruling A, qn.6m slice 6. What
          stands here is a LINK, and the move settles an argument this spot had already half-lost.

          quince#834 put the card in the Edit column, above the file, on the reading that "passkeys
          are a thing you CHANGE, so they belong beside the other things you change". Ruling A draws
          the line one step further out: **auth is not configuration**, and Settings is a config
          editor plus a config dump plus storages — a fourth section makes it a drawer.

          OUTSIDE THE `data` GUARD, WHICH IS THE POINT AND NOT A DETAIL. quince#834 stated and
          accepted a cost: the card sat inside that guard, so a box whose config failed to LOAD showed
          no passkey surface at all. Moving the card to its own page only pays that back if the way to
          REACH it is not behind the same condition — a link inside the guard would leave somebody
          with a broken config unable to get at the credentials they sign in with, which is the same
          defect one level up. Asserted, not just intended: SettingsAuthPage.test.tsx renders this
          page with the config query REJECTING and requires the link to still be there.

          Above the columns rather than below: on a phone they stack, and the config dump is long
          enough to bury anything after it. */}
      <Link
        to="/settings/auth"
        className="mt-6 flex max-w-xl items-center justify-between rounded-card border border-line bg-card px-4 py-3 text-sm transition-colors hover:bg-elevated"
      >
        <span>
          <span className="font-medium">Sign-in</span>
          <span className="mt-0.5 block text-muted">Password and passkeys</span>
        </span>
        <ChevronRight size={18} strokeWidth={1.75} className="shrink-0 text-muted" />
      </Link>

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
