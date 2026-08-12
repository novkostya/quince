// WHAT COUNTS AS "THE THING THE ON-SCREEN KEYBOARD IS FOR".
//
// ONE CALLER TODAY: `useScrollFocusIntoView`, which moves a focused field clear of the dialog's
// edges and must NOT move for anything focused by a tap — scrolling between `mousedown` and
// `mouseup` swallows the press entirely (quince#762, caught by `gates-ui-e2e`).
//
// It is its own file because the judgement is subtler than the one use makes it look, not because it
// is shared. This header used to name a second caller, `keyboardScrollReset`, which was added by
// `f0d1ee7` and REVERTED by `1d0742a` — the comment outlived it here and in `useScrollFocusIntoView`,
// so a reader sent looking for the counterpart found nothing (quince#838, quince#346's class).
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
