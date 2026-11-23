// The stories of RouteSeats: the table over the fixture's invented registry, which holds a forward, a sigv4 and two inject
// seats, and over a registry with no seats, which draws the head and no rows.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { registry } from "./fixtures/accounts";
import { RouteSeats as RouteSeatsView } from "./routeseats";

export const RouteSeats: Stories = {
  default: () => <RouteSeatsView registry={registry} />,
  "no-seats": () => <RouteSeatsView registry={create(RouteRegistrySchema)} />,
};
