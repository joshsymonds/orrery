// The stories of App: the three inbox orders, and each kind of page App shows, each drawn by PageStory at an address
// over the fixture slices. Page holds the states of the live scenarios: the address that names a state draws it, and
// where none does, an after step clicks the way there.

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
import { dockSlices, gatesSession, warmupSession } from "./fixtures/dock";
import type { Slices } from "./fixtures/slices";
import { createFixtureTransport } from "./fixtures/transport";
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

// macrotask resolves after one macrotask turn: a message posted on a MessageChannel is delivered after the promise jobs
// queued before it and after the conversation's first frame, which a microtask does not reach. It arms no timer.
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

// whenDrawn calls use with the first element of root that matches selector and, with text, includes it. Each round lets
// one macrotask turn run before it looks again. A page that never draws the element fails the story.
async function whenDrawn<T extends HTMLElement>(
  root: HTMLElement,
  selector: string,
  text: string,
  use: (element: T) => void,
): Promise<void> {
  for (let round = 0; round < 50; round++) {
    const element = Array.from(root.querySelectorAll<T>(selector)).find((candidate) =>
      (candidate.textContent ?? "").includes(text),
    );
    if (element !== undefined) {
      use(element);
      return;
    }
    await macrotask();
  }
  throw new Error(`the page never drew ${selector}`);
}

// clickWhen clicks the first element that whenDrawn finds.
const clickWhen = (root: HTMLElement, selector: string, text = "") =>
  whenDrawn(root, selector, text, (element) => element.click());

const openTalk = (root: HTMLElement) => clickWhen(root, "section.page .title-row .acts button.act", "Talk");

// shownDockBox is the textarea of the dock that is shown: one per column, and the cursor's column is section.read.
const shownDockBox = "section.read .send > div:not([hidden]) textarea";

// warmupReply is a line of the warm-up's transcript, which is drawn once the conversation's first frame is. A dock the
// frame has not yet drawn is redrawn by it, and what was typed into the dock is lost.
const warmupReply = "The cache is warm for all 14 ports";

// typeInShownDock sets the shown dock's textarea to text as typing does, and focuses it.
async function typeInShownDock(root: HTMLElement, text: string): Promise<void> {
  await whenDrawn(root, "section.read", warmupReply, () => {});
  await whenDrawn<HTMLTextAreaElement>(root, shownDockBox, "", (box) => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
    box.focus();
  });
}

// pressInShownDock focuses the shown dock's textarea and presses key in it.
async function pressInShownDock(root: HTMLElement, key: string): Promise<void> {
  await whenDrawn(root, "section.read", warmupReply, () => {});
  await whenDrawn<HTMLTextAreaElement>(root, shownDockBox, "", (box) => {
    box.focus();
    box.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

// sendInShownDock types text into the shown dock and presses Enter once the typing has drawn.
async function sendInShownDock(root: HTMLElement, text: string): Promise<void> {
  await typeInShownDock(root, text);
  await macrotask();
  await pressInShownDock(root, "Enter");
}

// dockStory draws App at address over one dock scenario's slices, and runs after, when given, once it has drawn.
const dockStory =
  (address: Address, slices: Slices, after?: (root: HTMLElement) => Promise<void>) => () => (
    <PageStory address={address} fixture={createFixtureTransport(slices)} after={after} />
  );

const warmupAt: Address = { order: "attention", entry: { kind: "session", id: warmupSession } };
const gatesAt: Address = { order: "attention", entry: { kind: "session", id: gatesSession } };
const pane17At: Address = { order: "attention", entry: { kind: "pane", host: "halyard", pane: "%17" } };

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
  "convo-working": () => (
    <PageStory address={{ order: "attention", entry: { kind: "pane", host: "halyard", pane: "%11" } }} />
  ),
  "ask-session": () => (
    <PageStory
      address={attention}
      after={(root) => clickWhen(root, '.list .b-row[data-key="pane:halyard:%12"]')}
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
  "dock-cut-off": dockStory({ order: "attention", entry: { kind: "session", id: headlessSession } }, dockSlices["dock-cut-off"]),
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
};
