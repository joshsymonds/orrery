import type { JSX } from "preact";

// FilterChipProps are the chip's inputs: the count it draws, the label beside it, its tone, whether it is on, and the press.
export interface FilterChipProps {
  count: number;
  label: string;
  tone: "attention" | "new";
  on: boolean;
  onPress: () => void;
}

// FilterChip draws a press-able chip that narrows a list: its count in bold, then its label, in its tone. It says whether
// it is on through aria-pressed and the on class. It draws the count and label it is given, and calls onPress on each press.
export function FilterChip(props: FilterChipProps): JSX.Element {
  const { count, label, tone, on, onPress } = props;
  return (
    <button type="button" class={on ? `fchip ${tone} on` : `fchip ${tone}`} aria-pressed={on ? "true" : "false"} onClick={() => onPress()}>
      <b>{count}</b>
      {label}
    </button>
  );
}
