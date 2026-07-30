# gh-array-to-graphql.jq — lift a recorded `gh pr list --json` array into the GraphQL envelope.
#
# THE INVERSE OF forge-watch's FETCH_NORMALISE, and it exists so that FIXTURES AND STUBS KEEP
# RECORDING FACTS ABOUT PRs RATHER THAN A WIRE FORMAT. The queue fetch moved from `gh pr list --json`
# to `gh api graphql` for cost (26 of 27 points were the unbounded `commits` connection). Every
# recorded payload and every hand-written stub in this repository speaks the old array shape; without
# this file each of them would have to be re-recorded to chase a wire format none of them is about.
#
# ONE DEFINITION, THREE CONSUMERS — forge-watch's own `replay` loop stub, plus the ownership and
# composition suites' stubs. A copy per consumer is this project's most-repeated defect (a fact
# stated in one place and implemented in another), and three copies of a shape conversion would rot
# independently and silently.
#
# `forge-watch-roundtrip-test` is DELIBERATELY NOT a consumer, and saying so here is the point: it
# hand-writes the empty envelope, because an empty array converts to exactly that and a stub whose
# only job is to say "nothing here" should not need a file on disk to say it. An earlier version of
# this header claimed four consumers and was wrong — caught in review of quince#197.
#
# `mergedBy` IS THE ONLY PREFIXED ACTOR, matching `gh`: it renders a Bot as `app/<login>` there and
# as a bare login for comment and review authors. Undone here so the round trip is exact.
#
# An unresolved commit author is `login: ""` in `gh` and `user: null` in GraphQL. Mapped back to
# null, so the forward normaliser's `// ""` reproduces the empty string `gh` would have emitted.
# KEEP IT THAT WAY: this file's contract is that a recorded payload reaches the shaping exactly as
# `gh` would have delivered it, so a fixture asserting what happens to an unresolved author is
# asserting something real. Emitting `null` here would make that fixture vacuous.
#
# AN UNCONCLUDED CHECK IS THE SAME SHAPE ONE FIELD OVER (quince#247): `gh` renders a check still in
# flight as `conclusion: ""`, GraphQL as `null`. Mapped back to null here for the identical reason, so
# a recorded payload carrying a pending check reaches the shaping the way `gh` would have delivered
# it. Without this the round trip would silently "fix" the pending case and the equivalence suite
# would stop being able to see it.
#
# The empty string used to be a live defect as well — the shaping read `.login // "unknown"` and an
# empty string is TRUTHY in jq, so it yielded `actor=`. Fixed in the shaping (quince#199), where it
# belonged; `bin/testdata/forge/unresolved-commit-author-is-unknown.json` is the fixture that rides
# this path to prove it.
{data: {repository: {pullRequests: {nodes: [ .[] | {
  number, state, updatedAt, title, headRefName, mergedAt, mergeStateStatus,
  mergedBy: (if .mergedBy == null then null
             elif ((.mergedBy.login // "") | startswith("app/"))
               then {__typename: "Bot",  login: (.mergedBy.login[4:])}
               else {__typename: "User", login: .mergedBy.login} end),
  closingIssuesReferences: {nodes: [ (.closingIssuesReferences // [])[] | {number} ]},
  comments: {nodes: [ (.comments // [])[] |
              {createdAt, author: (if .author == null then null else {login: .author.login} end)} ]},
  reviews:  {nodes: [ (.reviews // [])[] |
              {state, submittedAt, author: (if .author == null then null else {login: .author.login} end)} ]},
  commits:  {nodes: [ (.commits // [])[] | {commit: {
              committedDate: .committedDate,
              authors: {nodes: [ (.authors // [])[] |
                         {user: (if (.login // "") == "" then null else {login: .login} end)} ]},
              # `committer` WAS DROPPED HERE WHILE THE FORWARD PATH READS IT (quince#242 step 3). The
              # shaping takes `.commit.committer.user.login // ""`, so every payload arriving through
              # this file got `committer: ""` — silently, because the empty string is exactly what an
              # unresolvable committer produces. The field was invisible to every fixture, every stub,
              # and to `forge-fetch-equivalence-test`, whose whole subject is that this conversion is
              # exact.
              #
              # It matters twice over: `review-answered` uses `committer == actor` to tell an author's
              # answer from a merging seat's rebase, and the actor arm uses the same discriminator to
              # tell my own push from somebody else moving my branch. Neither was reachable from a
              # recorded payload.
              #
              # Mapped to null rather than an empty object for the same bug-for-bug reason as
              # `authors` above: the forward `// ""` reproduces what `gh` emits, so a payload
              # recording an unresolvable committer keeps meaning that.
              committer: (if (.committer // "") == "" then null
                          else {user: {login: .committer}} end)}} ]},
  rollup: {nodes: [ {commit: {statusCheckRollup: {contexts: {nodes:
            [ (.statusCheckRollup // [])[] |
              {__typename: "CheckRun", name,
               conclusion: (if (.conclusion // "") == "" then null else .conclusion end)} ]}}}} ]}
} ]}}}}
