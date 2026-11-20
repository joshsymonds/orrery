// The stories of App: the inbox and the archived view, and each kind of page App shows, each drawn by PageStory at an address
// over the fixture slices. Page holds the states of the live scenarios: the address that names a state draws it, and
// where none does, an after step clicks the way there.

import { Code } from "@connectrpc/connect";
import type { VNode } from "preact";

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
import { dockSlices, gatesSession, warmupSession } from "./fixtures/dock";
import {
  type AskAnswer,
  contractStories,
  emptySlices,
  type EpicScenario,
  epicScenarios,
  type PickerRowInit,
  type Slices,
  shippedSlices,
} from "./fixtures/slices";
import { endedSlices, handoverSlices, movedSlices } from "./fixtures/steer";
import { createFixtureTransport, type FixtureTransport } from "./fixtures/transport";
import type { Address, ListPanelKind, PageView } from "./router";

const rota = "lighthouse/rota-export";
const tide = "tidepool/tide-alerts";
const holder = "orrery/holder-scopes";
const notices = "orrery/notices-usable";
const harbour = "orrery/harbour-map";
const buoy = "tidepool/buoy-feeds";
const tideCache = "tidepool/tide-cache";
const deck = "commonframe/deck-pipeline";
const headlessSession = "4be0c6d2-8f3a-4c71-9e25-ephemeris001";
// The headless session that steers tidepool/buoy-feeds, which runs two sub-agents and one shell.
const buoySession = "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55";
const workerStep = "st-rota-t2-impl";
// otherWords is the answer epic-question-other types into Something else.
const otherWords = "Vendor the Lisbon rule as a fixture file; do not touch go.mod.";
// panesList is the procedure the inbox's rows wait on: held, it leaves the inbox loading; failed, it leaves the inbox
// saying the server did not answer.
const panesList = "orrery.v1.Panes/List";
// phoneWidth is the width under which the layout is the phone's: the groups are a strip and the epic is a page.
const phoneWidth = 760;

const inbox: Address = { view: "inbox", entry: null };

// epicAt is the address of an epic's page, or of a sub-page under it, in the inbox.
function epicAt(id: string, page: PageView): Address {
  return { view: "inbox", entry: { kind: "epic", id, page } };
}

// sessionAt is the address of a headless session, with the list panel open on it when panel names one.
function sessionAt(id: string, panel?: ListPanelKind): Address {
  return { view: "inbox", entry: panel === undefined ? { kind: "session", id } : { kind: "session", id, panel } };
}

// paneAt is the address of a terminal pane on halyard.
function paneAt(pane: string): Address {
  return { view: "inbox", entry: { kind: "pane", host: "halyard", pane } };
}

// contractAt is the address of a contract in the inbox: its panel opens over its conversation, or as the page at a
// phone's width.
function contractAt(id: string): Address {
  return { view: "inbox", entry: { kind: "contract", id } };
}

// immediate is the process's own setImmediate, where there is one, taken when this module loads. A test's act holds the
// page's renders until its turn ends, and that turn is a setImmediate turn, so a round that waits for one sees them.
const immediate = (globalThis as { setImmediate?: (callback: () => void) => unknown }).setImmediate;

// macrotask resolves after one macrotask turn: a message posted on a MessageChannel is delivered after the promise jobs
// queued before it, so the effects a page runs after its draw have run too. It arms no timer.
function macrotask(): Promise<void> {
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    channel.port1.onmessage = () => {
      channel.port1.close();
      resolve();
    };
    channel.port2.postMessage(null);
  });
}

// nextRound lets the page draw what it has queued, and run the effects that follow a draw, before the story looks
// again: one setImmediate turn where the process has one, else one macrotask turn, which a stream's first frame needs.
function nextRound(): Promise<void> {
  return immediate === undefined ? macrotask() : new Promise((resolve) => immediate(resolve));
}

