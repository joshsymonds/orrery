// The stories of App: the three inbox orders, and each kind of page App shows, each drawn by PageStory at an address
// over the fixture slices. Page holds the states of the live scenarios: the address that names a state draws it, and
// where none does, an after step clicks the way there.

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
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

// clickWhen clicks the first element of root that matches selector and, with text, includes it. The page draws a click's
// result in a microtask, so each round lets one run before it looks again. A page that never draws the element fails the
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
    await Promise.resolve();
  }
  throw new Error(`the page never drew ${selector} to click`);
}

const openTalk = (root: HTMLElement) => clickWhen(root, "section.page .title-row .acts button.act", "Talk");

export const App: Stories = {
  attention: () => <PageStory address={attention} />,
  project: () => <PageStory address={{ order: "project", entry: null }} />,
  archived: () => <PageStory address={{ order: "archived", entry: null }} />,
  session: () => <PageStory address={{ order: "attention", entry: { kind: "session", id: headlessSession } }} />,
  pane: () => <PageStory address={{ order: "attention", entry: { kind: "pane", host: "sextant", pane: "%13" } }} />,
  step: () => <PageStory address={{ order: "attention", entry: { kind: "step", id: workerStep } }} />,
  epic: () => <PageStory address={epicAt(rota, { kind: "epic" })} />,
  contract: () => <PageStory address={epicAt(rota, { kind: "contract" })} />,
  phase: () => <PageStory address={epicAt(rota, { kind: "phase", task: "T2", phase: "implementing" })} />,
  "worker-session": () => (
    <PageStory address={epicAt(rota, { kind: "session", task: "T2", phase: "implementing", step: workerStep })} />
  ),
};

export const Page: Stories = {
  "list-home": () => <PageStory address={attention} />,
  "epic-rota-decision": () => <PageStory address={epicAt(rota, { kind: "epic" })} />,
  "epic-holder-released": () => <PageStory address={epicAt(holder, { kind: "epic" })} />,
  "contract-tidealerts-open": () => <PageStory address={epicAt(tide, { kind: "contract" })} />,
  "contract-rota-accepted": () => <PageStory address={epicAt(rota, { kind: "contract" })} />,
  "phase-rota-export-cli-checking": () => (
    <PageStory address={epicAt(rota, { kind: "phase", task: "T2", phase: "checking" })} />
  ),
  "worker-rota-export-cli-implementer": () => (
    <PageStory address={epicAt(rota, { kind: "session", task: "T2", phase: "implementing", step: workerStep })} />
  ),
  "talk-rota-rotaplan-docked": () => (
    <PageStory
      address={epicAt(rota, { kind: "epic" })}
      after={async (root) => {
        await openTalk(root);
        await clickWhen(root, "ul.talk li:first-child button");
      }}
    />
  ),
  "talk-rota-menu": () => <PageStory address={epicAt(rota, { kind: "epic" })} after={openTalk} />,
  "session-lamp-working": () => (
    <PageStory address={{ order: "attention", entry: { kind: "pane", host: "halyard", pane: "%11" } }} />
  ),
  "session-beacon-question": () => (
    <PageStory
      address={attention}
      after={(root) => clickWhen(root, '.list .b-row[data-key="pane:halyard:%12"]')}
    />
  ),
};
