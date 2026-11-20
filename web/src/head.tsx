import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { AskLine, Badge } from "./asks";
import { CompactHead } from "./compacthead";
import type { Conversation } from "./conversation";
import type { Fold } from "./headfold";
import type { Row } from "./rows";
import { PaneStrip, type StripHost } from "./strip";
import { SubjectBand } from "./subjectband";
import { lastPathElement } from "./titleparts";
import { type InboxMark, TitleRow } from "./titlerow";

// HeadProps is what the conversation's Head draws: the row and its conversation, the strip's host and whether it shows,
// the asks, the folded line's badges, the fold, and what each control does.
export interface HeadProps {
  row: Row;
  conversation: Conversation | null;
  title: string;
  host: StripHost | null;
  showStrip: boolean;
  mark: InboxMark;
  onBack: () => void;
  note: string;
  lines: readonly AskLine[];
  askingWords: string;
  badges: readonly Badge[];
  fold: Fold;
  narrow: boolean;
  onOpenEpic: (epic: string) => void;
  onFind: () => void;
  onHide: () => void;
  onShow: () => void;
  menu: HeadMenu;
}

// HeadMenu is what the More menu's items do.
export interface HeadMenu {
  stripsShown: boolean;
  toggleStrips: () => void;
  markSeen: () => void;
  archive: (() => void) | null;
  keys: () => void;
}

// MenuItem is one item of the More menu: its label, the key listed beside it when it has one, and what it runs.
interface MenuItem {
  label: string;
  shortcut?: string;
  run: () => void;
}

// Head is a conversation's header: its title row with the find control and the More menu, then either the strip and the
// band of what it is for and what waits on it, or, when folded, the one folded line. The strip is drawn with the open
// header only. The hide control on the band and the Hide the header item are drawn only in the narrow layout.
export function Head({
  row,
  conversation,
  title,
  host,
  showStrip,
  mark,
  onBack,
  note,
  lines,
  askingWords,
  badges,
  fold,
  narrow,
  onOpenEpic,
  onFind,
  onHide,
  onShow,
  menu,
}: HeadProps): JSX.Element {
  const [menuOpen, setMenuOpen] = useState(false);
  // choose runs a More menu item and closes the menu.
  const choose = (run: () => void) => {
    setMenuOpen(false);
    run();
  };
  const items: MenuItem[] = [
    { label: "Find in this conversation", shortcut: "/", run: onFind },
    { label: menu.stripsShown ? "Hide the meters" : "Show the meters", shortcut: "m", run: menu.toggleStrips },
    { label: "Mark seen", shortcut: "x", run: menu.markSeen },
    ...(menu.archive === null ? [] : [{ label: "Archive", shortcut: "A", run: menu.archive }]),
    { label: "Keys", shortcut: "?", run: menu.keys },
    ...(narrow ? [{ label: "Hide the header", run: onHide }] : []),
  ];
  const strip = showStrip && fold === "open" ? <PaneStrip host={host} context={conversation?.state?.context} /> : undefined;
  const dirName = lastPathElement(row.project);
  return (
    <div class="convo-head" data-fold={fold}>
      <TitleRow
        crumbs={[...(dirName === "" ? [] : [{ label: dirName, place: true }]), { label: title }]}
        onBack={onBack}
        mark={mark}
        note={note}
        strip={strip}
      >
        <button type="button" class="icon find" aria-label="Find in this conversation" title="Find  /" onClick={() => onFind()}>
          ⌕
        </button>
        <button
          type="button"
          class="icon more"
          aria-label="More"
          title="Archive, hide meters, keys"
          aria-expanded={menuOpen}
          onClick={() => setMenuOpen((open) => !open)}
        >
          ⋯
        </button>
        {menuOpen && (
          <div class="head-menu" role="menu">
            {items.map((item) => (
              <button key={item.label} type="button" role="menuitem" onClick={() => choose(item.run)}>
                {item.label}
                {item.shortcut !== undefined && <kbd>{item.shortcut}</kbd>}
              </button>
            ))}
          </div>
        )}
      </TitleRow>
      {fold === "open" ? (
        <SubjectBand
          goal={conversation?.state?.goal ?? ""}
          lines={lines}
          onOpen={onOpenEpic}
          onHide={narrow ? onHide : undefined}
        />
      ) : (
        <CompactHead asking={askingWords} badges={badges} onOpen={onShow} />
      )}
    </div>
  );
}
