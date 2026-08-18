import { Button } from "@/components/ui/button";

// THE CONFIGURATION MOVED WHILE YOU WERE EDITING (quince#764). Rendered ABOVE a form's Save row on
// purpose: it is a fact about what Save will do, and a reader who meets it after pressing the button
// has already shipped the stale section.
//
// It says what will happen rather than only what happened, because `PUT /api/config` is a
// full-document replace — saving now overwrites the change somebody else made. The action is the
// other direction and is labelled with its cost: taking the new version discards this edit. Neither
// side is dropped without being chosen, which is the rule this mechanism exists to establish.
//
// SHARED SINCE quince#1212, when `/settings/notifications` became a second editor of the same
// document. The wording is the load-bearing part — two forms telling the user different things about
// one hazard is worse than either sentence alone — so it lives in one place rather than being copied
// beside each Save row.
export function ConfigStaleNotice({ onTakeServerVersion }: { onTakeServerVersion: () => void }) {
  return (
    <div role="status" className="rounded-card border border-line bg-accent-soft p-3 text-sm text-warn">
      <div className="font-medium">The configuration changed elsewhere</div>
      <p className="mt-1 text-xs">
        Something else — a hand-edit, the CLI, another tab — changed <code>config.yml</code> since
        this form loaded. Saving now replaces it with what you see here. Your unsaved edits are kept
        until you choose.
      </p>
      <Button type="button" className="mt-2" onClick={onTakeServerVersion}>
        Discard my edits and load the new version
      </Button>
    </div>
  );
}
