import type { JSX } from "preact";

// ToastAction is one button of a toast: its label, and the call a press of it makes.
export interface ToastAction {
  label: string;
  onAction: () => void;
}

// Toast draws one line at the foot of the screen that says what happened, with one button for each of its actions, in
// order. The toast keeps no timer of its own: whoever shows it decides when it goes. Pressing an action only reports the
// press through its onAction; the caller names the rows it acts on.
export function Toast(props: { text: string; actions: readonly ToastAction[] }): JSX.Element {
  const { text, actions } = props;
  return (
    <div class="toast" role="status">
      <span class="grow">{text}</span>
      {actions.map((action) => (
        <button key={action.label} type="button" class="btn quiet" onClick={action.onAction}>
          <span>{action.label}</span>
        </button>
      ))}
    </div>
  );
}
