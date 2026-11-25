// The stories of DesignCard, the body of a design held at the desk: an invented design with its state and its contracts in
// position order, and an invented design with no contracts yet. Every name, state and count here is invented.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { DesignCard as Card } from "./designcard";
import { DesignSchema } from "./gen/orrery/v1/designs_pb";

const noop = () => undefined;

// withContracts is the invented design, which its contracts give in the position order given.
const withContracts = create(DesignSchema, {
  id: "invented-design",
  title: "Invented design",
  conversation: "invented-conversation",
  created: 10n,
  state: "One contract accepted, two proposed",
  contracts: [
    { id: "invented/first", title: "first", repository: "invented", state: "accepted", position: 1n },
    { id: "invented/second", title: "second", repository: "invented", state: "proposed", position: 2n, after: ["invented/first"] },
    {
      id: "invented/third",
      title: "third",
      repository: "invented",
      state: "proposed",
      position: 3n,
      after: ["invented/second"],
    },
  ],
});

// withoutContracts is the invented design before any contract is proposed.
const withoutContracts = create(DesignSchema, {
  id: "invented-empty",
  title: "Invented empty design",
  conversation: "invented-conversation",
  created: 20n,
  state: "No contracts yet",
  contracts: [],
});

export const DesignCard: Stories = {
  contracts: () => <Card design={withContracts} onOpenContract={noop} />,
  none: () => <Card design={withoutContracts} onOpenContract={noop} />,
};
