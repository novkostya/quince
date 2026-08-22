---
name: architect
description: Become the architect session for quince — assert the identity boundary, load the state, sweep both repos for work awaiting review, and arm the review loop. Use at the start of an architect session (on the architect host, or anywhere the architect credential lives).
argument-hint: "[optional: pr number to start with]"
disable-model-invocation: true
---

# /architect $ARGUMENTS

Bootstrap an architect session. The architect reviews, rules, and lands; it does not implement.
This skill exists so that bootstrapping is a command rather than a paragraph somebody remembers —
the protocol below is distilled from failures that were expensive when they were not written down.

Ends with a report and an armed loop. It writes no code.

## 0. Declare the runner, THEN read the watch state — in that order

**Declaring RELOCATES the state directory, so a `status` read before it answers about the wrong
path** (quince#241). Until `runner set` runs, state resolves to the undeclared top-level path rather
than `…/forge-watch/<name>`. For a repeat name — a session resuming a name that has state — that
reports **`absent`** where the truth is **`dead`**, which is the exact substitution the rest of this
section spends twenty lines warning against: `dead` carries an accrued observation to re-arm from,
`absent` says nothing was ever armed. Measured on the architect box 2026-07-29: §0 reported *"absent
(exit 4) … Cold start; nothing inherited"* at 15:03:58Z, and at 15:08:24Z the session discovered that
declaring the name had moved the state directory out from under that answer. It was a genuine cold
start that day, so nothing was lost — **the defect is that the report could not have told the two
apart.** This skill's own §1 already said *"declare first, before anything reads or writes state"*,
and then §0 ran first and read state.

```sh
bin/forge-watch runner set <name>     # ONCE, FIRST. `arch<N>` — a pattern (r/arch/analyst), not a list.
```

**The name must LOOK like a seat — `arch<N>` here, `r<N>` for an implementer, `analyst<N>` for the
analyst — and `runner set` REFUSES one that does not** (quince#265, re-founded on a pattern by
quince#330; third seat quince#375). This seat is the
reason the check exists: `arch1` was declared only on the arch box, so the implementer box could
not attribute `arch1/…` branches and woke on every one — and, worse in the other direction,
`other_runner_names` on the arch box returned **empty**, which made the wake filter a documented
no-op there that had never suppressed anything. **Both are fixed by shape**: attribution no longer
needs a population, so it works on a one-seat box, and adding an ordinal is no longer a PR.

**Adding a KIND still is, and quince#375 is the first one.** `analyst<N>` cost a one-line change to
the alternation, which is the intended price of a third seat: a seventh implementer is arithmetic, a
third kind is a decision. **The ordinal is required there too** — bare `analyst` is refused, because a
bare word is the *"a prefix that is NOT a seat being read as one"* risk the pattern carries and the
deleted list did not.

**A taken name is refused**, so two sessions on one box cannot silently share a state directory.

**Now, and only now, read the watch state.** It lives on disk precisely so it survives this session's
process restarting; the loop is rebuilt from it, never assumed. Ask, and **say which of the six
answers you got**:

```sh
bin/forge-watch status --all      # declared set; exits 0 live / 9 starting / 3 dead / 4 absent / 5 wedged / 10 orphaned
```

`bin/forge-watch watch` (§6) runs this same check before arming and **refuses** on `live`, on
`starting` and on `wedged`, so none of the three answers that must not lead to a second watcher
depends on this section being read. Ask anyway: the tool can refuse to arm, but it cannot report on
your behalf that a watch was found dead.

**`orphaned` (exit 10) is the one to act on rather than reason about** (quince#111): the watcher is
running and the session that armed it is gone, so it can wake nobody. `stop` it, then re-arm from
that state — do not reseed. **If `stop` refuses (exit 1), do NOT arm** (quince#221): on this path the
watcher is still running *by definition*, so a second one beside it is quince#50 — and an unwatched
turn you have **reported** beats two watchers you have not. Say which happened. It appears after a
session is killed mid-watch, because the watcher is a child of the session and a single-pid kill
reparents it rather than ending it. An owner that cannot be *verified* gone never yields this class.

**`starting` (exit 9) is the one this seat will meet most** (quince#95). It means armed, first tick
not finished — nothing owed, nothing wrong, do not arm a second one. The window is one `gh pr list`
plus one `gh issue view` per declared issue: ~4 s with nothing declared, **17–18 s against a 20-issue
set**, and this seat's declared set is the larger one and grows. Before the class existed, `status`
reported `dead` here and the `Stop` hook's remedy for `dead` — *arm another one* — was quince#50's
race handed over as an instruction. One false block in two, measured on this box. It is bounded at one
interval and degrades to `dead reason=never_ticked` past it.

- **`watch=live`** → nothing to do. Do not arm a second watcher; two writing one state file is a race
  that presents as missing events.
- **`watch=dead`** → **re-arm from that state, and do NOT reseed it.** The next tick diffs against the
  stored observation and emits everything that accrued while nothing was watching. Report that a watch
  was found dead, and its reason.
- **`watch=wedged`** → a watcher process is still running and has stopped ticking. Run
  **`bin/forge-watch stop --repo <r>`**, then re-arm. Re-arming beside it puts two watchers on one state
  file, and nothing in the tool prevents that (quince#50). **Do not `kill` the pid yourself:** it is
  only known to be *our* watcher while its heartbeat is fresh, and `wedged` is defined by that
  heartbeat being stale — so on a recycled pid a bare kill signals a bystander. `stop` verifies the
  process start time before signalling and refuses when it cannot prove the identity. **Check that it
  exited 0 before you arm** — exit 1 is a refusal and means a watcher may still be live (quince#221).
  Its success line names which of three things happened; `exited on SIGTERM` is ordinary, **`REQUIRED
  SIGKILL` is a finding**, worth reporting rather than skimming.
- **`watch=absent`** → cold start; seed and tick.

Collapsing `dead` into `absent` is how a restarted watch silently becomes a fresh one that has "seen
nothing changed" since a beginning it invented; collapsing `wedged` into `dead` is how you are told to
start a second watcher beside a live one. Full reasoning: [`../../loop-protocol.md`](../../loop-protocol.md).

## 1. Assert the identity boundary

**The runner name is already declared — §0 does it, first, before any state is read.** `§4`'s
checkout root and `§6`'s watch both derive from it, and this skill never told you to set one until
quince#208 — the same gap `/kickoff` had. Undeclared, `forge-watch` cannot tell which watch is its
own and refuses to arm.

**The order is not cosmetic, and it is why the declaration sits in §0 rather than here.** Declaring
*after* seeding relocates the state directory — from the undeclared top-level path to
`…/forge-watch/<name>` — which **orphans any observation already seeded** there, leaving the watch to
diff against nothing. Measured on the architect box on 2026-07-29, by a session that seeded, armed,
was refused, declared, and found its seed stranded. This section carried the instruction while §0 ran
first and read state anyway, which is quince#241.

`approver ≠ author` is the authority model. It is structural only if the machine you are on holds
one credential and not the other:

```sh
QUINCE_RUNNER_ROLE=arch sh deploy/runner/preflight   # must end "environment is fit to start"
bin/gh-review api /installation/repositories -q '.total_count'   # must answer with a count
```

**Assert through `preflight`, not by listing the credential directory.** `.claude/settings.json`
denies `Read(~/.config/quince/**)` — deliberately — so a session that reaches for `ls` or a file
test there gets denied by the permission classifier and learns nothing. An architect session hit
exactly that on 2026-07-29 and fell back to `preflight`, which is the better mechanism anyway:

- it **asks the credential to mint** rather than checking that a file exists, and a key can be
  present and unusable in ways a file test cannot see (quince#121);
- it is the **same check the service runs at start**, so a hand-run answer and a boot-time answer
  cannot disagree;
- it needs no read access to the credential itself.

**It checks both implementer credentials, because the identity moved.** The old form named the bot
token alone — so once authoring became an App key (`decisions/0014`), *"the implementer identity is
absent"* was being asserted by looking for a credential that no longer carries it, and a box holding
`quince-coder.pem` would have passed. `preflight` covers both since quince#203; the `gh-*` wrappers
do not yet (quince#204).

**Verdicts are cast with `bin/gh-review`, which is a GitHub App and not a person.** That is
quince#47's fix and the reason it is structural rather than a convention: a review from
`quince-review[bot]` cannot be read as the Operator's, because it is not a login at all. Under the
previous wrapper the architect and the Operator shared the `novkostya` account, which first muddied
the record and then, on quince#115, **blocked** it — GitHub refuses a review on a PR the same
account authored, so the one class of PR that must come from the Operator was the one class the
architect could not review.

**`bin/gh-review api user` does not work, and that is not a broken credential.** An installation
token has no user context, so GitHub answers `403 Resource not accessible by integration`. The
question was never "who am I logged in as" but "can this box cast a verdict", and for an App the
thing that answers it is whether an installation token mints and reaches repositories. Asserting
`api user` here would be the third time this section has checked the wrong thing.

**Do not use `gh auth status` either.** A reviewer host is expected to show *unauthenticated*: its
credential is a key read at point of use, never a `gh auth login` session, deliberately, so it
cannot leak into an ambient session. (This section originally asserted `gh auth status`, written
before the wrappers existed, and hard-stopped the first architect session on a correctly configured
host. A protocol that checks the wrong thing fails closed — the right direction to fail, and still
a failure.)

**`bin/gh-review` IS THIS SEAT'S ONLY CREDENTIAL — reads, verdicts, canon authorship and merges all
go through it.** Operator ruling 2026-08-07 (quince#676): `bin/gh-arch` is retired, and
where the App cannot do something, `gh-review` is what gets fixed.

**There is no read/write split to observe any more, and that is the change to notice.** This section
used to route *reads* through `gh-arch` and *writes* through `gh-review`, because two architect
credentials existed and only one of them could cast a verdict without re-creating quince#47. One
credential means one wrapper: **nothing you do on this seat needs you to pick.** If you find a skill
still naming `gh-arch`, it is stale — say so rather than working around it.

**The two reasons canon gave for keeping it were tested, and neither survived.** Recorded because
they read as blockers and a session meeting them would otherwise stop:

1. *the private layer's git credential helper reads the architect PAT* — **was true and is fixed**
   (quince#674). `provision` searches `gh-coder`, `gh-review`, then the PAT, so the layer
   authenticates with the App.
2. *`forge-watch` needs a token cache before it can read through the App* — **contradicted by
   measurement**: 451 ticks over a full session on `gh-review`, zero credential failures, roughly
   11% of an installation token's hourly budget at a parked-only declared set. A cache is an
   optimisation, not a prerequisite. (Bounded to that set size: canon records a tick at ~40 s
   against 45 issues, where the call count rises with the set.)

**AUTHOR canon through `bin/gh-review` — commit and `pr create` as the App, never as `@novkostya`.**
Operator ruling on quince#137, 2026-07-27. The App holds `contents: write` and `pull_requests:
write`, so an architect-authored canon PR can be authored by `quince-review[bot]` rather than by
`@novkostya` — and that is what lets `@novkostya` approve it as **code owner** under
`.github/CODEOWNERS`, since GitHub does not count an author's approval of their own PR. Authoring
canon as `@novkostya` is what would make the code-owner requirement unsatisfiable, so this clause is
load-bearing for the file rather than a stylistic preference.

**This is not a missing capability you have to work around; it is a habit.** quince-devlog#51 was
authored as `@novkostya` and merged with `reviews: []`, while quince-devlog#53 — filed the same
hour — was authored by `app/quince-review`. Nothing in canon said which wrapper authors, so the one
that was reached for first won.

**Narrow, and record it as the exception it is.** Routing authorship through the App collapses
author and approver into one principal wherever the App is *also* the approver, which is exactly
what `approver ≠ author` forbids (quince#136). It does not collapse **here**, and only here: the
approver for canon is the **Operator**, not the App, so the separation stays where `CLAUDE.md`
already puts it — architect authors, Operator approves. It holds for this class *because* the
Operator's approval is already mandatory on it. **It does not license the App to author any class it
also approves**, which is every other class in this repository.

- **Bot token present on an architect host** → say so and stop. A box that can author *and*
  approve dissolves the property the whole identity ruling protects (devlog#7). Reviewing from a
  host that holds both is a finding about the host, not a detail to work around.
- **`bin/gh-review` cannot answer** (no key, no app id, no `openssl`, or it refuses) → stop, and
  quote its own message: it names the file and the likely cause. An architect session that cannot
  submit a real review verdict is a session pretending to be one.
- **It answers `0` repositories** → stop. The App exists and is installed nowhere, so every verdict
  it casts would 404. Zero is a successful call reporting an unusable identity, which is the
  shape this project keeps filing — read the number, not the exit code.
- **It refuses on multiple installations** → stop and set `QUINCE_REVIEW_INSTALLATION_ID`. The
  wrapper will not guess which identity it is acting as, and neither should you.
- Verdicts and merges in later steps run through **`bin/gh-review`**, not bare `gh`, for the same
  reason the implementer side uses `bin/gh-coder` (an allow rule never matches past a leading
  `VAR=value`, so `GH_TOKEN=$(cat …) gh …` is unallowlistable by construction).

The mirror image on the implementer side is `deploy/runner/preflight`; this is the same assertion
pointed the other way.

## 2. Load the state

Run `/onboard` if you do not already hold the project's state this session: the devlog's one-line
state, the frontier, open questions, and the canon the current work touches. Then read what has
happened since you were last here — the newest journal entries and the most recent merged PRs, not
just the open ones. A reviewer who does not know what landed yesterday will re-litigate it.

## 3. Enumerate the work — from the declared set, never from memory

```sh
for r in $(sed 's/#.*//' .claude/forge-set | grep -v '^[[:space:]]*$'); do
  bin/gh-review pr list -R "$r" --json number,title,author,reviewDecision,updatedAt,mergeStateStatus
done
```

**The set is `.claude/forge-set`, and it is not optional.** Do not hardcode the repositories: a
hardcoded pair goes stale the moment a third one matters, and a watch that covers one repo while
reporting "nothing to review" is making a claim it never checked — that is how a devlog PR sat
unreviewed for hours while the
queue was reported clear. `bin/forge-watch tick --all` reads the same file and **hard-fails** if it is
missing or empty rather than falling back to one repo. A canon or journal PR is review work exactly as
code is.

Also list open issues in every declared repo when starting cold: an issue with a ruling attached is work
the architect owes, not backlog.

**A NEWLY FILED ISSUE NOW WAKES THE WATCH, so do NOT bulk-declare open issues** (quince#273). Until it
did, an issue that was neither declared nor referenced by an open PR entered no watch at all — and
since the gap protocol makes *filing an issue* how a blocked session requests a ruling, the ruling
channel was the one channel with no wake. quince#265 landed on this seat's own quince#230 ruling and
reached it only because the Operator asked by hand.

**`--issue` is for what you are PARKED on, not for everything open.** quince#273's original Part 1 said
to re-declare the set from open issues at cycle start; that advice is superseded by its own Part 2,
because `issue-new` now covers arrival and declaring the backlog buys nothing while costing a great
deal. Measured on this seat (quince#282): a 45-issue declared set makes one foreground tick **40 s**
against an interval of 60–90 s, versus 17–18 s for 20 — a tick is one `gh issue view` per declared
issue. Parked-only is five, not forty-five.

**The ceiling, stated so it is not mistaken for a guarantee.** `issue-new` fires for an issue numbered
above the highest this watch has seen, and it establishes that mark **silently** on a cold start. So the
first tick after arming learns rather than reports, and an issue filed *before* you armed is not new to
the forge — it is backlog, and the cold-start listing above is still what finds it.

**Reading `updatedAt` is not reading whose turn it is.** Fetch the actor too — a session that read a bare
timestamp, assumed the latest activity was its own, and reported "nothing owed from me" was wrong about
three items. And `reviewDecision` still says `CHANGES_REQUESTED` after the author has fixed and pushed,
because no new review has landed: it records the last verdict, it does not say whose move it is.

**Never ask the bot to re-request a review — it cannot, on any repo (devlog#48).** `--add-reviewer`
resolves the login through an org-scoped GraphQL field, and the bot token is `repo`-scoped by ruling.
So *"re-request review when the points are in"* asks for an event the author is unable to emit: the
call fails on their side, this side waits, and both parties are waiting correctly. **Ask for a comment
and treat the comment as the signal** — it is a property of the token, not of the PR or the repository.
`CLAUDE.md`'s identity table lists the other refusals of this kind; read it before designing around one.

## 4. Review — the protocol, including what is easy to get wrong

**Check out into `$HOME/scratch/$(bin/forge-watch runner get)/`, never `/tmp`.** This seat clones
more than the implementer does — once per PR reviewed, twice when a head moves — and until this
line existed it cloned wherever the session chose. Measured on the arch box: **58 review clones in
one day, 161.9 MB**, all in `/tmp` and all outside every root `bin/scratch-reap` knows, so none of
them could ever be reaped (quince#45). `/review-pr` §2 carries the commands.


Per PR, follow `/review-pr`. Four things belong here because each was learned the hard way:

- **Run the head under review, never `main`.** Check the branch out first. Testing a guard using
  the version that lacks the guard destroyed a container that a merged version would have
  protected — the tool you run must be the tool you are reading.
- **Diff head-at-approval against head-now before merging.** A push can land *as* an approval
  registers, so GitHub can attach your approval to a commit you never read (stale-review dismissal
  covers pushes *after* an approval, not pushes racing one):
  ```sh
  # NOTE THE HEAD BEFORE YOU READ THE DIFF and use that value here — the same oid you passed to
  # `--commit-id` when casting the verdict (`/review-pr` §6). `reviews[].commit.oid` is NOT a
  # reliable source for it — see "the recorded binding MOVES" below.
  #
  # BEFORE YOU READ, not before you submit: taking it at submission time pins to a head that may
  # already have moved while you were reading, which is the race this whole section exists for.
  OLD=<the full 40-char oid you noted before reading the diff>
  NEW=$(bin/gh-review pr view <n> --repo novkostya/quince --json headRefOid -q .headRefOid)
  git fetch origin "$OLD" "$NEW"                 # both, by full oid
  git range-diff "origin/main...$OLD" "origin/main...$NEW"    # THREE-DOT, whole branch
  ```
  Identical → the approval stands. Different → re-review before it lands.

  **THREE-DOT AND WHOLE-BRANCH, because the old `"$OLD~1..$OLD" "$NEW~1..$NEW"` form was TIP-ONLY**
  (quince#110). `OLD~1..OLD` is exactly one commit, so a three-commit branch got one third of a
  check — and the output is indistinguishable from a full one: a single `1: … = 1: …` line, which
  reads as *"the branch is unchanged"* rather than *"the last commit is unchanged."* Demonstrated
  with a middle commit deliberately altered and the tip left alone:

  ```
  tip-only    1: 9e86c2f = 1: 5676f6e commit 3          <- clean, and WRONG
  three-dot   1: 8a95539 = 1: 8a95539 commit 1
              2: c185493 < -: ------- commit 2          <- the tampered commit, caught
              -: ------- > 2: 6d3828b commit 2
              3: 9e86c2f = 3: 5676f6e commit 3
  ```

  Three merges on 2026-07-31 — quince#377, #383 and #386, three commits each — were verified
  tip-only and reported as verified. Re-run in full afterwards they were pure replays throughout,
  so nothing was lost: that is luck plus a well-behaved `update-branch --rebase`, not evidence the
  check worked.

  The three-dot form asks *"how do these two branches differ from their bases"*, which is the actual
  question, and it handles the two bases being different commits — which they always are across a
  rebase. On a single-commit branch it degrades to exactly the old behaviour, so nothing is lost.
  Commits dropping out of the old range are **diagnostic, not noise**: that is what a rebase onto a
  newer `main` looks like.

  **THE RECORDED BINDING MOVES, so `reviews[].commit.oid` cannot be trusted as `OLD`** (quince#110).
  `gh pr update-branch --rebase` rewrites it — same review id, same `submitted_at`, different
  `commit_id` — and §5 makes that rebase the merging seat's standing duty on every `BEHIND` branch,
  which `strict: true` makes the steady state. Then `OLD == NEW`, `range-diff` compares a commit
  against itself, and the seat gets a clean `=` from a check that had nothing to compare. Still
  observable on merged PRs:

  ```
  quince#377   approved_at_oid 533c2711…   headRefOid 533c2711…   <- identical
  quince#383   approved_at_oid be61e18d…   headRefOid be61e18d…   <- identical
  ```

  It is **not** vacuous in every case: if the *author* force-pushes, `commit_id` stays on the old
  commit and the comparison works — that is the case the recipe was built from. What it cannot see
  is the merging seat's own rebase. **Until the wrapper passes `commit_id` explicitly (quince#110's
  ruled fix), noting the oid by hand before approving is the only reliable `OLD`.**

  **Use the FULL 40-character oid and fetch it first, and that is the whole of quince#243.** After a
  force-push the approved head is no longer any branch's tip, but the object is still on the forge
  and **`git fetch origin <full-oid>` still gets it** — GitHub serves an arbitrary full object id.
  What it will *not* serve is an abbreviation: `git fetch origin 410301f4` answers `fatal: couldn't
  find remote ref`, which reads as *"that commit is gone"* when it means *"say it in full"*.
  Measured on two force-pushed-away heads, 2026-07-29 — short form fails and full form succeeds on
  both, and `range-diff` then works normally.

  `reviews[].commit.oid` already hands you the full oid, so the failure only appears when a session
  abbreviates it by hand while pasting. Do not.

  **The fallback, when a head really is unreachable** — a deleted fork, or an object the forge has
  since collected — is to compare patches through the API, which needs no local object at all:
  ```sh
  bin/gh-review api repos/novkostya/quince/compare/"$OLD"..."$NEW"      # or …/commits/<oid> per side
  ```
  Prefer `range-diff`: it is rebase-aware and tells you *which* commit changed, where a patch
  comparison only tells you *that* something did.
- **Classify every red check; never wave one through.** Infrastructure (a job dying in setup, a
  registry timeout), a known flake with an issue, or a real failure. Say which. An unclassified red
  is an unread claim.
- **Refuse to approve your own authorship — and read "authorship" as substance, not git blame.** If the
  PR is yours, say who must approve instead. A PR the bot typed from *your* proposals is
  architect-authored in the sense that matters, and canon is the one place where the literal reading is
  not good enough: route it to the Operator.

  **Substance cuts both ways, and the second direction is the one that misfired.** A
  `novkostya`-authored PR is not yours by default. That login covers the Operator and the Mac acting
  as the break-glass seat — and, until quince#676, you as well, through `gh-arch`. **Retiring that
  wrapper removes one of the three claimants rather than the ambiguity**: two seats still share the
  field, and you are no longer one of them, so a `novkostya`-authored PR is now *never* yours. On [quince#158](https://github.com/novkostya/quince/pull/158), a
  Mac-authored repair of the gh wrappers, this bullet and `/review-pr` §0 together charged a full
  authorship investigation on a PR no seat of yours had written — reboot timing, `/etc/init.d`, the
  staged wrappers, quince#134 and #136 on attribution, and finally the PR's own prose. **You reached
  the right answer and approved.** The point is that the rule made you buy it, from evidence the
  forge does not carry. Ask whether **you** produced the change, not whether the account is one you
  can also post from — verdicts have had their own principal since quince#134, so the login on the
  author field is not evidence about who wrote it.
- **Cite a ruling by comment URL and self-declared role, never by login.** Unchanged, and the reason
  has grown rather than gone away: you cast as `quince-review[bot]` now, but `novkostya` still covers
  three seats (quince#47), so an unlinked "the Operator ruled X" is not a citation; it is a claim
  about a record the reader must go and fail to verify.

Verdicts are real GitHub reviews cast through **`bin/gh-review pr review … --commit-id "$OID"`**, and
the body states **what you ran**, not only what you think.

**`--commit-id` is required and the wrapper refuses without it** (quince#110). `gh pr review` has no
such flag and the REST endpoint defaults the field to head, so a verdict cast the old way binds to
whatever head exists *at the instant of submission* rather than to what you read — invisibly, because
the diff view does not change under you. `$OID` is the head you noted **before** reading, which is the
same value §4 calls `OLD`: one oid, noted once, used for the verdict and for the staleness check.
`/review-pr` §6 carries the full form.

## 5. Land what is ready

`/land <n>` — preconditions checked from the API rather than from memory of this session, privacy
re-swept over the whole branch, rebase-merge, then tidy up. A branch that is behind gets rebased,
re-run and re-approved, not merged around.

**Merge through `bin/gh-review`. The ladder on a refusal is AUTO-MERGE, then the OPERATOR** —
Operator ruling 2026-08-07 (quince#676), replacing devlog#52's `gh-arch` fallback, which named a
credential that no longer exists.

1. **`bin/gh-review pr merge --auto --rebase`**, issued at approval time. GitHub executes the merge
   when required checks pass.
2. **The Operator merges.** Honest, guaranteed, and it costs the timestamp devlog#52 was avoiding.
   Reached when auto-merge cannot be enabled or is not appropriate.

The primary path works — every merge since the App's first, quince#135 at `2026-07-27T21:53:23Z`
(then #138, #142, devlog#54, #57), reads `mergedBy: app/quince-review`; everything merged earlier
that day was `novkostya`'s. A ladder exists at all because the harness classifier refuses the merge
verb **intermittently**, leaving no trace on the forge, and without it written down the next session
to meet a refusal concludes the App cannot merge and escalates — §1's own warning arriving from a new
direction.

**Auto-merge fits devlog#52's reasoning rather than overturning it.** That ruling chose a second
architect credential over an Operator merge because **a merge carries no verdict**: the judgement is
the approval, structurally the App's, and the merge only executes it, so the attribution costs a
timestamp rather than an authority. Auto-merge executes it **as the App**, so the attribution
devlog#52 was protecting is *preserved* by the primary path and spent only on the backstop.

It also fixes something this seat already pays for: **a PR approved while CI runs has nothing happen
to it when the checks finish.** Check completion does not move `updatedAt` — which is why
`event=mergeability status=CLEAN` had to be invented (quince#65), and why quince#63 sat landable for
sixteen minutes.

**TWO THINGS TO GET RIGHT BEFORE USING IT, both measured rather than assumed.**

- **`allow_auto_merge` is `true` on BOTH repositories**, so every PR here can be armed and nothing
  falls back to the Operator for want of the setting. Measured 2026-08-20 on
  `novkostya/quince-devlog`, which is the one worth checking: the arm read back
  `ARMED by app/quince-review method=REBASE`.
- **The App can enable it and it fires unattended** — quince#692 on 2026-08-07, and seven arms on
  2026-08-20 of which six merged on green, each reading `mergedBy: app/quince-review`. **Read the
  arm back through the API rather than inferring it from an exit code**: `autoMergeRequest` appears
  in none of `state`, `reviewDecision` or `mergeStateStatus`, so a seat checking those three has not
  checked the arm.

**AN ARM DOES NOT SURVIVE THE BRANCH GOING `BEHIND`.** Auto-merge does not rebase, so under
`strict: true` every *other* merge silently strands an armed PR — approved, green, armed, and unable
to fire. Rebasing before arming covers arm time and nothing else: a PR waiting on something slow is
re-stranded by every merge that lands meanwhile, measured four times on one PR in 25 minutes
(quince#1325). **Re-check armed PRs after every merge.** A rebase preserves the arm —
`autoMergeRequest.enabledAt` is unchanged across it — where a close-and-reopen drops it (quince#905).

**AND THE STACKED-PR CHECK MOVES WITH IT.** §6/`CLAUDE.md` §6 requires the merging seat to look for
PRs stacked on this one *immediately before* merging, because deleting the head branch closes a
dependent irrecoverably (quince#388, quince#400). Under auto-merge the merge happens **later and
unattended**, so **run that check at ENABLE time** — and know that it stops being a check over an
instant: a PR stacked after auto-merge is enabled and before it fires is covered by no guard.
`novkostya/quince` has `delete_branch_on_merge = true` repo-wide, so the deletion happens on every
merge regardless of flags (quince-devlog#214). §1's do-not-stack rule is what keeps this exposure
narrow; it does not make it zero.

**Nobody can re-run a red check — not you, not the App, and not the implementer** (quince#141).
`run rerun` is refused for every agent seat, worded for an integration and exiting **`1`**, not the
`0` this line claimed for a year. The implementer's `CAN` died with `decisions/0014`: re-running was
a property of `quince-bot`'s classic PAT, not of the seat, and the App it became has no
`actions: write`. **Do not ask the author to re-run it** — that is the "never ask an identity for an
action it cannot perform" failure, and this file was the thing telling you to.

**Use `bin/gh-review pr update-branch --rebase` instead.** It re-triggers the workflow on the new
head *and* clears `BEHIND`, and it beats a re-run because it also revalidates against current
`main`. Measured on quince#216. Yours to run as the merging seat — but responsibility, not
exclusivity: an author may rebase its own PR too, and `CLAUDE.md` §5 says so (from an implementer
box that is `bin/gh-coder pr update-branch --rebase`, per `/kickoff` §1).

**It is a WRITE** — it moves the branch — and that used to decide *which wrapper*. With one
credential it decides nothing about tooling, and it still decides **whose turn it is**: §5 calls a
`BEHIND` rebase *"tree-preserving, no decision in it"*, which is true of its effect on the **tree**
and is not a licence to move a branch somebody else is still holding.

**On a branch that is already current the rebase is a no-op.** Then it is `CLAUDE.md` §5's rung 3 —
close and reopen, which re-triggers CI with no commit and no history. And rung 1 comes first,
always: **classify the red before retrying**, or the mechanism meant for flakes papers over a real
failure.

**It moves the head, so re-read before letting your approval stand.** A rebase does **not** reliably
dismiss the approval — on quince#216 it did not — so §4's stale-review rule applies with full force:
`range-diff` **against the full 40-character oid, fetched first** (§4 says why the abbreviation is
what fails), or compare the old and new patches directly, and confirm the rebase was a pure replay
before merging.

**AND THIS REBASE IS THE ONE §4's CHECK CANNOT SEE UNAIDED** (quince#110). Doing it rewrites the
review's recorded `commit_id`, so `reviews[].commit.oid` comes back equal to the new head and the
comparison is against itself. **Note the head oid BEFORE you approve, in full** — §4's `OLD`, and §4
says why an abbreviation cannot be fetched once the head has moved — or the check
you run after rebasing is vacuous exactly when you most need it. Never ask for a *push* to clear a red check: that moves the head off the tree you
reviewed for no gain the rebase does not already give you.

## 6. Arm the loop — the MECHANISM, not only the properties

This section used to specify the loop's properties and leave the mechanism to whoever read it. A
session duly reached for `/loop` plus a 1200 s `ScheduleWakeup`: up to twenty minutes of latency per
event, busy on every tick, and — when a client reconnect restarted the session process — a pending
wakeup that died silently while the watch reported healthy for 44 minutes. So the mechanism is named.

**Pull the launchpad before arming — it is part of arming, not housekeeping.** `--all` reads
`.claude/forge-set` from *this checkout*, so a stale checkout gives you a watch set that is silently
smaller than the declared one. The hard-fail cannot catch that: a stale set is not missing, not empty
and not malformed, it just describes yesterday. Observed on a real arch box, where the launchpad sat at
a commit predating the file entirely (#33).

**`bin/forge-watch watch`, run as a BACKGROUND task, does the waking** — and the loop belongs to the
tool, not to you. **Arm it LAST, as the final action of the turn, after a foreground catch-up tick:**

```sh
git -C "$PWD" pull --ff-only          # the watch set is this checkout's copy
# 1. do all the work first: every review, every merge, every comment
# 2. consume the catch-up SYNCHRONOUSLY, where you can read it   (FOREGROUND — one pass, returns)
bin/forge-watch tick --all --gh "$PWD/bin/gh-review"
# 3. arm, last, against a now-current observation                (BACKGROUND task)
bin/forge-watch watch --all --gh "$PWD/bin/gh-review" --interval 60
```

**THE ARM MUST BE A SINGLE, UNCOMPOUNDED INVOCATION — nothing before it, nothing after it, no `;`,
no `&&`, no `&`** (quince#282). "Run it in the background" is not enough on its own, and this seat
proved it twice in one session: both times the arm was written compounded, both times it silently did
not survive, and `status` said `dead` seconds later. The second attempt left an **`orphaned`** watcher
— running, owner gone, refusing the next clean arm — which then needed `stop` before anything could be
armed at all.

The failure is silent from the arming side, which is what makes it expensive: the command returns,
nothing complains, and the session believes it is watched. Backgrounding is a property of **how the
harness runs the call**, so a `&` inside it backgrounds a *child of the wrong process* and the
watcher's owner is gone the moment the compound statement finishes. Same rule in `/kickoff` §6.

**`eval` is NOT banned, and this list says so because the first version of it got that wrong.**
`eval "exec bin/forge-watch watch …"` is how a declared set is expanded — `$(cat …)` becomes N
`--issue` flags — and it appears in every arm that WORKED on the architect seat, as well as in both
that failed. It is the element that does not discriminate; the trailing `&` and the `;` are what did.
Banning it would make the correct form unreachable, and a rule that forbids what you were doing
correctly is a rule that gets ignored wholesale — which is how the `&` got there in the first place.

**This section named the mechanism and never said when in the turn to run it** (quince#100). The
natural reading — arm once you know you need one, right after handling the events that woke you — is
the broken one: a watch armed *before* your next approval or merge can still be dead by the time the
turn ends, and the `Stop` hook is telling the truth when it says so.

**Suppressed means NOT WOKEN ON, never NOT SEEN.** Every event is still printed on every tick; the
filters decide only whether the loop *ends*. **This seat is the better-covered of the two:**

- your own **approvals and merges** do not wake you — the per-runner ledger cancels them, because
  those lines are computed by diffing observations and carry no actor at all;
- **your own issue comments do not wake you either**, since quince#307 — which matters here more than
  anywhere, because a ruling **is** an issue comment, and it is this seat's primary output. The
  implementer half of that arm is deliberately still open (quince#227): one App, many runners, so
  `actor=quince-coder` names a seat rather than a session. You have one box and one signing key, so
  you have no such ambiguity;
- `actor=unattributed` still wakes you, and always will — unknown wakes is the rule the whole
  subsystem is built on.

So arming last matters less on this seat than it did, and **still arm last**: an approval whose event
lands between your arm and your stop is not the only thing that can end a turn, and the cost of the
ordering is nothing.

**Arming last is necessary and not sufficient, which is what step 2 is for.** A re-arm from `dead`
correctly emits what accrued, and what accrued is your own actions from the turn just finished — so the
first tick exits immediately and reaching a *quiet* watch takes two arms, only the second of which can
survive the end of the turn. The foreground tick eats that catch-up in the open rather than delivering
it as a task notification after the turn has ended. The measurement is the implementer's — three
`Stop`-hook firings before the tick step, none after — and it is quoted here with that seat named,
because a measurement carries the box it was taken on.

**Why step 2 is safe there — and it is a two-directional claim.** A hand-run `tick` leaves the
liveness verdict exactly as it found it: it never refreshes `.watch.last_watcher_tick`, so it cannot
make a **dead** watch look **alive** (quince#49), and `step()` carries the watcher record forward, so
it cannot make a **live** watch look **dead** (quince#103). **The second direction is the one that was
broken**, and the one that matters here: `watch` refuses to arm beside a live watcher by reading that
record, so a tick that erased it turned step 3 into a *second* watcher on one state file — quince#50's
race, reached through the guard rather than around it. Worth carrying from this seat in particular:
the one-directional version was **verified before being ruled**, and the verification was of the
direction that could not fail. Checking one direction of a two-directional property is not a check.

**BOTH DIRECTIONS ARE ABOUT THE LIVENESS VERDICT, AND NEITHER IS ABOUT CONCURRENT WRITES.** The
paragraph above is true and it is **narrower than it reads**: what a reader carries away is *"a
hand-run tick is safe"*, and quince#1460 is a hand-run tick that was not. It is safe **THERE** —
because of where step 2 sits in the sequence, with nothing live to race — rather than safe whenever.

**A tick beside a LIVE watcher is two writers on one state file.** Measured: the state ended as line 1
a complete object and line 2 the tail fragment of another write, after which `jq` fails, the `.new`
write never lands, and the next arm **RESEEDS** — reporting `first-observation` about a repo it had
been watching for hours. That is the accrued observation this section's own *"do NOT reseed"* exists
to protect, destroyed by accident rather than by ignoring the rule, and it announces itself as an
ordinary cold start.

**`tick` now REFUSES beside a live watcher** (exit 1), the way `watch` always has — so the ordering is
enforced rather than merely documented. **Read `status` first and tick only on `dead` or `absent`.**
The watcher's own ticks are exempt by `--watcher-pid`, which is why `watch`'s internal loop is
unaffected.

**The reviewer declares too, and its case is the one quince#80 was filed from.** The blocked list that
went unwatched was an *architect's* — quince#70/#71/#72/#75/#78/#80, most with no PR at all — and the
only reason a ruling on it was ever seen was a hand re-read the session had committed to when filing
the issue. That is a human-remembers mitigation at the head of the escalation channel:

```sh
bin/forge-watch watch --all --gh "$PWD/bin/gh-review" --interval 60 \
  --issue novkostya/quince#71 --issue novkostya/quince#80
```

Under `--all` the repo is required — issue numbers collide across repositories, and a bare number is
refused rather than guessed at. `--issue` replaces the set, `--no-issues` clears it, passing neither
keeps what is on disk, and `status --all` prints the declared set with its **age** so an inherited
declaration is visible rather than silently watched.

**Anything you have filed and are waiting on a ruling for belongs in that list**, and so does anything
you have parked. A ruling you cannot be woken by is a ruling that waits on you re-reading the issue.

**The loop must exit when it finds something; a loop that cannot exit cannot wake you.** A session is
woken by a background task *completing*, so the `while :; do tick; sleep 60; done` this section used to
print detects everything and delivers nothing. Not hypothetical: the first architect session under this
skill armed exactly that, slept through quince#61 for fifty minutes, and every instrument agreed it was
healthy throughout — fresh heartbeat, state rewritten every 60 s, `status --all` saying `live`
(quince#62). Run it in the **background**; in the foreground it blocks the session it is supposed to
wake.

**Every exit `watch` can return, and which are re-arms.** This list said *0, 6 and 7* and *"every exit
is a re-arm"* — false on the one it omitted. `watch` **refuses** to arm beside a `live` or `wedged`
watch, and refusing is exit **1**. Obeying the old rule there is refuse → re-arm → refuse → re-arm,
unbounded, with no watch running throughout; it was hit on this box and escaped by noticing, which is
not a mechanism (quince#75).

| exit | means | what to do |
| --- | --- | --- |
| **0** | events found, on stdout | handle them, then **re-arm** |
| **1** | **REFUSED** — already `live`, or `starting`, `wedged`, `orphaned`, or a bad argument | **read `status`, then act on the answer** (quince#88): `live` → **leave it running**, no second watch is wanted — do *not* run `forge-watch stop` · `starting` → **wait**, nothing is wrong and nothing is owed · `wedged` → `forge-watch stop`, then arm · **`orphaned` → `forge-watch stop`, then arm** (quince#111) — the watcher is running but its session is gone, so leaving it running ends your turn unwatched. **If `stop` refuses (exit 1), do NOT arm** (quince#221): a running watcher plus a second one is quince#50's race, and an unwatched turn you have *reported* beats two watchers you have not. Say which happened · `dead`/`absent` → **arm again.** Bounded at **two arm attempts per turn** — a third refusal is a report, not a loop. |
| **6** | `--max-wait` idle bound, `event=watch-idle` | nothing happened, which is a report and not a silence — **re-arm** |
| **7** | `--fail-after` failing ticks in a row | fix the cause the events name, then **re-arm** |

`status` answers a different question with its own codes: exit **0** live · **9** starting · **3** dead · **4** absent
· **5** wedged. An exit of **2** is not the tool's at all — it is an underlying tool (jq) failing and
the script aborting, so read the error rather than looking it up here.

**Re-arm on 0, 6 and 7. On 1, read what it refused and why** — then, if `status` says `dead` or
`absent`, arm again. A watch that exited is a watch that is not watching, and a refusal is true only
at the instant it is produced: five losses of the watch in one session came from *not* arming because
something was live, and none from arming when nothing should have been (quince#88).

**Arm unconditionally — never gate an arming behind a shell pre-check.** `watch`'s own refusal is the
only check here that is atomic with the act it guards; a conditional beside it is check-then-act across
a window in which the watcher can die, and both the sequenced form (`status …; exec watch …`, which
gates nothing because `;` does not condition) and the correctly-composed `if` form were measured
failing. **This retires the pre-arm conditional, not §0**: §0 still requires you to read `status` and
**report which of the six answers you got**, which the tool cannot do on your behalf.

**The window is narrowed, and the part of it that mattered is now CLOSED.** quince#102's arm-last
ordering shrank it from one side and the rule above from the other; quince#95's `starting` class shut
the consequence, which was the hook reading `dead` on a freshly-armed watch and instructing you to
arm a second one. What remains is genuinely small: a watcher that is `live` at the instant you check
can still die on your own next forge write, so a refusal you acted on can be stale. That is what the
`status`-then-arm-again rule above is for, and the `Stop` hook remains the **backstop** — a backstop,
not a resolution.

**Exits 6 and 7 will be reported to you as failures.** A background task that exits non-zero renders as
*"failed with exit code 6"* — and 6 is `watch`'s designed idle heartbeat, the floor this section names.
Read the last line of its output, which says which exit it was and why, before treating it as a
malfunction.

**`ScheduleWakeup` is only available in `/loop` dynamic mode, and most sessions are not started that
way.** A session that reaches for it outside that mode simply cannot arm it — so **say the second
channel is absent** rather than reporting a fallback that was never armed. The watcher's own
`--max-wait` is the floor this section already tells you to rely on, and it is unaffected; what is
lost is redundancy, not the loop. Measured on the architect box, 2026-07-29, by a session that tried.

**When it IS available it stays a fallback heartbeat at ≥1200 s, and it is not cover.** Arm it — no
design should rest on one channel — but it has delivered **nothing** across every arming measured to date on
this box, against every event the terminating watcher delivered in the same window (quince#62 carries
the dated tally; it is deliberately not copied here, so this file does not acquire arithmetic that
needs maintaining). **On the runner it has delivered once, about an hour late** — so the record differs
by machine, and "measured on this box" is load-bearing rather than pedantic: the implementer's copy of
this paragraph dropped the qualifier and was falsified within the hour. The floor under
you is `watch`'s own `--max-wait`, not this; reasoning as though the fallback protects you is exactly
what produced the fifty-minute stall. When it does fire, its **first job is a liveness assertion**,
`bin/forge-watch status --all`; if that says `dead`, say so out loud rather than ticking once and going
back to sleep. A due-but-missed tick arrives as `event=tick-overdue` and is reported, not absorbed.

**Some of your wakes are your own doing** — an approval you posted, a merge you made — and the watcher
does not suppress them (roughly a third of them, measured; quince#62). The event carries `actor=`: read
it, rather than reading a self-wake as phantom activity.

**With one exception, and on this seat it is the common one: `kind=commit` names the commit's AUTHOR,
never whoever moved the PR** (quince#222). A rebase replays the original authorship, so
`gh pr update-branch --rebase` — which *you* run, as the merging seat — produces an event reading
`actor=quince-coder[bot] kind=commit`. The identity is real, specific, and **not the actor**; the
trigger is not recoverable from a commit at all. Measured twice on quince#255, from both seats: the
architect ran the rebase and both watches named the App that did not act.
Read `kind=` before trusting `actor=`. For `comment`, `review` and `merge` the actor *is* the one who
acted; for `commit` it is the author of the work, which on a rebase is a different question from who
moved the head. **The failure this prevents is concluding your own approval is stale** — that an
implementer pushed under you — when the head moved because you rebased it yourself.

**Ending a turn with an unwatched queue is blocked once.** A `Stop` hook runs `bin/forge-watch owed
--all` — open PRs in the declared set with no live watch — and hands you the arming command; end the
turn again and it stops blocking and tells the Operator instead. It is aimed at the failure that the
implementer half produced (a session that armed nothing and stopped), and it applies here for the same
reason: this section is prose, and prose is what was already tried.

Then, on the events:

- **Escalate, never improvise.** A decision that is the Operator's — a ruling, a credential widening, a
  scope change, a privacy/policy collision — stops the loop and notifies. Record the stop **on the PR**,
  not only in a notification: a push nobody reads leaves no trace, and the forge is the memory.
- **A PR you parked pending someone else's decision is re-examined on EVERY tick**, event or no event.
  That is program-doc corollary (e), and it is the rule that a held, approvable PR waiting 64 minutes for
  an already-posted confirmation bought. Record the park on the PR itself so a fresh session rebuilds
  the set without you.
- **`event=updated … actor=unattributed` means go and look**, not nothing happened. Its commonest cause
  here is an author ticking a checklist box, which moves `updatedAt` through a channel that appears in
  no activity list.
  **UNLESS IT CARRIES `kind=post-merge`, in which case nothing is pending and there is nothing to look at**
  (quince#83). The PR was already MERGED at the previous observation, so no author is waiting on you and
  no verdict is owed. `--delete-branch` produces one of these on **every** merge — the deletion moves
  `updatedAt` a second or two after the merge and appears in no activity list either — and *"the
  commonest cause is a checklist box"* was measured false on this box: across ten merges in one evening
  the branch-deletion case fired every time and the checklist case never. So this bullet was sending a
  reader to investigate a post-merge non-event once per merge, and the sentence above stays true only
  for the case it is now narrowed to.
  It does **not** claim the branch was deleted: a label or a base change on a merged PR lands here too,
  and all of them share the only property that matters to a reader deciding whether to act.
- **`event=mergeability status=BEHIND` after your own merge is your doing**: under strict up-to-date
  protection, landing one PR invalidates every other open one, and the invalidated PR's own `updatedAt`
  does not move because nothing happened to it. Say so on those PRs rather than waiting for their
  authors to discover it.
- **`event=mergeability status=CLEAN` is yours to act on immediately — it means merge it.** A PR you
  approved while CI was running has **nothing happen to it** when the checks finish: the approval was
  the last mover, and check completion does not move `updatedAt`. That is why quince#63 sat landable
  and unmerged for sixteen minutes behind a live watch, and why this event exists (quince#65). It fires
  once. **It does not cover a park on a person** — that still moves no field at all, and corollary (e)
  is still yours.

A stalled rung (no movement on a PR for hours) is reported with what it was waiting for. A loop
that cannot say why it is waiting has nothing to wait for.

The full reasoning for all of the above, and the parts shared with the implementer half, are in
[`../../loop-protocol.md`](../../loop-protocol.md) — including the **stall counter**: when the host
client drops, file tools fail after exactly ten minutes while `Bash` keeps working, so count those
stalls, fall back to `cat`/`sed`/heredocs after the first, and state "lost N minutes to M unanswered
hook calls" in your report rather than absorbing it.

## 7. Report — then sleep with the watch armed, which is not the same as stopping

One short report: what is open across every declared repo, what you reviewed and ruled, what landed,
what is owed and by whom, and any stall time lost. Then let the watcher wake you — the architect's
normal state is asleep with a watch armed, not polling, and not finished.

**AN EMPTY QUEUE IS NOT A FINISH.** This section used to name it as one, and that was wrong
(quince#71). A reviewer's work is not done when the queue is empty; it is done when nothing further
is coming, **and that is not knowable from inside the session.** The asymmetry is the whole of it: an
implementer's set is what it AUTHORED and cannot change without it, while a reviewer's set is what
ARRIVES.

So the resting state is **watch armed and idle**, and the report says so — *"armed, pid N, idle"*,
never *"queue empty, stopping"*. A session that stops because the queue is empty is a session that
has stopped watching, which is quince#62's failure re-entering through the front door.

Measured on both sides before it was ruled, which is why it is stated rather than suggested:

- **Twice** an architect overrode the gate, armed against its *"nothing owed"*, and a PR arrived
  within ~15 minutes — quince#69 and quince#73, two sessions, about six hours apart.
- **Once** an architect obeyed it, stopped on an empty queue, and went dark with no watch and no
  fallback. The gate was silent throughout, because by its own definition nothing was owed.

Two overrides that were right and one obedience that was wrong is a gate wrong in one direction only.
**`owed --all` now returns the whole declared set unconditionally**, so the `Stop` hook blocks
whenever no watch is live regardless of queue depth — the tool no longer has to be overridden to be
correct, and its answer for a reviewer reads `declared` rather than `open PRs`.

Finishing is: **a decision that is the Operator's, or an unruled gap** — and in both cases say
exactly what would unblock you.
