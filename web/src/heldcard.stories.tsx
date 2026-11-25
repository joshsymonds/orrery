// The stories of HeldCard and HeldSheet: an invented design, contract and epic held open in their conversation, as the desk's
// card draws each, and an invented epic as a narrow layout's sheet draws it. Every name, key and count here is invented.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import type { MarkDetail } from "./epicrow";
import { EpicRequirementSchema } from "./gen/orrery/v1/epics_pb";
import type { HeldThing } from "./held";
import { HeldCard as Card, HeldSheet as Sheet } from "./heldcard";

const noop = () => undefined;

const design: HeldThing = { kind: "design", key: "design:invented-design", convo: "convo:invented-conversation" };
const contract: HeldThing = { kind: "contract", key: "contract:invented-contract", convo: "convo:invented-conversation" };
const epic: HeldThing = { kind: "epic", key: "epic:invented-epic", convo: "convo:invented-conversation" };

// epicMark is the detail the invented epic's mark reads: two of four requirements proven, one in flight, running.
const epicMark: MarkDetail = {
  centre: "running",
  requirements: [
    create(EpicRequirementSchema, { id: "R1", state: "proven" }),
    create(EpicRequirementSchema, { id: "R2", state: "proven" }),
    create(EpicRequirementSchema, { id: "R3", state: "in_flight" }),
    create(EpicRequirementSchema, { id: "R4", state: "not_reached" }),
  ],
};

export const HeldCard: Stories = {
  design: () => (
    <Card thing={design} title="Invented design" move="open" mark={null} onFoldBack={noop}>
      <p>an invented design's record</p>
    </Card>
  ),
  contract: () => (
    <Card thing={contract} title="Invented contract" move="open" mark={null} onFoldBack={noop}>
      <p>an invented contract's record</p>
    </Card>
  ),
  epic: () => (
    <Card thing={epic} title="Invented epic" move="open" mark={epicMark} onFoldBack={noop}>
      <p>an invented epic's record</p>
    </Card>
  ),
};

export const HeldSheet: Stories = {
  open: () => (
    <Sheet thing={epic} title="Invented epic" move="open" mark={epicMark} onClose={noop}>
      <p>an invented epic's record</p>
    </Sheet>
  ),
};
