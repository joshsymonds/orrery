// The stories of App: the three inbox orders, and each kind of page App shows, each drawn by PageStory at an address
// over the fixture slices. Page holds the states of the live scenarios: the address that names a state draws it, and
// where none does, an after step clicks the way there.

import { options } from "preact";

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
import type { Address, ListPanelKind, PageView } from "./router";

const rota = "lighthouse/rota-export";
const tide = "tidepool/tide-alerts";
const holder = "orrery/holder-scopes";
const headlessSession = "4be0c6d2-8f3a-4c71-9e25-ephemeris001";
// The headless session that steers tidepool/buoy-feeds, which runs two sub-agents and one shell.
const buoySession = "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55";
const workerStep = "st-rota-t2-impl";
// phoneWidth is the width under which the layout is the phone's: the groups are a strip and the epic is a page.
const phoneWidth = 760;

const attention: Address = { order: "attention", entry: null };

// epicAt is the address of an epic's page, or of a sub-page under it, in the attention inbox.
function epicAt(id: string, page: PageView): Address {
  return { order: "attention", entry: { kind: "epic", id, page } };
}

// sessionAt is the address of a headless session, with the list panel open on it when panel names one.
function sessionAt(id: string, panel?: ListPanelKind): Address {
  return { order: "attention", entry: panel === undefined ? { kind: "session", id } : { kind: "session", id, panel } };
}

// paneAt is the address of a terminal pane on halyard.
function paneAt(pane: string): Address {
  return { order: "attention", entry: { kind: "pane", host: "halyard", pane } };
}

// frame waits for Preact's next after-paint flush, the one that runs the effects a render holds. A test sets
// options.requestAnimationFrame to its own flush; in a browser the flush comes a frame later, so the wait does too.
function frame(): Promise<void> {
  return new Promise((resolve) => {
    const schedule = options.requestAnimationFrame ?? ((callback: () => void) => setTimeout(callback, 16));
    schedule(() => resolve());
  });
}

// clickWhen clicks the first element of root that matches selector and, with text, includes it. The page draws a click's
// result after a flush, so each round waits one before it looks again. A page that never draws the element fails the
// story.
async function clickWhen(root: HTMLElement, selector: string, text = ""): Promise<void> {
  for (let round = 0; round < 50; round++) {
    const element = Array.from(root.querySelectorAll<HTMLElement>(selector)).find((candidate) =>
      (candidate.textContent ?? "").includes(text),
    );
    if (element !== undefined) {
      element.click();
      return;
    }
    await frame();
  }
  throw new Error(`the page never drew ${selector} to click`);
}

const openTalk = (root: HTMLElement) => clickWhen(root, "section.page .title-row .acts button.act", "Talk");

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
        // Under the phone's width the epic is a page, and its foot bar opens the conversation that steers it.
        if (window.innerWidth < phoneWidth) {
          await clickWhen(root, ".epic-foot button", "Talk to the session");
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
};
