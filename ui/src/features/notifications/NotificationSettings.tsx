import { useState } from "react";
import { Link } from "react-router-dom";
import { useShallow } from "zustand/react/shallow";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Config, ConfigFieldError } from "@/lib/types";
import { configKey, updateConfig, useConfigDraft } from "@/lib/config";
import { APIError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { CheckboxRow } from "@/components/ui/checkbox-row";
import { Input } from "@/components/ui/input";
import { SectionHeading } from "@/components/ui/section-heading";
import { Field } from "@/features/settings/ConfigEditor";
import { ConfigStaleNotice } from "@/features/settings/ConfigStaleNotice";
import { useDevicesStore } from "@/stores/devices";

// The `notifications:` section of `config.yml`, on the page that is about notifications (quince#1212).
//
// EIGHT SETTINGS REACHED THE API AND NONE REACHED A SCREEN. That is a D12 violation stated in as many
// words — *"every setting has a sane default, is editable in the UI"* — and it cost more than tidiness:
// `backup_completed` defaults to **false**, so a whole kind of notification was off with no way to
// turn it on short of hand-editing the file, which inverts D12's promise that the UI edits the file.
//
// HERE RATHER THAN IN `ConfigEditor`, by Operator ruling 2026-08-18 (quince#1212). Somebody who has
// just been told on this page that a category is off should be able to turn it on where they were
// told, not on a different page under a heading that says nothing about notifications.
//
// IT RENDERS WHATEVER THIS BROWSER CAN DO, and that is deliberate rather than an oversight. These
// keys govern what quince SENDS to every subscribed device; they are not a property of the browser
// reading them. An iPhone in Lockdown Mode, or a desktop browser with no Push API, is still a
// perfectly good place to configure the phone that does receive them — so this sits OUTSIDE the
// `support === "supported"` branch that guards the subscribe controls.
//
// TWO GROUPS, NOT ONE LIST, and the split is the answer to quince#1212's third open question. Five
// booleans say WHAT quince tells you and three numbers say WHEN it decides there is anything to say.
// They are different questions with different failure modes: a boolean is instantly understood and
// needs no validation, a duration needs units, a floor, and a cross-field rule the server enforces
// (`overdue_days >= staleness_days`). Rendering them as one undifferentiated column of controls
// would make the switches look like tuning and the numbers look like preferences.
//
// ONE SAVE FOR THE WHOLE SECTION, matching `ConfigEditor`. `PUT /api/config` is a full-document
// replace, so a per-control autosave would ship the entire document on every keystroke in a number
// field — and each of those is a chance to overwrite what the other config surface just wrote.

// The five kinds, in the order a person meets them: the two reminders that are the point of the
// feature, then the two things that went wrong, then the one that is off by default.
//
// THE LABELS ARE THE NOTIFICATION TITLES, not the config keys. `notify.go` sends `Backup Due`,
// `Backup Overdue`, `Action Needed`, `Backup Failed` and `Backup Complete`; a switch labelled
// `backup_available` would be a different vocabulary for the same thing, and the user is being asked
// *do you want to receive this* about something they have seen on a lock screen.
const CATEGORIES: {
  key: "backup_available" | "backup_overdue" | "action_required" | "backup_failed" | "backup_completed";
  label: string;
  hint: string;
}[] = [
  {
    key: "backup_available",
    // A DEVICE THAT HAS NEVER BEEN BACKED UP HAS NO OTHER RANK TO FALL TO. `Evaluate` gives a
    // never-backed-up device `KindBackupAvailable` and nothing else — the overdue rank is reachable
    // only by AGE, and a device with no last backup has no age quince will reason about. So this
    // switch off means a newly paired phone is never reminded, ever, and nothing else on the screen
    // would tell somebody that. Named on the switch rather than left to be discovered.
    label: "A backup is due",
    hint: "Sent when a device quince has not backed up recently appears on the network — and the only reminder a device that has NEVER been backed up can get. This is the one that makes Wi-Fi backups happen.",
  },
  {
    key: "backup_overdue",
    // TURNING THIS OFF DOES NOT FALL BACK TO THE ONE ABOVE, and the hint has to say so because
    // nothing on the screen would suggest otherwise. `Evaluate` picks the rank first and then checks
    // whether that rank is enabled, so a device past `overdue_days` with this off is told NOTHING —
    // it does not degrade to "a backup is due". That is the *no silent caps* rule applied to a
    // switch: the degraded mode is surfaced where the switch is.
    label: "A backup is badly overdue",
    hint: "The same reminder, worded more firmly, once a device passes the overdue threshold below. Turning this off silences those devices completely — they do not fall back to the reminder above.",
  },
  {
    key: "action_required",
    label: "A backup needs you",
    hint: "Sent when quince cannot finish on its own — the device is locked, a password is needed, or it must be trusted.",
  },
  { key: "backup_failed", label: "A backup failed", hint: "Sent when a backup stops on an error quince cannot work around." },
  {
    key: "backup_completed",
    label: "A backup finished",
    // WHY IT IS OFF BY DEFAULT, on the switch rather than in a doc. A push per successful nightly
    // backup is the fastest way to teach somebody to swipe quince's notifications away without
    // reading them — after which the one that mattered goes with the rest.
    hint: "Off by default. A notification after every successful backup is a lot of them, and the habit it builds is swiping quince away without reading.",
  },
];

export function NotificationSettings({ config }: { config: Config }) {
  const qc = useQueryClient();
  const { draft, setDraft, staleElsewhere, takeServerVersion, adopt } = useConfigDraft(config);
  const [errors, setErrors] = useState<ConfigFieldError[]>([]);
  const [saved, setSaved] = useState(false);

  const mutation = useMutation({
    mutationFn: (c: Config) => updateConfig(c),
    onSuccess: (resp) => {
      setErrors([]);
      setSaved(true);
      adopt(resp.config);
      // THE OTHER CONFIG SURFACE IS INVALIDATED TOO, because there is only one query key for one
      // document. This is what makes `/settings` correct after a save here rather than merely
      // eventually correct.
      void qc.invalidateQueries({ queryKey: configKey });
    },
    onError: (err: unknown) => {
      setSaved(false);
      if (err instanceof APIError && err.status === 422) {
        const details = err.details as { errors?: ConfigFieldError[] } | undefined;
        setErrors(details?.errors ?? []);
      } else {
        setErrors([{ path: "", message: err instanceof Error ? err.message : "save failed" }]);
      }
    },
  });

  const errFor = (path: string) => errors.find((e) => e.path === path)?.message;

  // SPREAD THE SECTION, NEVER REBUILD IT. `ConfigEditor`'s own comment states the hazard: a
  // `{ ...draft, notifications: {…} }` written as a literal REPLACES the section, so any key of
  // `notifications:` this form does not render is dropped on save — and PUT is a full-document
  // replace, so dropped means reset to the Go zero value. It renders all eight today; this helper is
  // what keeps that true when a ninth is added.
  //
  // IT ALSO PRESERVES KEY ORDER, which `useConfigDraft` depends on: its dirty test stringifies the
  // draft against the synced document, and a section rebuilt in a different key order reads dirty
  // when nobody touched it, which silently stops the re-sync.
  const setNotifications = (patch: Partial<Config["notifications"]>) =>
    setDraft({ ...draft, notifications: { ...draft.notifications, ...patch } });

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        setSaved(false);
        mutation.mutate(draft);
      }}
    >
      <div>
        <SectionHeading>What quince tells you</SectionHeading>
        <CategoryCoverageNotice notifications={draft.notifications} />
        <DeviceCoverageNotice />
        <ul className="mt-3 flex flex-col gap-3">
          {CATEGORIES.map((c) => (
            <li key={c.key}>
              {/* A NESTED <label>, not a `Field` with an `htmlFor` — `CheckboxRow` is that label.
                  Radix associates implicitly this way and a checkbox's label belongs beside it
                  rather than above it, which is what `ConfigEditor`'s `Field` comment says to do
                  when the control is inside the label.

                  THE LINE-BOX ALIGNMENT MOVED INTO THE PRIMITIVE (quince#1227). It was written out
                  here, and before that as a `mt-0.5` nudge; both were page-local, which is why this
                  screen could ship the broken form after the fix already existed one directory
                  away. The reasoning now lives in `components/ui/checkbox-row.tsx`. */}
              <CheckboxRow
                checked={draft.notifications[c.key]}
                onChange={(e) => setNotifications({ [c.key]: e.target.checked })}
              >
                <span className="min-w-0">
                  <span className="font-medium">{c.label}</span>
                  <span className="mt-0.5 block text-xs text-muted">{c.hint}</span>
                </span>
              </CheckboxRow>
              {errFor(`notifications.${c.key}`) ? (
                <span className="mt-1 block text-xs text-danger">{errFor(`notifications.${c.key}`)}</span>
              ) : null}
            </li>
          ))}
        </ul>
        {/* WHAT THIS SCREEN DOES NOT SAY OTHERWISE (Operator, 2026-08-20, screenshot with every
            category on). The five switches above are the whole of the policy a reader can SEE, so
            in the all-enabled state — the state a working install sits in — nothing on this page
            reveals that a single device can be excluded. `DeviceCoverageNotice` reports the
            exception and returns null when there is none, which means the feature is discoverable
            only to somebody who has already found it.

            IT NAMES THE SCOPE FIRST, because the misconception is about the switches above rather
            than about the missing control: their real defect is not being incomplete, it is reading
            as per-install when they are per-EVERYTHING. Saying "these apply to every device" is what
            makes the next sentence land.

            MUTED, NOT WARN, AND ALWAYS PRESENT. It is an orientation line, not a report about a
            state — the warn-toned notice above is what reports state, and it names the devices. The
            two coexist deliberately: that one says WHAT is excluded, this one says exclusion EXISTS.

            THE LINK GOES TO THE DEVICE LIST, not to a device. There is no single right device to
            send anybody to, and `/devices` redirects to `/` (router.tsx), so `/` is the honest
            destination and "Devices" is what the reader sees it called. */}
        <p className="mt-3 max-w-md text-xs text-muted">
          These apply to every device. To silence just one — without losing the category for the
          rest — turn its notifications off from{" "}
          <Link className="underline underline-offset-2" to="/">
            Devices
          </Link>
          .
        </p>
      </div>

      <div>
        <SectionHeading>When quince decides a backup is due</SectionHeading>
        <p className="mt-1 text-xs text-muted">
          quince only reminds you about a device that is on the network right now, so a reminder is
          always something you can act on.
        </p>
        <div className="mt-3 flex max-w-md flex-col gap-4">
          {/* `min={0}` FOR ALL THREE, matching the server, which refuses a negative with a 422 on
              exactly these paths (`config.Validate`). 0 IS A MEANING here rather than a floor and it
              is not the same meaning as `reconcile.interval_minutes`' 0: it does not turn anything
              off, it makes every visible device due at once — bounded only by the cooldown below. The
              hint says so, because a bare number input cannot. */}
          <Field label="Due after (days)" error={errFor("notifications.staleness_days")}>
            {(id) => (
              <>
                <Input
                  id={id}
                  type="number"
                  min={0}
                  value={draft.notifications.staleness_days}
                  onChange={(e) => setNotifications({ staleness_days: Number(e.target.value) })}
                />
                <p className="text-xs text-muted">
                  How long since the last good backup before quince starts reminding you.{" "}
                  <strong>0 means every device is due</strong> the moment it appears.
                </p>
              </>
            )}
          </Field>

          {/* THE CROSS-FIELD RULE IS THE SERVER'S AND IS SURFACED AS ITS 422, not re-implemented
              here. `Validate` refuses `overdue_days < staleness_days` — *"a device cannot be overdue
              before it is stale"* — and a client-side copy of that rule is a second place for it to
              drift. The hint states the constraint so it is not discovered by being refused. */}
          <Field label="Overdue after (days)" error={errFor("notifications.overdue_days")}>
            {(id) => (
              <>
                <Input
                  id={id}
                  type="number"
                  min={0}
                  value={draft.notifications.overdue_days}
                  onChange={(e) => setNotifications({ overdue_days: Number(e.target.value) })}
                />
                <p className="text-xs text-muted">
                  When the reminder starts saying &ldquo;overdue&rdquo; instead of
                  &ldquo;due&rdquo;. Must be at least as long as the due threshold above.
                </p>
              </>
            )}
          </Field>

          <Field label="Wait between reminders (hours)" error={errFor("notifications.reminder_cooldown_hours")}>
            {(id) => (
              <>
                <Input
                  id={id}
                  type="number"
                  min={0}
                  value={draft.notifications.reminder_cooldown_hours}
                  onChange={(e) =>
                    setNotifications({ reminder_cooldown_hours: Number(e.target.value) })
                  }
                />
                {/* ONE DEVICE, NOT ALL OF THEM, and this is the sentence that stops the setting being
                    read as a global mute. The track is per device (spec D5), so two phones both
                    overdue are two reminders — which is right, and surprising if the label is read
                    as "at most one notification a day". */}
                <p className="text-xs text-muted">
                  How long quince waits before reminding you about the <em>same</em> device again. A
                  phone that reconnects repeatedly is not a reason to be told repeatedly.
                </p>
              </>
            )}
          </Field>
        </div>
      </div>

      {staleElsewhere ? (
        <ConfigStaleNotice
          onTakeServerVersion={() => {
            takeServerVersion();
            setErrors([]);
          }}
        />
      ) : null}

      <div className="flex items-center gap-3">
        <Button type="submit" disabled={mutation.isPending}>
          Save
        </Button>
        {/* "Saved", matching `ConfigEditor` and for its reason: a save reports what it knows, which
            is that the document was written. Every key here IS live — the notifier reads the config
            through a closure on each evaluation, so an edit applies from the next one with no
            restart — but upgrading the word is a separate claim from making the fields exist. */}
        {saved ? <span className="text-xs text-ok">Saved</span> : null}
        {errFor("") ? <span className="text-xs text-danger">{errFor("")}</span> : null}
      </div>
    </form>
  );
}

