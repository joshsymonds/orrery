// The stories of HeldCard and HeldSheet: an invented epic held open in its conversation, as the desk's card draws it and as a
// narrow layout's sheet draws it. Every name and key here is invented.

import type { Stories } from "./catalog/story";
import type { HeldThing } from "./held";
import { HeldCard as Card, HeldSheet as Sheet } from "./heldcard";

const noop = () => undefined;

const epic: HeldThing = { kind: "epic", key: "epic:invented-epic", convo: "convo:invented-conversation" };

export const HeldCard: Stories = {
  open: () => (
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
