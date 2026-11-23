// The stories that bear no round-6 name, each with the round-6 component or scenario that draws it and the frontend epic that
// draws that. An entry is keyed by a story's name, or by an export's kebab name when the export is not a round-6 component
// and no story of it bears a name: that entry covers every story of the export. A component's stories are listed one by one.
// names.test.ts checks the table against the registry and round6.json.

import type { UnnamedEntry } from "./names";

export const unnamed: Readonly<Record<string, UnnamedEntry>> = {
  // ---- whole exports: the export is not a round-6 component, and each of its stories is unnamed ----

  // App draws each kind of page at an address: the inbox, the archive, a session, a pane, a step, an epic, a contract, a
  // phase, a worker session. The inbox page stands for them.
  app: { replacement: "inbox-home", epic: "F2" },
  // Mark draws the Orrery mark, the symbol the rail and the title row open on.
  mark: { replacement: "Tile", epic: "F2" },
  // RailLineage draws a project's lineage under its tile on the collapsed inbox's rail: a pill per conversation, a pill per design,
  // and a mark for each contract or epic. The rail's tiles stand for it.
  "rail-lineage": { replacement: "Tile", epic: "F2" },
  // ReaderHead draws a conversation's head, titled, or with where.
  "reader-head": { replacement: "TitleRow", epic: "F3" },
  // TitleParts draws a title's directory and line, with either alone, and a line that holds a link.
  "title-parts": { replacement: "TitleRow", epic: "F3" },
  // OutputTails draws a command's output tails: both, stdout only, stderr only.
  "output-tails": { replacement: "epic-evidence", epic: "F4" },
  // EpicWidget draws the reader's tool-call card: the live card of an epic a tool call created, under the conversation's tool call.
  "epic-widget": { replacement: "convo-epic", epic: "F3" },
  // AccountsPage draws the accounts and routes page at #/accounts, as App draws the pages at its addresses. The inbox page
  // stands for it.
  "accounts-page": { replacement: "inbox-home", epic: "F2" },
  // AccountsTable draws the accounts table of the accounts and routes page, which round 6 does not draw. The inbox page
  // stands for it, as it does for AccountsPage.
  "accounts-table": { replacement: "inbox-home", epic: "F2" },
  // AccountAdd draws the Add account form under the accounts table, which round 6 does not draw. The inbox page stands for
  // it, as it does for AccountsPage.
  "account-add": { replacement: "inbox-home", epic: "F2" },
  // RouteContexts draws the contexts section of the accounts and routes page, which round 6 does not draw. The inbox page
  // stands for it, as it does for AccountsPage.
  "route-contexts": { replacement: "inbox-home", epic: "F2" },
  // RouteSeats draws the seats table of the accounts and routes page, which round 6 does not draw. The inbox page stands for
  // it, as it does for AccountsPage.
  "route-seats": { replacement: "inbox-home", epic: "F2" },
  // AccountsNarrow draws the accounts and routes page under 760px, which round 6 does not draw. The inbox page stands for it,
  // as it does for AccountsPage.
  "accounts-narrow": { replacement: "inbox-home", epic: "F2" },

  // ---- single stories of round-6 components, each the component in a state round 6 does not draw ----

  // HappeningList draws the history of the holder epic, and an empty history.
  "happening-list/holder": { replacement: "HappeningList", epic: "F4" },
  "happening-list/empty": { replacement: "HappeningList", epic: "F4" },
  // TitleRow draws a crumb and a state, a blocker, and a page's commands.
  "title-row/crumbs-and-state": { replacement: "TitleRow", epic: "F3" },
  "title-row/blocker": { replacement: "TitleRow", epic: "F3" },
  "title-row/with-commands": { replacement: "TitleRow", epic: "F3" },
  // StateGlyph draws broken, a ring cracked through, for a failed row and an unbound pane: round 6 draws no such state.
  "state-glyph/broken": { replacement: "StateGlyph", epic: "F2" },
};
