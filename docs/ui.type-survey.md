# What mainstream and self-hosted interfaces actually measure (quince#1155)

Measured **2026-08-17** in Chromium at 1440×900,
dark scheme unless a row says otherwise, by `ui/measure/probe.mjs`.
Every figure is **character-weighted**: the "body size" of a page is the size most of its rendered
text is actually set at, not the size its `body` rule declares. Monospace runs are excluded from
the prose figures and reported separately.

## Distributions

### body text size

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 12 | 14 | **14** | 14 | 16 | ±0 |
| Self-hosted admin UIs | 7 | 14 | 14 | **14** | 14 | 18 | ±0 |
| quince, today | 7 | 14 | 14 | **16** | 16 | 16 | ±1 |

### line-height ÷ size

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 1.19 | 1.49 | **1.51×** | 1.53 | 1.83 | ±0.01 |
| Self-hosted admin UIs | 7 | 1.2 | 1.45 | **1.47×** | 1.56 | 1.62 | ±0.05 |
| quince, today | 7 | 1.5 | 1.5 | **1.5×** | 1.5 | 1.5 | ±0 |

### primary text contrast

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 1 | 5.92 | **14.89:1** | 16.97 | 17.59 | ±0.23 |
| Self-hosted admin UIs | 7 | 8.09 | 10.89 | **13.58:1** | 17.03 | 18.42 | ±1.69 |
| quince, today | 7 | 5.8 | 8.89 | **8.89:1** | 9.63 | 14.9 | ±0.37 |

### secondary text contrast

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 14 | 6.09 | 6.5 | **9.63:1** | 16.7 | 21 | ±0.45 |
| Self-hosted admin UIs | 7 | 5.32 | 6.48 | **8.68:1** | 11.43 | 21 | ±1.13 |
| quince, today | 7 | 5.8 | 5.94 | **9.77:1** | 14.9 | 16.14 | ±2.57 |

### share of text below 4.5:1

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 0 | 0 | **1.45%** | 4.3 | 56.6 | ±0.15 |
| Self-hosted admin UIs | 7 | 0 | 0 | **1.8%** | 2.1 | 7.4 | ±0.9 |
| quince, today | 7 | 0 | 0 | **0%** | 0 | 1.4 | ±0 |

### share of text below 7:1

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 0.6 | 4.2 | **11.1%** | 36.9 | 83 | ±3 |
| Self-hosted admin UIs | 7 | 0 | 6.2 | **9%** | 33.9 | 52.1 | ±1.4 |
| quince, today | 7 | 2.5 | 7.8 | **23.2%** | 40.4 | 40.5 | ±7.7 |

### measured column width

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 283 | 700 | **766.5** | 1424 | 1440 | ±1.5 |
| Self-hosted admin UIs | 7 | 385 | 734 | **1184** | 1440 | 1440 | ±225 |
| quince, today | 7 | 321 | 448 | **576** | 1087 | 1121 | ±64 |

### button height

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 15 | 16 | 28 | **32** | 36 | 57 | ±0 |
| Self-hosted admin UIs | 6 | 16 | 30 | **47** | 56 | 78 | ±1 |
| quince, today | 6 | 32 | 32 | **34** | 36 | 40 | ±2 |

### list/table row height

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 15 | 16 | 24 | **40** | 42 | 135 | ±2 |
| Self-hosted admin UIs | 4 | 36 | 37 | **52** | 67 | 68 | ±15 |
| quince, today | 0 | — | — | — | — | — | — |

### gap between stacked blocks

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 16 | 1 | 8 | **9.5** | 16 | 48 | ±0.5 |
| Self-hosted admin UIs | 6 | 8 | 8 | **15** | 16 | 16 | ±1 |
| quince, today | 7 | 8 | 12 | **12** | 16 | 16 | ±0 |

### padding inside containers

| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Mainstream web apps | 15 | 8 | 12 | **16** | 16 | 24 | ±0 |
| Self-hosted admin UIs | 7 | 12 | 16 | **16** | 16 | 40 | ±0 |
| quince, today | 6 | 12 | 16 | **16** | 20 | 20 | ±0 |

## Every surface measured

