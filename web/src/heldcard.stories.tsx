// The stories of HeldCard and HeldSheet: an invented design, contract and epic held open in their conversation, as the desk's
// card draws each, and an invented epic as a narrow layout's sheet draws it. Every name, key and count here is invented.

import type { Stories } from "./catalog/story";
import type { GaugeShare } from "./epicrow";
import type { HeldThing } from "./held";
import { HeldCard as Card, HeldSheet as Sheet } from "./heldcard";

const noop = () => undefined;

const design: HeldThing = { kind: "design", key: "design:invented-design", convo: "convo:invented-conversation" };
const contract: HeldThing = { kind: "contract", key: "contract:invented-contract", convo: "convo:invented-conversation" };
const epic: HeldThing = { kind: "epic", key: "epic:invented-epic", convo: "convo:invented-conversation" };

// epicGauge is the invented epic's share: two of four tasks done, live, with its dot.
const epicGauge: GaugeShare = { done: 2, total: 4, tone: "live", inner: "dot" };

export const HeldCard: Stories = {
  design: () => (
    <Card thing={design} title="Invented design" move="open" gauge={null} onFoldBack={noop}>
      <p>an invented design's record</p>
    </Card>
  ),
  contract: () => (
    <Card thing={contract} title="Invented contract" move="open" gauge={null} onFoldBack={noop}>
      <p>an invented contract's record</p>
    </Card>
  ),
  epic: () => (
    <Card thing={epic} title="Invented epic" move="open" gauge={epicGauge} onFoldBack={noop}>
      <p>an invented epic's record</p>
    </Card>
  ),
};

export const HeldSheet: Stories = {
  open: () => (
    <Sheet thing={epic} title="Invented epic" move="open" gauge={epicGauge} onClose={noop}>
      <p>an invented epic's record</p>
    </Sheet>
  ),
};
