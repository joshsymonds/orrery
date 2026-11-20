import type { JSX } from "preact";

// Toast draws one line at the foot of the screen that says what happened, with one action button. The toast keeps no
// timer of its own: whoever shows it decides when it goes. Pressing the action only reports the press through onAction;
// the caller names the row it acts on.
export function Toast(props: { text: string; action: string; onAction: () => void }): JSX.Element {
  const { text, action, onAction } = props;
  return (
    <div class="toast" role="status">
      <span class="grow">{text}</span>
      <button type="button" class="btn quiet" onClick={onAction}>
        <span>{action}</span>
      </button>
    </div>
  );
}
