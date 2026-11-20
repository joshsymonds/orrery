// The stories of the inbox rail's tile: round 6's Tile on the left rail, one story for each of its states.

import type { Stories } from "./catalog/story";
import { RailTile as Button } from "./inboxrail";

const noop = () => undefined;

export const Tile: Stories = {
  mark: () => <Button mark bare label="Open the inbox" tip="Inbox  b" onClick={noop} />,
  project: () => <Button tone="place" text="li" label="lighthouse" onClick={noop} />,
  "project-waiting": () => <Button tone="place" text="ti" n={3} live label="tidepool" onClick={noop} />,
  "project-new": () => <Button tone="place" text="al" fresh label="almanac" onClick={noop} />,
  current: () => <Button tone="place" text="li" cur live label="lighthouse" onClick={noop} />,
  new: () => <Button icon="plus" tone="primary" label="New session" onClick={noop} />,
};
