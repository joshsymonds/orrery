import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { AskLine, Badge } from "./asks";
import { CompactHead } from "./compacthead";
import type { Conversation } from "./conversation";
import type { Fold } from "./headfold";
import type { Row } from "./rows";
import { ContextMeter, contextFigure, PaneStrip, type StripHost } from "./strip";
import { type ContractBandLine, foldHead, SubjectBand } from "./subjectband";
import { lastPathElement } from "./titleparts";
import { TitleRow } from "./titlerow";
import type { RailGroup, Thing } from "./work";
import { WorkRail } from "./workrail";

// HeadProps is what the conversation's Head draws: the row and its conversation, the strip's host and whether it shows,
// the asks, the folded line's badges, the fold, and what each control does.
export interface HeadProps {
  row: Row;
  conversation: Conversation | null;
  title: string;
  host: StripHost | null;
  showStrip: boolean;
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
  // here is the epic open in the panel beside the conversation, whose band line says so.
  here?: string;
  // contracts are the contracts that wait on the conversation's owner, drawn as the band's contract lines;
  // onOpenContract opens one, and hereContract is the contract open in the panel beside the conversation.
  contracts?: readonly ContractBandLine[];
  onOpenContract?: (id: string) => void;
  hereContract?: string;
  // work is the middle conversation's groups under 760px, drawn as tiles under the band: the groups, the key of the
  // thing open, and what a tile opens. Absent, the head draws no tiles.
  work?: HeadWork;
}

// HeadWork is what the strip of tiles under the band draws.
export interface HeadWork {
  groups: RailGroup[];
  lit: string;
  onOpen: (thing: Thing) => void;
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
  work,
  here,
  contracts,
  onOpenContract,
  hereContract,
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
  // tiles is whether the band has the strip of tiles under it, which then carries the control that hides the header.
  const tiles = work !== undefined && work.groups.length > 0;
  // The phone layout draws the context bare in the title row, beside More, open or folded, and leaves it out of the
  // strip.
  const context = conversation?.state?.context;
  const figure = showStrip && context !== undefined ? contextFigure(context) : null;
  const phoneContext = context !== undefined && figure !== null ? <ContextMeter context={context} bare figure={figure} /> : null;
  return (
    <div class="convo-head" data-fold={fold}>
      <TitleRow
        crumbs={[...(dirName === "" ? [] : [{ label: dirName, place: true, host: host?.host }]), { label: title }]}
        onBack={onBack}
        note={note}
        strip={strip}
      >
        {phoneContext !== null && <span class="phone-ctx">{phoneContext}</span>}
        <button type="button" class="icon find" aria-label="Find in this conversation" title="Find  /" onClick={() => onFind()}>
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5" /></svg>
        </button>
        <button
          type="button"
          class="icon more"
          aria-label="More"
          title="Archive, hide meters, keys"
          aria-expanded={menuOpen}
          onClick={() => setMenuOpen((open) => !open)}
        >
          <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z" /></svg>
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
        <>
          <SubjectBand
            goal={conversation?.state?.goal ?? ""}
            lines={lines}
            onOpen={onOpenEpic}
            onHide={narrow && !tiles ? onHide : undefined}
            here={here}
            contracts={contracts}
            onOpenContract={onOpenContract}
            hereContract={hereContract}
          />
          {work !== undefined && tiles && (
            <WorkRail
              groups={work.groups}
              lit={work.lit}
              layout="strip"
              onOpen={work.onOpen}
              end={narrow ? foldHead(onHide) : undefined}
            />
          )}
        </>
      ) : (
        <CompactHead asking={askingWords} badges={badges} onOpen={onShow} />
      )}
    </div>
  );
}