`fg` / `bg` are the colours as **rendered**, after compositing every translucent layer above the
first opaque one. `<4.5` is the share of rendered characters whose pair is below WCAG AA for body
text — the floor, not the goal.

| surface | body | line | scale present | fg on bg | c1 | c2 | <4.5 | <7 | col | btn | row | gap | pad | chars |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **Mainstream web apps** | | | | | | | | | | | | | | |
| GitHub — repository code view | 14px | 21.11 (1.51×) | 12 · 14 · 16 | `#f0f6fc` on `#0d1117` | 17.39 | 6.5 | 4.3% | 36.9% | 1440 | 32 | 41 | 8 | 16 | 10279 |
| GitHub — issue thread | 14px | 21.08 (1.51×) | 12 · 14 · 21 · 32 | `#f0f6fc` on `#0d1117` | 17.39 | 6.5 | 0.2% | 5.5% | 878 | 32 | 18 | 16 | 16 | 7483 |
| GitHub — pull request list | 12px | 18.02 (1.5×) | 12 · 14 · 16 | `#f0f6fc` on `#0d1117` | 17.39 | 6.5 | 6.7% | 48.4% | 1440 | 21 | 18 | 16 | 8 | 3973 |
| GitLab — project overview | 14px | 21.05 (1.5×) | 14 · 21 | `#ececef` on `#18171d` | 15.11 | 10.07 | 13.8% | 14.1% | 765 | 28 | 42 | 16 | 12 | 10073 |
| Stack Overflow — tagged questions ⚠ | 16px | 24 (1.5×) | 12 · 16 · 24 · 40 | `#b6b6b6` on `#000000` | 10.36 | 18.76 | 0% | 0% | 896 | — | — | 8 | 32 | 256 |
| Reddit — subreddit feed | 14px | 20.14 (1.44×) | 12 · 14 · 18 | `#b7cad4` on `#0e1113` | 11.2 | 16.7 | 1.3% | 4.2% | 700 | 32 | 40 | 9 | 16 | 19038 |
| YouTube — watch page | 14px | 21.9 (1.56×) | 12 · 14 · 16 · 20 | `#f1f1f1` on `#0f0f0f` | 16.97 | 8.25 | 0% | 1.4% | 1440 | 39 | — | 16 | 8 | 4616 |
| npm — package page | 16px | 20.64 (1.29×) | 14 · 16 · 17 · 18 · 20 | `#202020` on `#ffffff` | 16.29 | 12.63 | 0.8% | 25.2% | 731 | 27.6 | 40 | 16 | 16 | 1781 |
| MDN — reference page | 16px | 25.82 (1.61×) | 12.8 · 16 | `#ffffff` on `#18191b` | 17.59 | 9.18 | 2.6% | 5.1% | 768 | 57 | 46 | 8 | 8 | 28063 |
| Stripe — API reference | 14px | 21.4 (1.53×) | 12 · 14 | `#eceef1` on `#14171d` | 15.44 | 6.22 | 1.7% | 22.3% | 503 | 16 | 47 | 4 | 16 | 26616 |
| Vercel — docs | 13px | 19.83 (1.53×) | 13 · 14 · 16 · 18 · 24 | `#888888` on `#000000` | 5.92 | 21 | 0% | 43% | 283 | 32 | 28 | 1 | 24 | 5795 |
| Cloudflare — developer docs | 13px | 19.41 (1.49×) | 13 · 14 · 15 · 16 | `#f0e3de` on `#151414` | 14.66 | 6.09 | 0% | 1.7% | 852 | 32 | 16 | 1 | 8 | 9246 |
| Linear — docs | 14px | 21 (1.5×) | 14 · 15 · 20 | `#8a8f98` on `#141516` | 5.63 | 17.18 | 0% | 83% | 1440 | 36 | 36 | 48 | 20 | 1158 |
| Tailwind CSS — docs | 14px | 25.61 (1.83×) | 14 · 16 | `#ffffff` on `#ffffff` | 1 | — | 8.1% | 8.1% | 600 | 24 | 24 | 8 | 24 | 7849 |
| Google — Google Fonts (an app surface Google itself builds) | 16px | 21.92 (1.37×) | 14 · 16 · 18 · 21 · 22 · 24 · 32 · 36 · 48 | `#e3e3e3` on `#131314` | 14.47 | 10.9 | 0% | 0.6% | 588 | 48 | 135 | 8 | 16 | 6845 |
| Supabase — docs | 15px | 27.04 (1.8×) | 12 · 12.8 · 13 · 15 · 16 · 18 · 22 | `#00c573` on `#ffffff` | 2.27 | — | 1.6% | 1.6% | 706 | 28 | 40 | 24 | 12 | 4937 |
| Hacker News — front page (deliberate outlier) | 13.33px | 15.87 (1.19×) | 9.33 · 10.67 · 13.33 | `#828282` on `#f6f6ef` | 3.54 | 19.35 | 56.6% | 56.6% | 1424 | — | 19 | 10 | — | 3282 |
| **Self-hosted admin UIs** | | | | | | | | | | | | | | |
| Grafana — play instance | 14px | 22.67 (1.62×) | 12 · 14 · 16 | `#ccccdc` on `#181b1f` | 10.89 | 5.32 | 0% | 52.1% | 734 | 16 | 36 | 8 | 16 | 747 |
| Home Assistant — demo | 14px | 21.86 (1.56×) | 11.67 · 12 · 14 · 16 | `#141414` on `#ffffff` | 18.42 | 6.48 | 1.8% | 6.2% | 385 | 56 | — | 8 | 16 | 788 |
| Forgejo — Codeberg (a public instance of the self-hosted forge) | 14px | 20.14 (1.44×) | 14 · 16 | `#c0cfe0` on `#1d262f` | 9.66 | 11.43 | 7.4% | 9% | 1214 | 30 | 37 | 14 | 14 | 10494 |
| openHAB — demo | 14px | 20.63 (1.47×) | 10.67 · 12 · 14 · 16 · 18 · 20 · 24 · 30 · 32 · 34 | `#ffffff` on `#1c1c1d` | 17.03 | 5.97 | 2.1% | 33.9% | 1440 | — | 68 | 16 | 16 | 794 |
| Netdata — public agent dashboard | 14px | 16.8 (1.2×) | 12 · 14 · 16 · 36 | `#93a4a4` on `#000000` | 8.09 | 8.68 | 1.9% | 9.9% | 1440 | 78 | 67 | 16 | 16 | 1297 |
| Jellyfin — stable demo (signed in) ⚠ | 14.88px | 20.08 (1.35×) | 14.88 · 26.78 | `#cfcfcf` on `#101010` | 12.24 | 10.39 | 0% | 0% | 804 | 46.9 | — | 3.7 | 47.5 | 646 |
| Immich — demo (signed in) | 14px | 20.3 (1.45×) | 12 · 14 · 16 | `#e5e7eb` on `#000000` | 16.96 | 21 | 0% | 0% | 1184 | 48 | — | 16 | 12 | 662 |
| Portainer — demo (signed in) ⚠ | 14px | 18.06 (1.29×) | 14 · 18 · 20 | `#fef0c7` on `#713b12` | 7.92 | 8.06 | 0% | 0% | 311 | 46 | — | 25 | 20 | 167 |
| Synology DSM — live demo | 18px | 28 (1.56×) | 12 · 15 · 16 · 18 · 22 | `#ffffff` on `#2e2e2e` | 13.58 | 10.94 | 0% | 6.2% | 527 | 46 | — | — | 40 | 2070 |
| Proxmox VE — web console (local install) ⚠ | 13px | 15.93 (1.23×) | 12 · 13 · 15 · 16 | `#f2f2f2` on `#262626` | 13.52 | 6.75 | 0% | 23.9% | 1125 | 24 | 29 | 5 | — | 194 |
| OpenWrt LuCI (local install) | `NOT MEASURED — not run — unset: LUCI_URL` | | | | | | | | | | | | |
| Uptime Kuma (local instance, signed in) | `NOT MEASURED — not run — unset: KUMA_URL` | | | | | | | | | | | | |
| Cockpit (local instance) | `NOT MEASURED — not run — unset: COCKPIT_URL` | | | | | | | | | | | | |
| **quince, today** | | | | | | | | | | | | | | |
| quince — Onboarding ▸ HTTPS | 16px | 24 (1.5×) | 14 · 16 · 18 | `#b0b7c1` on `#14171c` | 8.89 | 14.9 | 0% | 2.7% | 630 | — | — | 16 | — | 2019 |
| quince — Onboarding ▸ Certificate ⚠ | 16px | 24 (1.5×) | 16 · 20 · 24 | `#e8eaed` on `#0b0d10` | 16.14 | 9.63 | 0% | 5.8% | 672 | 36 | — | 16 | — | 294 |
| quince — Onboarding ▸ Certificate confirm ⚠ | 16px | 24 (1.5×) | 16 · 20 · 24 | `#e8eaed` on `#0b0d10` | 16.14 | — | 0% | 0% | 672 | — | — | 16 | — | 214 |
| quince — First-run setup (set password) ⚠ | 16px | 24 (1.5×) | 16 · 18 · 20 | `#e8eaed` on `#14171c` | 14.9 | 8.89 | 0% | 8.3% | 1425 | 36 | — | 16 | 24 | 84 |
| quince — Sign in ⚠ | 16px | 24 (1.5×) | 16 · 18 · 20 | `#e8eaed` on `#14171c` | 14.9 | 8.89 | 0% | 8.3% | 1425 | 36 | — | 16 | 24 | 84 |
| quince — Onboarding ▸ Storage ⚠ | 16px | 24 (1.5×) | 16 · 20 · 24 | `#b0b7c1` on `#0b0d10` | 9.63 | 16.14 | 0% | 8.2% | 672 | 36 | — | 16 | — | 343 |
| quince — Add storage ⚠ | 16px | 24 (1.5×) | 14 · 16 · 20 · 24 | `#b0b7c1` on `#0b0d10` | 9.63 | 16.14 | 0% | 13.8% | 1121 | 36 | — | 19 | 16 | 181 |
| quince — Home | 14px | 21 (1.5×) | 14 · 16 · 20 | `#b0b7c1` on `#14171c` | 8.89 | 5.8 | 0% | 26.3% | 321 | 32 | — | 12 | 20 | 1146 |
| quince — Device details | 16px | 24 (1.5×) | 14 · 16 · 20 · 24 | `#e8eaed` on `#14171c` | 14.9 | 5.8 | 0% | 23.2% | 1121 | 36 | — | 8 | 16 | 943 |
| quince — Settings | 14px | 21 (1.5×) | 14 · 16 · 20 | `#b0b7c1` on `#0b0d10` | 9.63 | 16.14 | 0% | 7.8% | 448 | 40 | — | 16 | 12 | 927 |
| quince — Settings ▸ Sign-in (the page the Operator finds readable) | 16px | 24 (1.5×) | 16 · 20 | `#b0b7c1` on `#0b0d10` | 9.63 | 9.77 | 0% | 2.5% | 576 | 36 | — | 12 | 16 | 1453 |
| quince — Storage details | 16px | 24 (1.5×) | 14 · 16 · 20 | `#8b93a0` on `#14171c` | 5.8 | 14.9 | 1.4% | 40.4% | 1087 | 32 | — | 8 | 16 | 1407 |
| quince — Home (light theme) | 14px | 21 (1.5×) | 14 · 16 · 20 | `#4f555f` on `#ffffff` | 7.51 | 5.94 | 0% | 40.5% | 321 | 32 | — | 12 | 20 | 1055 |

