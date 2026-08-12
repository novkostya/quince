// THE ONE CLASS STRING BEHIND EVERY FULL-WIDTH FORM CONTROL — a constant rather than a convention,
// because the convention already failed once.
//
// `Input` and the `<select>` in `ConfigEditor` carried this shape as two character-for-character
// copies: same `h-9 w-full rounded-lg border border-line bg-bg px-3 text-sm text-fg`, maintained
// twice, and nothing connected them. Extracting a shared `Select` COMPONENT would not have fixed
// that on its own — it would have left two identical *responsive* strings where there were two
// identical static ones, which is the same defect one size larger (quince#616).
//
// So the export is the string, and the components below it are thin. A third full-width control
// gets 16px-on-mobile by importing this, not by remembering to.
//
// `text-base sm:text-sm` — 16px on phones, 14px from `sm` up — is the load-bearing part, and it is
// not a style preference. iOS Safari zooms the whole page in when a focused control's computed
// font-size is under 16px, which put quince's login field, backup-password field and settings form
// behind a jump-and-rescroll on the device the product is designed around. WebKit computes the
// target scale as `16 / fontSize`, so 14px was a 1.14x zoom on every tap
// (`Source/WebKit/UIProcess/API/ios/WKWebViewIOS.mm`, `webViewStandardFontSize`, read on `main`
// 2026-08-04). At >= 16px the ratio clamps to `minimumScale` and nothing moves.
//
// The mobile-first form is what keeps this from being a redesign: desktop stays 14px, which is the
// density `ui.design.md` asks for. The same idiom is already in `button.tsx` and `Sidebar.tsx`.
//
// DO NOT "fix" this in the viewport meta. `maximum-scale=1` / `user-scalable=no` also stops the
// zoom and disables pinch-zoom for the whole app — WCAG 1.4.4, and `ui/index.html` is deliberately
// correct as it stands. Ruled binding on quince#616.
//
// `h-10 sm:h-9` — 40px on phones, 36px from `sm` up — is the HEIGHT half of the same mobile-first
// idiom, and it is the other half of the line above (quince#619). `button.tsx` has stepped its
// height by breakpoint since the qn.6a mobile pass; this string did not, so the two shared surfaces
// disagreed about touch height and only one of them varied.
//
// The font change is what made it close to required: `text-base` puts 16px of type inside the box,
// and 16px in a 36px box leaves about 10px of vertical padding in total. Desktop is unchanged at
// 36px, so this is not a redesign — it is the two components agreeing.
//
// NOT 44px, and that is the ruling rather than an oversight. The commonly cited figure would raise
// every control on a page canon wants dense, which is exactly why `Button` steps instead of picking
// one number. Matching `Button` was ruled the answer; **quince meets no 44px bar and this does not
// change that** — quince#619 records the target-size question as unmeasured, on a device and against
// platform guidance, and it stays that way.
export const fieldBase =
  "h-10 w-full rounded-lg border border-line bg-bg px-3 text-base text-fg sm:h-9 sm:text-sm " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50";
