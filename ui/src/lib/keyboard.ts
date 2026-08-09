// WHAT COUNTS AS "THE THING THE ON-SCREEN KEYBOARD IS FOR".
//
// Two separate behaviours need this same judgement and must not drift apart, so it is written once:
//
//  - `useScrollFocusIntoView` moves a focused field clear of the dialog's edges, and must NOT move
//    for anything focused by a tap — scrolling between `mousedown` and `mouseup` swallows the press
//    entirely (quince#762, caught by `gates-ui-e2e`);
//  - `keyboardScrollReset` undoes iOS's leftover viewport offset once the keyboard is really gone,
//    and must be able to tell "the keyboard closed" from "focus moved to the next field".
//
// A `<select>` is deliberately absent: iOS answers it with a picker rather than a keyboard, and it
// is tapped, so it belongs with the buttons on both counts.
export function raisesKeyboard(el: HTMLElement): boolean {
  if (el.isContentEditable) return true;
  if (el instanceof HTMLTextAreaElement) return true;
  if (el instanceof HTMLInputElement) {
    return !["button", "submit", "reset", "checkbox", "radio", "range", "color", "file", "image"].includes(
      el.type,
    );
  }
  return false;
}
