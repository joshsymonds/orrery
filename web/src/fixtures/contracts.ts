// The contracts the fixture slices hold: the Contracts.List answer, one contract, proposed by the conversation that wrote it.

import { create } from "@bufbuild/protobuf";

import { ContractsListResponseSchema } from "../gen/orrery/v1/contracts_pb";
import { at } from "./clock";

// contracts is the Contracts.List answer: tide-alerts, proposed by sess-tide-16, the conversation that wrote it.
export const contracts = create(ContractsListResponseSchema, {
  contracts: [
    {
      id: "tidepool/tide-alerts", repository: "/home/owner/code/tidepool", title: "Tide alerts for the harbour office",
      care: "serious", state: "proposed", version: 2n, openDecisions: 0n, conversation: "sess-tide-16",
      created: at("@13:58"), changed: at("@14:15"),
    },
  ],
});