// whenDrawn calls act with the first element of root that matches selector and, with text, includes it, and resolves
// with what act returns. Each round lets the page draw before it looks again. act runs in the round the element is found,
// before a later draw can replace the element. A page that never draws it fails the story.
async function whenDrawn<T>(
  root: HTMLElement,
  selector: string,
  text: string,
  act: (element: HTMLElement) => T,
): Promise<T> {
  for (let round = 0; round < 50; round++) {
    const element = Array.from(root.querySelectorAll<HTMLElement>(selector)).find((candidate) =>
      (candidate.textContent ?? "").includes(text),
    );
    if (element !== undefined) {
      return act(element);
    }
    await nextRound();
  }
  throw new Error(`the page never drew ${selector}`);
}

// drawnWhen resolves with the element whenDrawn finds.
const drawnWhen = (root: HTMLElement, selector: string, text = ""): Promise<HTMLElement> =>
  whenDrawn(root, selector, text, (element) => element);

// clickWhen clicks the element whenDrawn finds.
const clickWhen = (root: HTMLElement, selector: string, text = ""): Promise<void> =>
  whenDrawn(root, selector, text, (element) => element.click());

// press sends one key press to target as the browser does: a keydown that bubbles up to the document.
function press(target: EventTarget, key: string): void {
  target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
}

// typeInto types text into the field that selector names, as a key press does: the field takes the text and an input
// event reports it to the page. The page draws the change before the story goes on, and the field is returned.
async function typeInto(root: HTMLElement, selector: string, text: string): Promise<HTMLElement> {
  const field = await whenDrawn(root, selector, "", (element) => {
    const input = element as HTMLInputElement | HTMLTextAreaElement;
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    return element;
  });
  await nextRound();
  return field;
}

// openSteer opens the steering menu of the epic page: at the desk its steering command in the title row, and under the
// phone's width the foot bar's More and then the More sheet's Steer item, which opens the menu in a sheet.
async function openSteer(root: HTMLElement): Promise<void> {
  if (window.innerWidth >= phoneWidth) {
    await clickWhen(root, "section.page .title-row .icon.steer");
    return;
  }
  await clickWhen(root, "section.page .cmdbar button", "More");
  await clickWhen(root, '[role="dialog"] button.act', "Steer it from another conversation");
}

// scrollOpenRowIntoView brings the open requirement row to the top of the epic panel, where its runs and their logs come
// under it. The row is drawn open by the time the story asks, so the scroll is the page's own call to the browser.
async function scrollOpenRowIntoView(root: HTMLElement): Promise<void> {
  const row = await drawnWhen(root, '.epic-page section.requirements button.tr[aria-expanded="true"]');
  row.scrollIntoView({ block: "start" });
}

// openRunLog opens the log of the first evidence run whose log is still closed, and lets the page draw the change.
async function openRunLog(root: HTMLElement): Promise<void> {
  await clickWhen(root, '.runs .run-line button[aria-expanded="false"]', "log");
  await nextRound();
}

// onPhone reports whether the layout is the phone's, where an epic's commands are under More and not in its title row.
const onPhone = (): boolean => window.innerWidth < phoneWidth;

// pressEpicCommand presses the epic page's command labelled label: in the page's title row at the desk, and under More
// on the phone, where the command is one of More's sheet. The inbox's card of the epic has commands of its own, so the
// page's are named by their place.
async function pressEpicCommand(root: HTMLElement, label: string): Promise<void> {
  if (onPhone()) {
    await clickWhen(root, "section.page .cmdbar button", "More");
    await clickWhen(root, '[role="dialog"] button.act', label);
    return;
  }
  await clickWhen(root, "section.page .title-row .acts button.act", label);
}

// epicScenario draws the epic at address over the slices of the scenario named.
const epicScenario = (address: Address, scenario: EpicScenario): VNode => (
  <PageStory address={address} fixture={createFixtureTransport(epicScenarios[scenario])} />
);

