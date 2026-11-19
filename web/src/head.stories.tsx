// The page stories of Head: a conversation drawn at its address over the fixture slices each head state needs. Each is a
// PageStory, which draws App over those slices, so the head is what the page shows at that address.

import { PageStory } from "./catalog/pagestory";
import type { Stories } from "./catalog/story";
import { epicSlices, fullSlices, hotSlices, manySlices, warmupSession } from "./fixtures/head";
import { createFixtureTransport } from "./fixtures/transport";
import type { Address } from "./router";

const warmup: Address = { order: "attention", entry: { kind: "session", id: warmupSession } };

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
  "dock-full": () => <PageStory address={warmup} fixture={createFixtureTransport(fullSlices)} />,
};
