// The stories of MeterStrip and ContextMeter, drawn by PaneStrip and ContextMeter: the host with its stats, the host under
// load, a host with a context, a context alone in each band it can fall in, a context bare of its name and tokens, and a
// pane row's host with no stats. A strip with neither a host nor a context draws nothing, so no state is that.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { host } from "./fixtures/host";
import { ConversationContextSchema, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import { ContextMeter as Meter, PaneStrip as Strip } from "./strip";

// context is a conversation's context at the given tokens, in a window of 200000.
const context = (tokens: bigint) => create(ConversationContextSchema, { tokens, window: 200000n });

// hot is the host under load: its CPU at warn and its RAM at crit.
const hot = create(HostStatusResponseSchema, {
  host: "halyard",
  stats: [
    { label: "cpu", value: "71%", percent: 71, level: "warn" },
    { label: "ram", value: "30G", percent: 94, level: "crit" },
    { label: "disk", value: "49%", percent: 49, level: "ok" },
    { label: "net", value: "↓1M ↑298K", level: "ok" },
  ],
});

export const MeterStrip: Stories = {
  "host-only": () => <Strip host={host} />,
  "host-and-context": () => <Strip host={host} context={context(150000n)} />,
  "under-load": () => <Strip host={hot} context={context(174000n)} />,
  "context-green": () => <Strip host={null} context={context(30000n)} />,
  "context-red": () => <Strip host={null} context={context(184000n)} />,
  // A session on another host names that host and says it has no meters.
  "other-host": () => <Strip host={{ host: "brine", stats: [], other: true }} />,
};

export const ContextMeter: Stories = {
  ok: () => <Meter context={context(30000n)} />,
  warn: () => <Meter context={context(140000n)} />,
  crit: () => <Meter context={context(184000n)} />,
  bare: () => <Meter context={context(140000n)} bare />,
};
