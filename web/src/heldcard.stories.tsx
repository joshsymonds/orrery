// The stories of HeldCard and HeldSheet: an invented design, contract and epic held open in their conversation, as the desk's
// card draws each, and an invented epic as a narrow layout's sheet draws it. Every name and key here is invented.

import type { Stories } from "./catalog/story";
import type { HeldThing } from "./held";
import { HeldCard as Card, HeldSheet as Sheet } from "./heldcard";

const noop = () => undefined;

const design: HeldThing = { kind: "design", key: "design:invented-design", convo: "convo:invented-conversation" };
const contract: HeldThing = { kind: "contract", key: "contract:invented-contract", convo: "convo:invented-conversation" };
const epic: HeldThing = { kind: "epic", key: "epic:invented-epic", convo: "convo:invented-conversation" };

export const HeldCard: Stories = {
  design: () => (
    <Card thing={design} title="Invented design" move="open" onFoldBack={noop}>
      <p>an invented design's record</p>
    </Card>
  ),
  contract: () => (
    <Card thing={contract} title="Invented contract" move="open" onFoldBack={noop}>
      <p>an invented contract's record</p>
    </Card>
  ),
  epic: () => (
    <Card thing={epic} title="Invented epic" move="open" onFoldBack={noop}>
      <p>an invented epic's record</p>
    </Card>
  ),
};

export const HeldSheet: Stories = {
  open: () => (
    <Sheet thing={epic} title="Invented epic" move="open" onClose={noop}>
      <p>an invented epic's record</p>
    </Sheet>
  ),
};
