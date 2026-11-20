// The stories of ContractBlock: round 6's two states, a contract waiting to be read and one accepted.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { ContractBlock as Block } from "./contractblock";
import { at } from "./fixtures/clock";
import { ContractSchema } from "./gen/orrery/v1/contracts_pb";

const ignore = () => {};

export const ContractBlock: Stories = {
  waiting: () => (
    <Block
      id="tidepool/tide-alerts"
      contract={create(ContractSchema, {
        id: "tidepool/tide-alerts",
        repository: "tidepool",
        title: "The harbour office hears within a minute when a published tide table changes",
        care: "severe",
        state: "proposed",
        version: 2n,
        openDecisions: 1n,
        conversation: "session-tide",
        created: at("@14:02"),
        changed: at("@14:15"),
      })}
      onOpen={ignore}
    />
  ),
  accepted: () => (
    <Block
      id="harbour/shift-export"
      contract={create(ContractSchema, {
        id: "harbour/shift-export",
        repository: "harbour",
        title: "A keeper’s week of shifts becomes a file for the harbour office",
        care: "serious",
        state: "accepted",
        version: 1n,
        openDecisions: 0n,
        conversation: "session-shifts",
        created: at("@12:40"),
        changed: at("@13:50"),
      })}
      onOpen={ignore}
    />
  ),
};
