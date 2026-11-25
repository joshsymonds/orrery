// The stories of UsagePanel: a session whose kinds are in dollars, a session whose kinds are in tokens alone because its
// kind costs do not sum to its list cost, and a session on a subscription, billed nothing. Each draws inside the side
// panel frame it opens in, with its head, as round 8 section E draws it.

import type { Stories } from "./catalog/story";
import { sessionTotals, subscriptionTotals, unsummedTotals } from "./fixtures/usage";
import { formatAddress } from "./router";
import { SidePanel } from "./sidepanel";
import { UsagePanel as Panel } from "./usagepanel";

// inbox is the address the panel's Open in a new window link names.
const inbox = formatAddress({ view: "inbox", entry: null });

export const UsagePanel: Stories = {
  "kinds-dollars": () => (
    <SidePanel label="Usage" tone="quiet" href={inbox} onClose={() => undefined}>
      <Panel totals={sessionTotals} />
    </SidePanel>
  ),
  "kinds-tokens": () => (
    <SidePanel label="Usage" tone="quiet" href={inbox} onClose={() => undefined}>
      <Panel totals={unsummedTotals} />
    </SidePanel>
  ),
  subscription: () => (
    <SidePanel label="Usage" tone="quiet" href={inbox} onClose={() => undefined}>
      <Panel totals={subscriptionTotals} />
    </SidePanel>
  ),
};