⚠ = fewer than 25 distinct styled text runs — in practice a sign-in screen or a bot wall.
Kept because an absent row and a weak row say different things; excluded from the distributions
above because at that size one label decides the answer.

## Not measured

| surface | why |
| --- | --- |
| OpenWrt LuCI (local install) | not run — unset: LUCI_URL |
| Uptime Kuma (local instance, signed in) | not run — unset: KUMA_URL |
| Cockpit (local instance) | not run — unset: COCKPIT_URL |

## Does the scale change on a phone?

Same probe, same day, Playwright's iPhone descriptor — device scale factor, touch flags and
Safari UA together, because sites serve different markup on the UA alone.

| surface | body px | line-height | button px |
| --- | --- | --- | --- |
| quince — Home | 14 → **14** | 1.5 → 1.5 | 32 → 36 |
| quince — Settings ▸ Sign-in (the page the Operator finds readable) | 16 → **16** | 1.5 → 1.5 | 36 → 40 |
| quince — Storage details | 16 → **16** | 1.5 → 1.5 | 32 → 36 |
| quince — Home (light theme) | 14 → **14** | 1.5 → 1.5 | 32 → 36 |
| GitHub — repository code view | 14 → **14** | 1.51 → 1.53 | 32 → 32 |
| GitHub — issue thread | 14 → **14** | 1.51 → 1.5 | 32 → 30 |
| Reddit — subreddit feed | 14 → **14** | 1.44 → 1.43 | 32 → 32 |
| YouTube — watch page | 14 → **14** | 1.56 → 1.29 | 39 → 48 |
| MDN — reference page | 16 → **16** | 1.61 → 1.71 | 57 → 18.5 |
| Stripe — API reference | 14 → **14** | 1.53 → 1.54 | 16 → 22 |
| Linear — docs | 14 → **14** | 1.5 → 1.5 | 36 → 48 |
| Google — Google Fonts (an app surface Google itself builds) | 16 → **14** | 1.37 → 1.44 | 48 → 48 |
| Grafana — play instance | 14 → **14** | 1.62 → 1.64 | 16 → 16 |
| Home Assistant — demo | 14 → **14** | 1.56 → 1.55 | 56 → 56 |
| Forgejo — Codeberg (a public instance of the self-hosted forge) | 14 → **14** | 1.44 → 1.45 | 30 → 30 |
| Netdata — public agent dashboard | 14 → **14** | 1.2 → 1.2 | 78 → 78 |
| Immich — demo (signed in) | 14 → **14** | 1.45 → 1.46 | 48 → 48 |
| Synology DSM — live demo | 18 → **18** | 1.56 → 1.56 | 46 → 36 |

