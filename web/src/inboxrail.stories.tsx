// The stories of the inbox rail's tiles: round 6's Tile on the collapsed inbox's rail, one story for each of its states.
// The project tiles are round 7's B2c tiles: the project's whole name set sideways, its count above and its dots below.

import type { Stories } from "./catalog/story";
import { ProjectTile, RailTile } from "./inboxrail";
import type { RailTileModel } from "./railtiles";

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
