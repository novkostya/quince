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
        <div className="mt-6 grid gap-8 lg:grid-cols-2">
          <div>
            <h2 className="text-sm font-semibold text-muted">Edit</h2>
            <div className="mt-3">
              <ConfigEditor config={data.config} />
            </div>
          </div>
          <div>
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
