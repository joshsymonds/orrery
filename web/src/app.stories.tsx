// The stories of App: the three inbox orders, and each kind of page App shows, each drawn by PageStory at an address
// over the fixture slices. Page holds the states of the live scenarios: the address that names a state draws it, and
// where none does, an after step clicks the way there.

import { Code } from "@connectrpc/connect";

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
import { type AskAnswer, type PickerRowInit, shippedSlices } from "./fixtures/slices";
import { createFixtureTransport, type FixtureTransport } from "./fixtures/transport";
import type { Address, PageView } from "./router";

const rota = "lighthouse/rota-export";
const tide = "tidepool/tide-alerts";
const holder = "orrery/holder-scopes";
const headlessSession = "4be0c6d2-8f3a-4c71-9e25-ephemeris001";
const workerStep = "st-rota-t2-impl";

const attention: Address = { order: "attention", entry: null };

// epicAt is the address of an epic's page, or of a sub-page under it, in the attention inbox.
function epicAt(id: string, page: PageView): Address {
  return { order: "attention", entry: { kind: "epic", id, page } };
}

// immediate is the process's own setImmediate, where there is one, taken when this module loads. A test's act holds the
// page's renders until its turn ends, and that turn is a setImmediate turn, so a round that waits for one sees them.
const immediate = (globalThis as { setImmediate?: (callback: () => void) => unknown }).setImmediate;

// nextRound lets the page draw what it has queued before the story looks again. A browser draws in a microtask, which
// is all the round needs there.
function nextRound(): Promise<void> {
  return immediate === undefined ? Promise.resolve() : new Promise((resolve) => immediate(resolve));
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

const openTalk = (root: HTMLElement) => clickWhen(root, "section.page .title-row .acts button.act", "Talk");

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
  attention: () => <PageStory address={attention} />,
  project: () => <PageStory address={{ order: "project", entry: null }} />,
  archived: () => <PageStory address={{ order: "archived", entry: null }} />,
  session: () => <PageStory address={{ order: "attention", entry: { kind: "session", id: headlessSession } }} />,
  pane: () => <PageStory address={{ order: "attention", entry: { kind: "pane", host: "halyard", pane: "%13" } }} />,
  step: () => <PageStory address={{ order: "attention", entry: { kind: "step", id: workerStep } }} />,
  epic: () => <PageStory address={epicAt(rota, { kind: "epic" })} />,
  contract: () => <PageStory address={epicAt(rota, { kind: "contract" })} />,
  phase: () => <PageStory address={epicAt(rota, { kind: "phase", task: "T2", phase: "implementing" })} />,
  "worker-session": () => (
    <PageStory address={epicAt(rota, { kind: "session", task: "T2", phase: "implementing", step: workerStep })} />
  ),
};

// convoWorkingAt is the reader of pane %11, the page the New session and the key list are drawn over.
const convoWorkingAt: Address = { order: "attention", entry: { kind: "pane", host: "halyard", pane: "%11" } };

export const Page: Stories = {
  "inbox-home": () => <PageStory address={attention} />,
  "epic-question": () => <PageStory address={epicAt(rota, { kind: "epic" })} />,
  "epic-shipped": () => <PageStory address={epicAt(holder, { kind: "epic" })} />,
  "contract-to-accept": () => <PageStory address={epicAt(tide, { kind: "contract" })} />,
  "contract-accepted": () => <PageStory address={epicAt(rota, { kind: "contract" })} />,
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
        await openTalk(root);
        await clickWhen(root, "ul.talk li:first-child button");
      }}
    />
  ),
  "epic-handover": () => <PageStory address={epicAt(rota, { kind: "epic" })} after={openTalk} />,
  "convo-working": () => <PageStory address={convoWorkingAt} />,
  "ask-session": () => (
    <PageStory
      address={attention}
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
