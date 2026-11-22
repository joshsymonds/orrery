// The stories of WorkRail and Tile: the rail's groups, and a group of many things as one tile of dots; then one tile in
// each state it draws.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import type { MarkDetail } from "./epicrow";
import { EpicRequirementSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { EpicThing, ListThing, RailGroup, Thing } from "./work";
import { Tile as One, WorkRail as Rail } from "./workrail";

const noop = () => undefined;

function epicOf(slug: string, init: Partial<EpicThing>): EpicThing {
  return {
    kind: "epic",
    key: `epic:${slug}`,
    id: `widgets-5d0c1a7/${slug}`,
    slug,
    short: slug,
    asks: false,
    tone: "quiet",
    state: "building",
    detail: { requirements: [], centre: "" },
    ...init,
  };
}

// markOf is an epic's detail in centre, with one requirement per state in states, named R1, R2 and so on in order.
function markOf(centre: string, states: readonly string[]): MarkDetail {
  return create(EpicsGetResponseSchema, {
    centre,
    requirements: states.map((state, i) => create(EpicRequirementSchema, { id: `R${i + 1}`, state })),
  });
}

const asking = epicOf("widgets-rail", {
  short: "rail",
  asks: true,
  tone: "attention",
  state: "rail asks: which store?",
  detail: markOf("asks", ["proven", "proven", "in_flight", "not_reached", "not_reached"]),
});

const live = epicOf("orrery-picker", {
  short: "picker",
  tone: "live",
  state: "picker is building",
  detail: markOf("running", ["proven", "not_reached", "not_reached"]),
});

const agents: ListThing = { kind: "agents", key: "agents", count: 2, tone: "live", tasks: [] };
const running: ListThing = { kind: "running", key: "running", count: 1, tone: "quiet", tasks: [] };

// fiveEpics are five epics, the first of them asking and the rest quiet.
const fiveEpics: Thing[] = [
  epicOf("epic-a", { asks: true, tone: "attention", state: "epic-a asks: ship now?" }),
  epicOf("epic-b", {}),
  epicOf("epic-c", {}),
  epicOf("epic-d", {}),
  epicOf("epic-e", {}),
];

export const WorkRail: Stories = {
  groups: () => {
    const groups: RailGroup[] = [
      { kind: "epic", label: "epics", things: [asking, live], many: false },
      { kind: "agents", label: "agents", things: [agents], many: false },
      { kind: "running", label: "running", things: [running], many: false },
    ];
    return <Rail groups={groups} lit="" layout="rail" onOpen={noop} />;
  },
  many: () => {
    const groups: RailGroup[] = [{ kind: "epic", label: "epics", things: fiveEpics, many: true }];
    return <Rail groups={groups} lit="" layout="rail" onOpen={noop} />;
  },
};

export const Tile: Stories = {
  "epic-asks": () => <One thing={asking} lit={false} onOpen={noop} />,
  lit: () => <One thing={live} lit onOpen={noop} />,
  "sub-agents": () => <One thing={agents} lit={false} onOpen={noop} />,
  "left-running": () => <One thing={running} lit={false} onOpen={noop} />,
};
