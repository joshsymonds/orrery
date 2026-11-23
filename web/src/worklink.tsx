import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { Icon, type IconName } from "./glyphs";
import { Menu, type MenuEntry } from "./menu";
import type { WorkLinkItem } from "./work";

// WorkLinkProps are the links a conversation's title row draws: what it works on, whether the layout is the phone's, and
// what a press on a link calls, with the thing it opens.
export interface WorkLinkProps {
  items: readonly WorkLinkItem[];
  narrow: boolean;
  onOpen: (item: WorkLinkItem) => void;
}

// linksAtDesk and linksNarrow are how many links the title row draws before its "+n" control.
const linksAtDesk = 2;
const linksNarrow = 1;

// markOf is the icon of each kind of thing: the mark its link draws, and the one its menu item draws.
const markOf: Record<WorkLinkItem["kind"], IconName> = { design: "commit", epic: "goal", contract: "doc" };

// WorkLink draws what a conversation is working on, in its title row after the title: each thing as a plain link of its mark
// and its name, two at the desk and one under 760px, then a "+n" control for the rest, which opens a menu of all of them. Each
// link and each menu item calls onOpen with its thing. A conversation that works on nothing draws nothing.
export function WorkLink({ items, narrow, onOpen }: WorkLinkProps): JSX.Element | null {
  const [open, setOpen] = useState(false);
  if (items.length === 0) {
    return null;
  }
  const shown = narrow ? linksNarrow : linksAtDesk;
  const more = items.length - shown;
  // choose shuts the menu and opens the thing it names.
  const choose = (item: WorkLinkItem) => {
    setOpen(false);
    onOpen(item);
  };
  const entries: MenuEntry[] = items.map((item) => ({
    label: item.name,
    icon: markOf[item.kind],
    onSelect: () => choose(item),
  }));
  return (
    <span class="wlinks" aria-label="what this conversation is working on">
      {items.slice(0, shown).map((item) => (
        <button key={item.key} type="button" class="wlink" title={`Open ${item.name}`} onClick={() => onOpen(item)}>
          <Icon name={markOf[item.kind]} />
          <span class="nm">{item.name}</span>
        </button>
      ))}
      {more > 0 && (
        <>
          <button
            type="button"
            class="wlink-more"
            aria-expanded={open}
            title={`All ${items.length} things this conversation is working on`}
            onClick={() => setOpen((shut) => !shut)}
          >
            {`+${more}`}
          </button>
          {open && <Menu items={entries} />}
        </>
      )}
    </span>
  );
}
