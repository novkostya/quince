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

## 1. Assert the identity boundary — and stop if it is wrong

`approver ≠ author` is the authority model. It is structural only if the machine you are on holds
one credential and not the other:

```sh
gh auth status                         # must be an approve-capable identity, NOT the bot
ls -l ~/.config/quince/quince-bot.token 2>/dev/null   # on an architect host: must NOT exist
```

- **Bot token present on an architect host** → say so and stop. A box that can author *and*
  approve dissolves the property the whole identity ruling protects (devlog#7). Reviewing from a
  host that holds both is a finding about the host, not a detail to work around.
- **`gh` unauthenticated, or authenticated as the bot** → stop. An architect session that cannot
  submit a real review verdict is a session pretending to be one.

The mirror image on the implementer side is `deploy/runner/preflight`; this is the same assertion
pointed the other way.

## 2. Load the state

Run `/onboard` if you do not already hold the project's state this session: the devlog's one-line
state, the frontier, open questions, and the canon the current work touches. Then read what has
happened since you were last here — the newest journal entries and the most recent merged PRs, not
just the open ones. A reviewer who does not know what landed yesterday will re-litigate it.

## 3. Enumerate the work — both repos, every time

```sh
gh pr list -R novkostya/quince        --json number,title,author,reviewDecision,updatedAt
gh pr list -R novkostya/quince-devlog --json number,title,author,reviewDecision,updatedAt
```

**Both.** The product repo and the devlog. A journal or canon PR is review work exactly as code is,
and a watch that covers one repo while reporting "nothing to review" is making a claim it never
checked — that is how a devlog PR sat unreviewed for hours while the queue was reported clear.

Also list open issues in both repos when starting cold: an issue with a ruling attached is work the
architect owes, not backlog.

## 4. Review — the protocol, including what is easy to get wrong

Per PR, follow `/review-pr`. Four things belong here because each was learned the hard way:

- **Run the head under review, never `main`.** Check the branch out first. Testing a guard using
  the version that lacks the guard destroyed a container that a merged version would have
  protected — the tool you run must be the tool you are reading.
- **Diff head-at-approval against head-now before merging.** A push can land *as* an approval
  registers, so GitHub can attach your approval to a commit you never read (stale-review dismissal
  covers pushes *after* an approval, not pushes racing one):
  ```sh
  git range-diff <head-at-approval>~1..<head-at-approval> <head-now>~1..<head-now>
  ```
  Identical → the approval stands. Different → re-review before it lands.
- **Classify every red check; never wave one through.** Infrastructure (a job dying in setup, a
  registry timeout), a known flake with an issue, or a real failure. Say which. An unclassified red
  is an unread claim.
- **Refuse to approve your own authorship.** If the PR is yours, say who must approve instead
  (architect-authored docs/canon → the Operator).

Verdicts are real GitHub reviews (`gh pr review --approve` / `--request-changes`), and the body
states **what you ran**, not only what you think.

## 5. Land what is ready

`/land <n>` — preconditions checked from the API rather than from memory of this session, privacy
re-swept over the whole branch, rebase-merge, then tidy up. A branch that is behind gets rebased,
re-run and re-approved, not merged around.

## 6. Arm the loop, and let it escalate

Watch both repos for new and updated PRs, and stay quiet when nothing changed. Two properties are
not optional:

- **Silence must be distinguishable from not-looking.** Initialise with a sentinel so "never
  observed" is not the same value as "observed empty", enumerate the queue on every tick rather
  than binding it at arm time, emit queue-empty as an event, and watch *checks* alongside reviews.
  When `bin/forge-watch` exists (devlog#4), call it instead of hand-rolling a poller — its fixtures
  are the regression tests for exactly these mistakes.
- **Escalate, never improvise.** A decision that is the Operator's — a ruling, a credential
  widening, a scope change, a privacy/policy collision — stops the loop and notifies. Record the
  stop **on the PR**, not only in a notification: a push nobody reads leaves no trace, and the
  forge is the memory.

A stalled rung (no movement on a PR for hours) is reported with what it was waiting for. A loop
that cannot say why it is waiting has nothing to wait for.

## 7. Report, then stop

One short report: what is open across both repos, what you reviewed and ruled, what landed, what is
owed and by whom. Then stop and let the loop wake you — the architect's normal state is asleep with
a watch armed, not polling.