// The inbox's search button and its field, which the search states open and type into.
const searchButton = 'button[aria-label="Search sessions and epics"]';
const searchField = 'input[aria-label="Search sessions and epics"]';

// searchFor opens the inbox's search and types words into its field.
async function searchFor(root: HTMLElement, words: string): Promise<void> {
  await clickWhen(root, searchButton);
  await typeInto(root, searchField, words);
}

// menuOpenedOn moves the cursor down the list by j, from its top, until the row with key holds it, and then opens that
// row's More. A click on the row would open the row, so the cursor reaches it by the keyboard.
async function menuOpenedOn(root: HTMLElement, key: string): Promise<void> {
  press(document, "g");
  press(document, "g");
  const rowSelector = `[data-key="${key}"]`;
  for (let step = 0; step < 100; step++) {
    await nextRound();
    if (root.querySelector(`${rowSelector}.cur`) !== null) {
      await clickWhen(root, `${rowSelector} button.more`);
      return;
    }
    press(document, "j");
  }
  throw new Error(`the cursor never reached ${key}`);
}

// shownDockBox is the textarea of the dock that is shown: one per column, and the cursor's column is section.read.
const shownDockBox = "section.read .send > div:not([hidden]) textarea";

// warmupReply is a line of the warm-up's transcript, which is drawn once the conversation's first frame is. A dock the
// frame has not yet drawn is redrawn by it, and what was typed into the dock is lost.
const warmupReply = "The cache is warm for all 14 ports";

// typeInShownDock types text into the shown dock once the conversation's first frame is drawn, and focuses it.
async function typeInShownDock(root: HTMLElement, text: string): Promise<void> {
  await drawnWhen(root, "section.read", warmupReply);
  const box = await typeInto(root, shownDockBox, text);
  box.focus();
}

// pressInShownDock focuses the shown dock's textarea, once the conversation's first frame is drawn, and presses key in it.
async function pressInShownDock(root: HTMLElement, key: string): Promise<void> {
  await drawnWhen(root, "section.read", warmupReply);
  const box = await drawnWhen(root, shownDockBox);
  box.focus();
  press(box, key);
}

// sendInShownDock types text into the shown dock and presses Enter once the typing has drawn.
async function sendInShownDock(root: HTMLElement, text: string): Promise<void> {
  await typeInShownDock(root, text);
  await pressInShownDock(root, "Enter");
}

// dockStory draws App at address over one dock scenario's slices, and runs after, when given, once it has drawn.
const dockStory =
  (address: Address, slices: Slices, after?: (root: HTMLElement) => Promise<void>) => () => (
    <PageStory address={address} fixture={createFixtureTransport(slices)} after={after} />
  );

const warmupAt: Address = { view: "inbox", entry: { kind: "session", id: warmupSession } };
const gatesAt: Address = { view: "inbox", entry: { kind: "session", id: gatesSession } };
const pane17At: Address = { view: "inbox", entry: { kind: "pane", host: "halyard", pane: "%17" } };

// newField is New session's one field.
const newField = 'input[aria-label="New session"]';

// openNew presses N, which opens New session, and waits until its field is drawn.
async function openNew(root: HTMLElement): Promise<void> {
  press(document, "N");
  await drawnWhen(root, newField);
}

// askNew opens New session, types the words into its field and presses Enter in it, which sends them to Picker.Ask.
async function askNew(root: HTMLElement, words: string): Promise<void> {
  await openNew(root);
  press(await typeInto(root, newField, words), "Enter");
}

// fixtureWith is a fixture transport that answers Picker.Ask with the answer each words names, and serves everything
// else as the shipped slices do.
function fixtureWith(asks: [string, AskAnswer][]): FixtureTransport {
  return createFixtureTransport({ ...shippedSlices, asks: new Map(asks) });
}

