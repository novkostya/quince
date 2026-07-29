# gh-array-to-graphql.jq — lift a recorded `gh pr list --json` array into the GraphQL envelope.
#
# THE INVERSE OF forge-watch's FETCH_NORMALISE, and it exists so that FIXTURES AND STUBS KEEP
# RECORDING FACTS ABOUT PRs RATHER THAN A WIRE FORMAT. The queue fetch moved from `gh pr list --json`
# to `gh api graphql` for cost (26 of 27 points were the unbounded `commits` connection). Every
# recorded payload and every hand-written stub in this repository speaks the old array shape; without
# this file each of them would have to be re-recorded to chase a wire format none of them is about.
#
# ONE DEFINITION, FOUR CONSUMERS — forge-watch's own `replay` loop stub, plus the ownership,
# composition and roundtrip suites' stubs. A copy per consumer is this project's most-repeated
# defect (a fact stated in one place and implemented in another), and four copies of a shape
# conversion would rot independently and silently.
#
# `mergedBy` IS THE ONLY PREFIXED ACTOR, matching `gh`: it renders a Bot as `app/<login>` there and
# as a bare login for comment and review authors. Undone here so the round trip is exact.
#
# An unresolved commit author is `login: ""` in `gh` and `user: null` in GraphQL. Mapped back to
# null, so the forward normaliser's `// ""` reproduces the empty string `gh` would have emitted.
# That empty string is a live defect — the shaping reads `.login // "unknown"` and an empty string is
# TRUTHY in jq, so it yields `actor=` rather than `unknown` — but it is a PRE-EXISTING one, and this
# conversion exists to preserve behaviour, not to change it.
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
                         {user: (if (.login // "") == "" then null else {login: .login} end)} ]}}} ]},
  rollup: {nodes: [ {commit: {statusCheckRollup: {contexts: {nodes:
            [ (.statusCheckRollup // [])[] | {__typename: "CheckRun", name, conclusion} ]}}}} ]}
} ]}}}}
