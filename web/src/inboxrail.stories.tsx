// The stories of the inbox rail's tiles: round 6's Tile on the collapsed inbox's rail, one story for each of its states.
// The project tiles are round 7's B2c tiles: the project's whole name set sideways, its count above and its dots below.
// The lineage under a project's tile is round 8's: a pill per conversation, a pill per design, and a mark for each contract or epic.

import type { Stories } from "./catalog/story";
import { ProjectTile, RailLineage as Lineage, RailTile } from "./inboxrail";
import type { LineageConvo, RailTileModel } from "./railtiles";

const noop = () => undefined;

// tileOf is a project's tile model: quiet unless it counts, is live or is new, and titled as railTiles titles it.
function tileOf(name: string, init: Partial<RailTileModel> = {}): RailTileModel {
  const n = init.n ?? 0;
  const fresh = init.fresh ?? false;
  return {
    key: `|/home/owner/code/${name}`,
    name,
    n,
    live: false,
    fresh,
    quiet: n === 0 && !fresh,
    cur: false,
    title: n === 0 ? name : `${name}: ${n} ${n === 1 ? "waits" : "wait"} on you`,
    ...init,
  };
}

export const Tile: Stories = {
  mark: () => <RailTile mark bare label="Expand the inbox" tip="Expand the inbox  b" onClick={noop} />,
  project: () => <ProjectTile tile={tileOf("lighthouse")} onClick={noop} />,
  "project-waiting": () => <ProjectTile tile={tileOf("tidepool", { n: 3, live: true })} onClick={noop} />,
  "project-new": () => <ProjectTile tile={tileOf("almanac", { fresh: true })} onClick={noop} />,
  current: () => <ProjectTile tile={tileOf("lighthouse", { n: 1, live: true, cur: true })} onClick={noop} />,
  new: () => <RailTile icon="plus" tone="primary" label="New session" onClick={noop} />,
};

// The lineage's conversations: tidepool's session works the design Tide alerts, with an epic and a contract under it; the
// design's epic is open in the first story. twoDesigns has the session work two designs, each a stack of its own.
const tide = { key: "design:tide", name: "Tide alerts" };
const alerts = { key: "epic:tide/alerts", kind: "epic" as const, name: "alerts" };
const table = { key: "contract:tide/table", kind: "contract" as const, name: "table" };
const oneDesign: LineageConvo[] = [
  { key: "sess-tide", designs: [{ ...tide, open: false, marks: [{ ...alerts, open: false }, { ...table, open: false }] }] },
];
const openEpic: LineageConvo[] = [
  { key: "sess-tide", designs: [{ ...tide, open: false, marks: [{ ...alerts, open: true }, { ...table, open: false }] }] },
];
const twoDesigns: LineageConvo[] = [
  {
    key: "sess-tide",
    designs: [
      { ...tide, open: false, marks: [{ ...table, open: false }] },
      {
        key: "design:feed",
        name: "Harbour feed",
        open: false,
        marks: [
          { key: "epic:feed/ingest", kind: "epic", name: "ingest", open: false },
          { key: "epic:feed/publish", kind: "epic", name: "publish", open: false },
        ],
      },
    ],
  },
];

const tidepool = "|/home/owner/code/tidepool";

export const RailLineage: Stories = {
  design: () => <Lineage projectKey={tidepool} convos={oneDesign} onPress={noop} />,
  "two-designs": () => <Lineage projectKey={tidepool} convos={twoDesigns} onPress={noop} />,
  open: () => <Lineage projectKey={tidepool} convos={openEpic} onPress={noop} />,
};
