// The page stories of Head: a conversation drawn at its address over the fixture slices each head state needs. Each is a
// PageStory, which draws App over those slices, so the head is what the page shows at that address. Where no address
// names the state, an after step makes the owner's way there.

import { Code, ConnectError } from "@connectrpc/connect";

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
import { epicSlices, fullSlices, hotSlices, manySlices, scrolledSlices, warmupSession } from "./fixtures/head";
import { createFixtureTransport, type FixtureTransport } from "./fixtures/transport";
import { pagingQuery } from "./reader";
import type { Address } from "./router";

const warmup: Address = { order: "attention", entry: { kind: "session", id: warmupSession } };

// whenDrawn resolves with the first element of root that matches selector and, with text, includes it, once the page
// draws one. A stream's answer reaches the page after the stories' own turns, so the wait is on the page's mutations
// rather than on a count of rounds, and a page that never draws the element leaves the wait open.
function whenDrawn(root: HTMLElement, selector: string, text = ""): Promise<HTMLElement> {
  const find = () =>
    Array.from(root.querySelectorAll<HTMLElement>(selector)).find((candidate) => (candidate.textContent ?? "").includes(text));
  return new Promise((resolve) => {
    const drawn = find();
    if (drawn !== undefined) {
      resolve(drawn);
      return;
    }
    const observer = new window.MutationObserver(() => {
      const element = find();
      if (element !== undefined) {
        observer.disconnect();
        resolve(element);
      }
    });
    observer.observe(root, { subtree: true, childList: true, characterData: true });
  });
}

// clickWhen clicks the element of root that whenDrawn finds.
async function clickWhen(root: HTMLElement, selector: string, text = ""): Promise<void> {
  (await whenDrawn(root, selector, text)).click();
}

// scrollToTop puts the cursor's reader at its top and reports the scroll, as the owner's scroll does. Under pagingQuery
// the reader does not scroll on its own, so the page scrolls to its top and reports the window's scroll instead. The
// reader's transcript is drawn once its stream has answered, and the reader is the item's parent that scrolls.
async function scrollToTop(root: HTMLElement): Promise<void> {
  const reader = (await whenDrawn(root, 'section[aria-label="reader"] .reader .t')).closest<HTMLElement>(".reader");
  if (reader === null) {
    throw new Error("the transcript's item is not drawn in the cursor's reader");
  }
  if (window.matchMedia(pagingQuery).matches) {
    reader.scrollTop = 0;
    reader.dispatchEvent(new Event("scroll"));
  } else {
    window.scrollTo(0, 0);
    window.dispatchEvent(new Event("scroll"));
  }
}

// unansweredArchive is the fixture transport with its archive refused by a server that does not answer: the More menu's
// archive fails with the server not answering, and changes nothing.
function unansweredArchive(): FixtureTransport {
  const fixture = createFixtureTransport();
  const archive = () => Promise.reject(new ConnectError("the server did not answer", Code.Unavailable));
  return { ...fixture, clients: { ...fixture.clients, epics: { ...fixture.clients.epics, archive } } };
}

export const Page: Stories = {
  "convo-hot": () => <PageStory address={warmup} fixture={createFixtureTransport(hotSlices)} />,
  "convo-other-host": () => (
    <PageStory address={{ order: "attention", entry: { kind: "pane", host: "brine", pane: "%24" } }} />
  ),
  "convo-epic": () => (
    <PageStory
      address={{ order: "attention", entry: { kind: "pane", host: "halyard", pane: "%14" } }}
      fixture={createFixtureTransport(epicSlices)}
    />
  ),
  "convo-many-epics": () => (
    <PageStory
      address={{ order: "attention", entry: { kind: "pane", host: "halyard", pane: "%22" } }}
      fixture={createFixtureTransport(manySlices)}
    />
  ),
  "convo-command-error": () => (
    <PageStory
      address={warmup}
      fixture={unansweredArchive()}
      after={async (root) => {
        await clickWhen(root, 'section[aria-label="reader"] .icon.more');
        await clickWhen(root, 'section[aria-label="reader"] .head-menu [role="menuitem"]', "Archive");
      }}
    />
  ),
  "dock-full": () => <PageStory address={warmup} fixture={createFixtureTransport(fullSlices)} />,
  "phone-head-scrolled": () => (
    <PageStory address={warmup} fixture={createFixtureTransport(scrolledSlices)} after={scrollToTop} />
  ),
  "phone-head-hidden": () => (
    <PageStory
      address={warmup}
      fixture={createFixtureTransport(fullSlices)}
      after={(root) => clickWhen(root, 'section[aria-label="reader"] button.fold-head')}
    />
  ),
};
