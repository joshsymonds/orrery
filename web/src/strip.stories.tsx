// The stories of PaneStrip: the host with its stats, a host with a context, a context alone in each band it can fall in,
// and a pane row's host with no stats. A strip with neither a host nor a context draws nothing, so no state is that.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { host } from "./fixtures/host";
import { ConversationContextSchema } from "./gen/orrery/v1/orrery_pb";
import { PaneStrip as Strip } from "./strip";

// context is a conversation's context at the given tokens, in a window of 200000.
const context = (tokens: bigint) => create(ConversationContextSchema, { tokens, window: 200000n });

export const PaneStrip: Stories = {
  "host-stats": () => <Strip host={host} />,
  "host-and-context": () => <Strip host={host} context={context(150000n)} />,
  "context-green": () => <Strip host={null} context={context(30000n)} />,
  "context-red": () => <Strip host={null} context={context(184000n)} />,
  // A pane row on another host names that host, with no stats.
  "other-host": () => <Strip host={{ host: "sextant", stats: [] }} />,
};
