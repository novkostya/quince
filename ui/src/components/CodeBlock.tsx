import { CopyButton } from "@/components/CopyButton";

// CodeBlock is a copyable block: the text, and one subtle copy control in its corner.
//
// IT EXISTS BECAUSE THE BUTTONS WERE THE PAGE (Operator, 2026-08-14). The add-storage zfs branch had
// three full-size outline buttons reading *Copy the line*, *Copy the script* and *Copy the command*,
// each on its own row under the block it belonged to, interleaved with *Check this host's key* and
// *Test helper*. Every one was the same size and weight as the actions that DO something, so the
// screen read as a wall of buttons and the two that matter did not stand out.
//
// THE CONTROL MOVES INTO THE BLOCK'S CORNER, which is where a reader already expects it and where it
// costs no vertical space. It is an icon, not a labelled button, so it recedes — and it is inside the
// block it copies, so which one it copies needs no words.
//
// ONE STRING, DISPLAYED AND COPIED. The old shape passed the text twice — once as the `<pre>`'s
// children and once as the button's `value` — which is two chances to render one thing and copy
// another. On this form that would hand somebody a working key with a constraint they cannot see, so
// the duplication is removed rather than watched.
//
// `wrap` IS THE CALLER'S, because the two kinds of content want opposite things and neither is a
// default the other can live with:
//
//   - `"none"`  a shell SCRIPT. Wrapping folds `case` bodies and comments into ragged half-lines,
//     which reads worse than the clipping it was meant to cure. It scrolls sideways instead.
//   - `"anywhere"` a ONE-LINER — an `authorized_keys` entry or a `curl` command. There is no
//     structure to preserve and it is 200 characters, so scrolling it is scrolling forever; it wraps,
//     mid-token if it must, because a URL and a base64 key have no spaces to break at.
export function CodeBlock({
  value,
  label,
  wrap,
  testId,
  className = "",
}: {
  value: string;
  label: string;
  wrap: "none" | "anywhere";
  testId?: string;
  className?: string;
}) {
  // `whitespace-pre` and `whitespace-pre-wrap` carry the SAME specificity, so which wins is decided
  // by their order in Tailwind's output rather than in the class string. They are therefore chosen
  // here, one or the other, never layered.
  const flow =
    wrap === "none" ? "whitespace-pre overflow-x-auto" : "whitespace-pre-wrap break-all";

  return (
    <div className={`relative ${className}`}>
      {/* `pr-10` KEEPS THE TEXT OUT FROM UNDER THE BUTTON. Absolute positioning takes the control out
          of flow, so without it the first line of a long one-liner runs beneath the icon. */}
      <pre className={`rounded bg-elevated p-2 pr-10 text-xs ${flow}`} data-testid={testId}>
        {value}
      </pre>
      <div className="absolute right-1 top-1">
        <CopyButton
          value={value}
          label={label}
          inline
          testId={testId === undefined ? undefined : `copy-${testId}`}
        />
      </div>
    </div>
  );
}