**Body size is UNCHANGED between desktop and phone on 13 of 14 comparison surfaces**, and
the ones that differ go DOWN on the phone rather than up. So a responsive type scale is not what
this category does, and quince does not need one — which is the opposite of what was expected
before measuring, and is why the phone pass was worth running rather than reasoning about.

## The native reference — iOS Dynamic Type

Read live from Apple's Human Interface Guidelines, **Large (default)** size class, 2026-08-17.
Included because the web set and the platform DISAGREE, and quince's first-class client is an
iPhone: a web-only comparison would have answered half the question and looked complete.

| iOS text style | size / line-height | ratio |
| --- | --- | --- |
| Large Title | 34 / 41 | 1.21× |
| Title 1 | 28 / 34 | 1.21× |
| Title 2 | 22 / 28 | 1.27× |
| Title 3 | 20 / 25 | 1.25× |
| Headline (semibold) | 17 / 22 | 1.29× |
| Body | 17 / 22 | 1.29× |
| Callout | 16 / 21 | 1.31× |
| Subhead | 15 / 20 | 1.33× |
| Footnote | 13 / 18 | 1.38× |
| Caption 1 | 12 / 16 | 1.33× |

iOS Body is **17pt** where the web set's median is **14px**, and iOS runs a TIGHTER line at 1.29×
against the web's 1.51×. Native is bigger per glyph and denser per line. A user who has moved the
Dynamic Type slider gets 15pt or 19pt and a native app follows them; quince follows nothing, which
is an asymmetry rather than a defect but is not measured here.

## Which steps the comparison set actually uses

How many measured surfaces carry each size as a visible step (≥1.5% of their rendered characters).

| size | surfaces carrying it |
| --- | --- |
| 9.33px | █ 1 |
| 10.67px | ██ 2 |
| 11.67px | █ 1 |
| 12px | █████████████ 13 |
| 12.8px | ██ 2 |
| 13px | ███ 3 |
| 13.33px | █ 1 |
| 14px | █████████████████████████ 25 |
| 15px | ████ 4 |
| 16px | ████████████████████████ 24 |
| 17px | █ 1 |
| 18px | ████████ 8 |
| 20px | ██████████ 10 |
| 21px | ███ 3 |
| 22px | ███ 3 |
| 24px | ████ 4 |
| 30px | █ 1 |
| 32px | ███ 3 |
| 34px | █ 1 |
| 36px | ██ 2 |
| 48px | █ 1 |