// CategoryCoverageNotice — `category_off`, the fifth status cause, made reachable (quince#1212).
//
// THE SPEC'S STATUS TABLE LISTED IT AND NOTHING COULD REPORT IT. Six rows for five causes, and the
// string `category_off` appeared in this codebase exactly once, in a comment: it was a four-cause
// surface wearing a five-cause spec. The reason was structural rather than an omission — the cause
// is a fact about `notifications:`, and until the settings reached a screen there was no client
// holding that section to notice.
//
// IT LIVES ABOVE THE SWITCHES, NOT IN THE STATUS SURFACE ABOVE, and that is a deliberate reading of
// the spec's own remedy column, which says *"turn it on here"*. The other five causes are facts
// about this browser and this subscription; this one is a fact about the configuration, true for
// every device at once. Rendering it beside the controls that fix it makes the remedy the next thing
// on screen, and means it appears on a browser that cannot receive a push at all — which is correct,
// because the misconfiguration is not that browser's.
//
// IT READS THE DRAFT, NOT THE SAVED DOCUMENT, so unticking the last category says what that will
// mean before Save is pressed rather than after. The notice describes what quince WILL do with what
// is on screen; that is the only version of it the user can act on.
//
// TWO STATES, NOT ONE, because they are different silences with different remedies:
//
//   - EVERYTHING OFF — a live subscription that can never receive anything. This is the honest
//     `category_off`: quince is set up, the phone is subscribed, and nothing will ever arrive.
//   - BOTH REMINDERS OFF — quince still reports failures, so notifications visibly "work", and the
//     one thing the rung exists for silently never happens. That is worse than the first state
//     rather than milder: the first is obvious the moment you look, and this one is invisible
//     precisely because the other notifications keep arriving.
function CategoryCoverageNotice({ notifications }: { notifications: Config["notifications"] }) {
  const remindersOff = !notifications.backup_available && !notifications.backup_overdue;
  const everythingOff =
    remindersOff &&
    !notifications.action_required &&
    !notifications.backup_failed &&
    !notifications.backup_completed;

  if (!everythingOff && !remindersOff) return null;

  return (
    // `role="status"` and the warn treatment, matching `ConfigStaleNotice` — the house idiom for a
    // form telling you what your current state means, as against `role="alert"` and `border-danger`,
    // which this project reserves for a configuration that is not in force.
    <div role="status" className="mt-3 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn">
      <div className="font-medium">
        {everythingOff
          ? "quince will not notify you about anything"
          : "quince will never remind you a backup is due"}
      </div>
      <p className="mt-1 text-xs">
        {everythingOff
          ? "Every kind below is switched off, so a device that has subscribed will never receive anything. Turn on at least one."
          : "Both reminders below are off. quince will still tell you when a backup fails or needs you — but it will never tell you one is due, which is what makes Wi-Fi backups happen on their own."}
      </p>
    </div>
  );
}

