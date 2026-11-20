import type { JSX } from "preact";

import { Icon, type IconName } from "./glyphs";

// MenuItemProps is one press-able line of a Menu: its label, its icon, its key cap if it has one, its sub-text under the
// label if it has one, whether it is danger, and the action a press calls.
export interface MenuItemProps {
  label: string;
  icon: IconName;
  kbd?: string;
  sub?: string;
  danger?: boolean;
  onSelect: () => void;
}

// MenuEntry is a line of a Menu: an item, or "sep" for the rule between groups.
export type MenuEntry = MenuItemProps | "sep";

// MenuProps are the Menu's inputs: its entries, in the order they draw.
export interface MenuProps {
  items: readonly MenuEntry[];
}

// Menu draws a menu of items: each with its icon, its label and its key cap, and "sep" as a rule between groups. A press
// on an item calls that item's onSelect and no other's. The menu keeps no open state; its caller shows and hides it.
export function Menu({ items }: MenuProps): JSX.Element {
  return (
    <div class="menu" role="menu">
      {items.map((item, index) =>
        item === "sep" ? (
          <div key={index} class="menu-sep" />
        ) : (
          <button
            key={index}
            type="button"
            class={item.danger ? "menu-item danger" : "menu-item"}
            role="menuitem"
            onClick={() => item.onSelect()}
          >
            <Icon name={item.icon} />
            <span>{item.label}</span>
            {item.sub !== undefined && <span class="sub">{item.sub}</span>}
            {item.kbd !== undefined && <kbd>{item.kbd}</kbd>}
          </button>
        ),
      )}
    </div>
  );
}
