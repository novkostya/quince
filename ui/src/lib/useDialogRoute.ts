import { useCallback } from "react";
import { useLocation, useNavigate } from "react-router-dom";

// A DIALOG IS A PLACE YOU WENT, SO OPENING ONE IS A NAVIGATION AND CLOSING ONE IS GOING BACK.
// quince#931, and the third instance of one rule: quince#838 gave scrolling back to the browser and
// quince#908 §4 gave a multi-step flow a route instead of an accordion. Modelling a modal as a
// boolean in a component throws away everything the platform already implements for a place.
//
// THE BUG THIS IS BUILT FOR, Operator-reported 2026-08-15 from a phone. With the keyboard open,
// Safari scrolls the document to clear the focused field. Close the dialog and the page underneath
// stays where Safari left it — you opened a dialog from the top of a device page and came back to
// its Backup history. Nothing restored the offset because nothing had recorded that leaving the
// page was a navigation at all.
//
// A HISTORY ENTRY IS THE RECORD. `history.scrollRestoration` is `"auto"` — `useScrollReset` keeps it
// that way on purpose — so the browser saves the document offset when we push and puts it back when
// we pop. Closing therefore restores the page to where it was BEFORE the keyboard moved it, and not
// one line here does the restoring.
//
// A QUERY PARAM, NOT A PATH SEGMENT, AND THAT IS LOAD-BEARING RATHER THAN A SHORTCUT.
// `useScrollReset` sends a new screen to the top and keys that on the PATHNAME, stating in as many
// words that a query-only change is not a new screen and must not move a page under a user who
// navigated nowhere. `/devices/x/encryption` is a new pathname, so opening a dialog would scroll the
// page to the top — the very defect class this is fixing, reintroduced by the fix. The param leaves
// the pathname alone, so the reset hook correctly does nothing.
//
// It still gives the two things quince#931 wants from a URL: Back closes the dialog, and the address
// can be sent to someone. `?dialog=encryption` is uglier than a path segment and can become one the
// day a dialog is worth its own screen — at which point it is a page, which is quince#846's answer
// and a different decision from this one.
const PARAM = "dialog";

type DialogState = { dialogPushed?: boolean } | null;

export function useDialogRoute(key: string): {
  open: boolean;
  onOpenChange: (next: boolean) => void;
} {
  const location = useLocation();
  const navigate = useNavigate();
  const open = new URLSearchParams(location.search).get(PARAM) === key;

  const onOpenChange = useCallback(
    (next: boolean): void => {
      if (next === open) return;

      if (next) {
        const search = new URLSearchParams(location.search);
        search.set(PARAM, key);
        // A PUSH, so that closing can be a POP. The marker rides on the new entry rather than in a
        // ref, because the question it answers — "is there an entry of mine to go back to?" — has to
        // survive a reload, and a ref does not.
        navigate(
          { pathname: location.pathname, search: `?${search.toString()}` },
          { state: { ...(location.state as object | null), dialogPushed: true } },
        );
        return;
      }

      // GOING BACK IS THE CLOSE, and it is the only branch that restores the scroll offset: a push
      // of the closed URL would be a new entry, which the browser starts wherever the last one
      // ended — the bug, in a new costume.
      if ((location.state as DialogState)?.dialogPushed) {
        navigate(-1);
        return;
      }

      // NOTHING OF OURS TO GO BACK TO: a link somebody sent, or a reload with the dialog open. A
      // `-1` here would leave the app entirely, so the param is dropped in place instead. The user
      // arrived at this dialog, so the page behind it has no earlier offset to restore anyway.
      const search = new URLSearchParams(location.search);
      search.delete(PARAM);
      const rest = search.toString();
      navigate({ pathname: location.pathname, search: rest ? `?${rest}` : "" }, { replace: true });
    },
    [open, key, location, navigate],
  );

  return { open, onOpenChange };
}