// The New session words of round 6's New stories, and the rows Picker.Ask answers them with: the go-to row for pane %12,
// the tidepool resume, the start in ~/code/tidepool that starts with the words (its form's first message), and the start
// in its worktree.
const rosterWords = "roster page tidepool";
const rosterRows: PickerRowInit[] = [
  {
    token: "tok-jump",
    kind: "jump",
    dir: "/home/owner/code/tidepool",
    pane: "%12",
    label: "Why the roster page is slow past forty shifts",
    note: "A session that is waiting on you, 4 minutes old.",
  },
  {
    token: "tok-resume",
    kind: "resume",
    dir: "/home/owner/code/tidepool",
    label: "Tide table cache warm-up",
    note: "Stopped 2 days ago. Resuming picks it up where it was.",
  },
  {
    token: "tok-fresh",
    kind: "fresh",
    dir: "/home/owner/code/tidepool",
    note: "Start a new session in this directory.",
    prompt: true,
  },
  {
    token: "tok-worktree",
    kind: "pane",
    dir: "/home/owner/code/tidepool.worktrees/offline",
    note: "Start a new session in this worktree.",
  },
];

export const App: Stories = {
  inbox: () => <PageStory address={inbox} />,
  archived: () => <PageStory address={{ view: "archived", entry: null }} />,
  session: () => <PageStory address={{ view: "inbox", entry: { kind: "session", id: headlessSession } }} />,
  pane: () => <PageStory address={{ view: "inbox", entry: { kind: "pane", host: "halyard", pane: "%13" } }} />,
  step: () => <PageStory address={{ view: "inbox", entry: { kind: "step", id: workerStep } }} />,
  epic: () => <PageStory address={epicAt(rota, { kind: "epic" })} />,
  contract: () => <PageStory address={epicAt(rota, { kind: "contract" })} />,
  phase: () => <PageStory address={epicAt(rota, { kind: "phase", task: "T2", phase: "implementing" })} />,
  "worker-session": () => (
    <PageStory address={epicAt(rota, { kind: "session", task: "T2", phase: "implementing", step: workerStep })} />
  ),
};

// convoWorkingAt is the reader of pane %11, the page the New session and the key list are drawn over.
const convoWorkingAt: Address = { view: "inbox", entry: { kind: "pane", host: "halyard", pane: "%11" } };

// lighthouseAt is the page of lighthouse's project. Its key is its directory with no host, since the fixtures serve from
// halyard, the host that is left out of a key.
const lighthouseAt: Address = { view: "inbox", entry: { kind: "project", key: "|/home/owner/code/lighthouse" } };

// pressAtRest waits for the opened page's inbox to rest as its rail, then presses key on the document and lets the page
// draw what the press changed.
async function pressAtRest(root: HTMLElement, key: string): Promise<void> {
  await drawnWhen(root, '.panes[data-inbox="rail"] > .rail.start');
  press(document, key);
  await nextRound();
}