// DeviceCoverageNotice — `device_off`, the SIXTH status cause (quince#1270).
//
// IT MUST BE DISTINCT FROM `category_off` AND MUST NOT BE FOLDED INTO IT. Both mean "nothing will
// arrive"; their remedies are on different screens — the switches directly below for one, one
// device's own page for the other. A message that cannot tell them apart sends the user to the
// wrong screen, which is the quince#940 defect exactly: one true, useless sentence over
// distinguishable states with different fixes. Both notices can be true at once and both render.
//
// IT NAMES THE DEVICES AND LINKS TO THEM, because "troubleshooting is ACTIONABLE" is the rule and
// the remedy here is somewhere else. `category_off` can say *turn one on below*; this one cannot,
// so it carries the way there instead.
//
// IT READS THE DEVICES STORE, NOT THE DRAFT, and the asymmetry with `CategoryCoverageNotice` is
// deliberate rather than an oversight. That notice reads the draft because this form is where those
// values are EDITED, so it must warn before Save. These values are not edited here at all — they are
// saved the moment the box on the device page moves — so the store IS the saved state, and it stays
// live through `device.updated`.
//
// A NAME, NEVER A UDID: Operator-private, and meaningless to a reader besides. Same fallback chain
// as `DeviceNotificationsControl` and `notify.deviceName`.
function DeviceCoverageNotice() {
  const muted = useDevicesStore(
    useShallow((s) => s.order.map((u) => s.byUdid[u]).filter((d) => d && !d.notifications_enabled)),
  );
  if (muted.length === 0) return null;

  return (
    <div role="status" className="mt-3 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn">
      <div className="font-medium">
        {muted.length === 1
          ? "quince will not notify you about one of your devices"
          : `quince will not notify you about ${muted.length} of your devices`}
      </div>
      <p className="mt-1 text-xs">
        Notifications are switched off for {muted.length === 1 ? "it" : "them"} on
        {muted.length === 1 ? " its" : " their"} own page — no reminders and no failures, whatever
        the categories below say. Turn {muted.length === 1 ? "it" : "them"} back on there:
      </p>
      <ul className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs">
        {muted.map((d) => (
          <li key={d.udid}>
            <Link className="underline underline-offset-2" to={`/devices/${d.udid}`}>
              {d.name || d.model || "an unnamed device"}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
