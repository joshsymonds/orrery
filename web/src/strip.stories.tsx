// The stories of MeterStrip and ContextMeter, drawn by PaneStrip and ContextMeter: the host alone, the host with a context,
// the host with a context at crit, a context alone in each band it can fall in, a context bare of its name and tokens, and
// a pane row's host on another host. The strip draws no meters, so no story draws one. A strip with neither a host nor a
// context draws nothing, so no state is that.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { host } from "./fixtures/host";
import { ConversationContextSchema } from "./gen/orrery/v1/orrery_pb";
import { ContextMeter as Meter, PaneStrip as Strip } from "./strip";

// context is a conversation's context at the given tokens, in a window of 200000.
const context = (tokens: bigint) => create(ConversationContextSchema, { tokens, window: 200000n });

export const MeterStrip: Stories = {
  "host-only": () => <Strip host={host} />,
  "host-and-context": () => <Strip host={host} context={context(150000n)} />,
  "under-load": () => <Strip host={host} context={context(174000n)} />,
  // A session on another host names that host, and nothing else.
  "other-host": () => <Strip host={{ host: "brine" }} />,
};

export const ContextMeter: Stories = {
  ok: () => <Meter context={context(30000n)} />,
  warn: () => <Meter context={context(140000n)} />,
  crit: () => <Meter context={context(184000n)} />,
  bare: () => <Meter context={context(140000n)} bare />,
};