export const Page: Stories = {
  "inbox-home": () => <PageStory address={inbox} />,
  "inbox-waiting": () => (
    <PageStory address={inbox} after={(root) => clickWhen(root, ".inbox-attn .fchip.attention")} />
  ),
  "inbox-new": () => <PageStory address={inbox} after={(root) => clickWhen(root, ".inbox-attn .fchip.new")} />,
  "inbox-search": () => <PageStory address={inbox} after={(root) => searchFor(root, "tide")} />,
  "inbox-search-none": () => <PageStory address={inbox} after={(root) => searchFor(root, "harbour-pilot")} />,
  "inbox-row-menu": () => <PageStory address={inbox} after={(root) => menuOpenedOn(root, "pane:halyard:%12")} />,
  "inbox-folded": () => (
    <PageStory
      address={inbox}
      after={async (root) => {
        await clickWhen(root, 'button.fold[aria-label="Fold orrery"]');
        await clickWhen(root, 'section.dir[aria-label="tidepool"] button.fold-line');
        await nextRound();
        // At 1600 the inbox column scrolls inside itself. The ended line opened under tidepool is brought to the column's
        // top, and orrery's folded card, which sits below it, is then brought into view, so both show.
        (await drawnWhen(root, 'section.dir[aria-label="tidepool"] button.fold-line')).scrollIntoView({ block: "start" });
        (await drawnWhen(root, 'section.dir[aria-label="orrery"]')).scrollIntoView({ block: "nearest" });
      }}
    />
  ),
  "inbox-archived": () => <PageStory address={{ view: "archived", entry: null }} />,
  "inbox-loading": () => (
    <PageStory address={inbox} fixture={createFixtureTransport({ ...shippedSlices, hold: new Set([panesList]) })} />
  ),
  "inbox-empty": () => <PageStory address={inbox} fixture={createFixtureTransport(emptySlices)} />,
  "inbox-error": () => (
    <PageStory address={inbox} fixture={createFixtureTransport({ ...shippedSlices, fail: new Set([panesList]) })} />
  ),
  "epic-question": () => <PageStory address={epicAt(rota, { kind: "epic" })} />,
  "epic-shipped": () => <PageStory address={epicAt(holder, { kind: "epic" })} />,
  "convo-contract": () => (
    <PageStory address={paneAt("%16")} fixture={createFixtureTransport(contractStories.toAccept)} />
  ),
  "contract-to-accept": () => (
    <PageStory address={contractAt(tide)} fixture={createFixtureTransport(contractStories.toAccept)} />
  ),
  "contract-accepted": () => (
    <PageStory address={contractAt(tide)} fixture={createFixtureTransport(contractStories.accepted)} />
  ),
  "contract-talk": () => (
    <PageStory
      address={contractAt(tide)}
      fixture={createFixtureTransport(contractStories.toAccept)}
      after={async (root) => {
        // The row's control sets the About, and the draft it types is what the owner sends about that row.
        await clickWhen(root, 'div.tr[data-row="F2"] button.ctl');
        // A dock the conversation's first frame has not yet drawn is redrawn by it, so the draft waits for that frame.
        await drawnWhen(root, "section.read", "Why three retries and not one?");
        const box = await typeInto(root, shownDockBox, "Make it one alert per table, not per table and day.");
        box.focus();
      }}
    />
  ),
  "contract-versions": () => (
    <PageStory
      address={contractAt(tide)}
      fixture={createFixtureTransport(contractStories.toAccept)}
      after={(root) => clickWhen(root, "button.link", "versions")}
    />
  ),
  "contract-set-aside": () => (
    <PageStory address={contractAt(tide)} fixture={createFixtureTransport(contractStories.setAside)} />
  ),
  "contract-ready": () => (
    <PageStory
      address={contractAt(tide)}
      fixture={createFixtureTransport(contractStories.ready)}
      after={async (root) => {
        // The section that cannot be undone is read once it has been on screen, where the page can watch it; the page
        // then goes back to its head.
        if (globalThis.IntersectionObserver === undefined) {
          return;
        }
        await whenDrawn(root, 'section[aria-label="What leaves this machine or can’t be undone"]', "", (section) =>
          section.scrollIntoView(),
        );
        await whenDrawn(root, ".cmdbar", "Nothing is left to settle.", () => {});
        // The panel's page is what scrolls, so the title row is brought back into view there.
        await whenDrawn(root, ".rcard .title-row", "", (title) => {
          title.scrollIntoView({ block: "nearest" });
        });
      }}
    />
  ),
  "phase-implement": () => (
    <PageStory address={epicAt(rota, { kind: "phase", task: "T2", phase: "checking" })} />
  ),
  "worker-running": () => (
    <PageStory address={epicAt(rota, { kind: "session", task: "T2", phase: "implementing", step: workerStep })} />
  ),
  "epic-talk": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      after={async (root) => {
        // Under the phone's width the epic is a page, and its foot bar opens the conversation that steers it.
        if (window.innerWidth < phoneWidth) {
          await clickWhen(root, ".cmdbar button", "Talk to the session");
        }
      }}
    />
  ),
  "work-rail": () => <PageStory address={sessionAt(buoySession)} />,
  "work-rail-group": () => (
    <PageStory address={paneAt("%22")} after={(root) => clickWhen(root, "button.tile-w.group")} />
  ),
  "work-empty": () => <PageStory address={paneAt("%13")} />,
  "dock-running": () => <PageStory address={sessionAt(buoySession, "agents")} />,
  "dock-background": () => <PageStory address={sessionAt(buoySession, "running")} />,
  "epic-handover": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      fixture={createFixtureTransport(handoverSlices)}
      after={openSteer}
    />
  ),
  "epic-moved": () => (
    <PageStory address={epicAt(rota, { kind: "epic" })} fixture={createFixtureTransport(movedSlices)} />
  ),
  "epic-convo-ended": () => (
    <PageStory address={epicAt(rota, { kind: "epic" })} fixture={createFixtureTransport(endedSlices)} />
  ),
  "convo-took-over": () => <PageStory address={paneAt("%11")} fixture={createFixtureTransport(movedSlices)} />,
  "convo-handed-over": () => <PageStory address={paneAt("%14")} fixture={createFixtureTransport(movedSlices)} />,
  "epic-building": () => epicScenario(epicAt(rota, { kind: "epic" }), "building"),
  "epic-question-other": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      after={async (root) => {
        await clickWhen(root, ".opts button.opt", "Something else");
        await typeInto(root, 'textarea[aria-label="answer"]', otherWords);
      }}
    />
  ),
  "epic-parked": () => epicScenario(epicAt(notices, { kind: "epic" }), "parked"),
  "epic-planner-parked": () => epicScenario(epicAt(rota, { kind: "epic" }), "plannerParked"),
  "epic-limited": () => epicScenario(epicAt(rota, { kind: "epic" }), "limited"),
  "epic-paused": () => <PageStory address={epicAt(harbour, { kind: "epic" })} />,
  "epic-planning": () => epicScenario(epicAt(buoy, { kind: "epic" }), "planning"),
  "epic-dense": () => epicScenario(epicAt(deck, { kind: "epic" }), "dense"),
  "epic-evidence": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      fixture={createFixtureTransport(epicScenarios.evidence)}
      after={async (root) => {
        await clickWhen(root, "button.tr", "Export refuses to overwrite an existing file.");
        // The red run's log opens first, then the green run's, which folds its artifacts under it.
        await openRunLog(root);
        await openRunLog(root);
        await scrollOpenRowIntoView(root);
      }}
    />
  ),
  "epic-stop-confirm": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      fixture={createFixtureTransport(epicScenarios.building)}
      after={async (root) => {
        await pressEpicCommand(root, "Stop…");
        await drawnWhen(root, onPhone() ? '[role="dialog"]' : ".sure", "Stop rota-export?");
      }}
    />
  ),
  "epic-command-error": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      fixture={createFixtureTransport(epicScenarios.commandError)}
      after={async (root) => {
        await pressEpicCommand(root, "Pause");
        await drawnWhen(root, ".page-note", "did not land");
      }}
    />
  ),
  "epic-problem": () => epicScenario(epicAt(rota, { kind: "epic" }), "problem"),
  "epic-loading": () => epicScenario(epicAt(tideCache, { kind: "epic" }), "loading"),
  "epic-unavailable": () => epicScenario(epicAt(rota, { kind: "epic" }), "unavailable"),
  "convo-working": () => <PageStory address={convoWorkingAt} />,
  "inbox-rail": () => <PageStory address={convoWorkingAt} />,
  "inbox-flyout": () => <PageStory address={convoWorkingAt} after={(root) => pressAtRest(root, "b")} />,
  "inbox-narrow": () => <PageStory address={convoWorkingAt} after={(root) => pressAtRest(root, "B")} />,
  project: () => <PageStory address={lighthouseAt} />,
  "ask-session": () => (
    <PageStory
      address={inbox}
      after={(root) => clickWhen(root, '.list .b-row[data-key="pane:halyard:%12"]')}
    />
  ),
  "new-empty": () => <PageStory address={convoWorkingAt} after={openNew} />,
  "new-working": () => (
    <PageStory
      address={convoWorkingAt}
      fixture={fixtureWith([[rosterWords, { held: true }]])}
      after={async (root) => {
        await askNew(root, rosterWords);
        await drawnWhen(root, ".count", "asking");
      }}
    />
  ),
  "new-results": () => (
    <PageStory
      address={convoWorkingAt}
      fixture={fixtureWith([[rosterWords, { rows: rosterRows }]])}
      after={async (root) => {
        await askNew(root, rosterWords);
        await drawnWhen(root, ".pick-row");
      }}
    />
  ),
  "new-form": () => (
    <PageStory
      address={convoWorkingAt}
      fixture={fixtureWith([[rosterWords, { rows: rosterRows }]])}
      after={async (root) => {
        await askNew(root, rosterWords);
        await clickWhen(root, ".pick-row", "in this directory");
        await drawnWhen(root, 'textarea[aria-label="message"]');
      }}
    />
  ),
  "new-none": () => (
    <PageStory
      address={convoWorkingAt}
      fixture={fixtureWith([["harbour pilot", { rows: [] }]])}
      after={async (root) => {
        await askNew(root, "harbour pilot");
        await drawnWhen(root, ".inote", "Nothing found");
      }}
    />
  ),
  "new-error": () => (
    <PageStory
      address={convoWorkingAt}
      fixture={fixtureWith([[rosterWords, { failure: Code.DeadlineExceeded }]])}
      after={async (root) => {
        await askNew(root, rosterWords);
        await drawnWhen(root, ".inote.error");
      }}
    />
  ),
  "dock-idle": dockStory(warmupAt, dockSlices["dock-idle"]),
  "dock-queued": dockStory(warmupAt, dockSlices["dock-queued"]),
  "dock-draft": dockStory(warmupAt, dockSlices["dock-draft"], (root) =>
    typeInShownDock(root, "And skip ports that are already warm."),
  ),
  "dock-interrupting": dockStory(warmupAt, dockSlices["dock-interrupting"], (root) =>
    pressInShownDock(root, "Escape"),
  ),
  "dock-interrupted": dockStory(warmupAt, dockSlices["dock-interrupted"]),
  "dock-interrupt-queue": dockStory(warmupAt, dockSlices["dock-interrupt-queue"]),
  "dock-cut-off": dockStory({ view: "inbox", entry: { kind: "session", id: headlessSession } }, dockSlices["dock-cut-off"]),
  "dock-pane-queue": dockStory(pane17At, dockSlices["dock-pane-queue"]),
  "dock-waiting": dockStory(warmupAt, dockSlices["dock-waiting"]),
  "dock-failed": dockStory(warmupAt, dockSlices["dock-failed"]),
  "dock-stopped": dockStory(warmupAt, dockSlices["dock-stopped"]),
  "dock-send-error": dockStory(warmupAt, dockSlices["dock-send-error"], (root) =>
    sendInShownDock(root, "Run it for January too."),
  ),
  "dock-send-unknown": dockStory(warmupAt, dockSlices["dock-send-unknown"], (root) =>
    sendInShownDock(root, "Run it for January too."),
  ),
  "dock-cancelled": dockStory(pane17At, dockSlices["dock-cancelled"]),
  "ask-later": dockStory(gatesAt, dockSlices["ask-later"]),
  keys: () => (
    <PageStory
      address={convoWorkingAt}
      after={async (root) => {
        press(document, "?");
        await drawnWhen(root, '[role="dialog"][aria-label="Keys"]');
      }}
    />
  ),
};
