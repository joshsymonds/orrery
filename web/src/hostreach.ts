// The reach of each host the server lists: whether its agent answered. A host the status does not list is not known to be
// down, and reads as reachable where a view asks only whether it is down.

import type { HostStatusResponse } from "./gen/orrery/v1/orrery_pb";

// HostReach is each listed host's name, mapped to whether it answered.
export type HostReach = ReadonlyMap<string, boolean>;

// reachOf maps each host of status to its reachable flag. A null status lists no host.
export function reachOf(status: HostStatusResponse | null): HostReach {
  return new Map((status?.hosts ?? []).map((host) => [host.name, host.reachable]));
}

// hostDown reports whether host is listed with reachable false. An unlisted host, an empty name and an empty map are not
// down.
export function hostDown(reach: HostReach, host: string): boolean {
  return host !== "" && reach.get(host) === false;
}

// reachableHosts is the names of the hosts that answered, in the order the status lists them.
export function reachableHosts(status: HostStatusResponse | null): string[] {
  return (status?.hosts ?? []).filter((host) => host.reachable).map((host) => host.name);
}
