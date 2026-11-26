// The stories of HostsView: every host reachable, one under load past 100%, and one that does not answer.

import { create } from "@bufbuild/protobuf";

import { HostEntrySchema, HostStatusResponseSchema, type HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import type { Stories } from "./catalog/story";
import * as hostsview from "./hostsview";

// status is a status of invented hosts, in the order the server lists them.
function status(...hosts: { name: string; loadPercent: number; memoryPercent: number; reachable: boolean }[]): HostStatusResponse {
  return create(HostStatusResponseSchema, {
    host: "halyard",
    hosts: hosts.map((host) => create(HostEntrySchema, host)),
  });
}

export const HostsView: Stories = {
  "all-reachable": () => (
    <hostsview.HostsView
      status={status(
        { name: "halyard", loadPercent: 12.4, memoryPercent: 44.2, reachable: true },
        { name: "sextant", loadPercent: 40.1, memoryPercent: 61.8, reachable: true },
        { name: "lantern", loadPercent: 3.2, memoryPercent: 18.6, reachable: true },
      )}
    />
  ),
  "under-load": () => (
    <hostsview.HostsView
      status={status(
        { name: "halyard", loadPercent: 12.4, memoryPercent: 44.2, reachable: true },
        { name: "sextant", loadPercent: 171.5, memoryPercent: 61.8, reachable: true },
        { name: "lantern", loadPercent: 3.2, memoryPercent: 18.6, reachable: true },
      )}
    />
  ),
  "one-unreachable": () => (
    <hostsview.HostsView
      status={status(
        { name: "halyard", loadPercent: 12.4, memoryPercent: 44.2, reachable: true },
        { name: "brine", loadPercent: 0, memoryPercent: 0, reachable: false },
        { name: "lantern", loadPercent: 3.2, memoryPercent: 18.6, reachable: true },
      )}
    />
  ),
};
