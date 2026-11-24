// The stories of ContractWork: an accepted contract's work by requirement, building with its requirements in each state,
// ended with one failed, and unstarted with no epic yet.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { ContractWork as Work } from "./contractwork";
import { ContractRequirementTaskSchema, ContractRequirementWorkSchema } from "./gen/orrery/v1/contracts_pb";
import { EvidenceRunSchema } from "./gen/orrery/v1/epics_pb";

const ignore = () => {};

const epic = { id: "tidepool/tide-alerts", onOpen: ignore };

// greenRun is the green evidence run that proves a requirement: its phase and that it passed.
const greenRun = (id: string) =>
  create(EvidenceRunSchema, { id, phase: "green", state: "ended", passed: true, verdict: "holds", exit: 0 });

const task = (id: string, title: string, status: string, place: string) =>
  create(ContractRequirementTaskSchema, { id, title, status, place });

export const ContractWork: Stories = {
  building: () => (
    <Work
      epic={epic}
      work={[
        create(ContractRequirementWorkSchema, {
          id: "R1",
          text: "A changed table alerts the office within a minute.",
          state: "proven",
          tasks: [task("T1", "watch-publish", "done", "integrated")],
          proof: greenRun("E1"),
        }),
        create(ContractRequirementWorkSchema, {
          id: "R2",
          text: "An unchanged table sends nothing.",
          state: "being worked",
          tasks: [task("T2", "compare-hash", "open", "implement"), task("T3", "guard-repeat", "open", "red")],
        }),
        create(ContractRequirementWorkSchema, {
          id: "R3",
          text: "One table makes one alert.",
          state: "not started",
        }),
        create(ContractRequirementWorkSchema, {
          id: "R4",
          text: "The alert names the table’s own port.",
          state: "proven",
          tasks: [task("T4", "port-from-table", "done", "integrated")],
          proof: greenRun("E4"),
        }),
      ]}
    />
  ),
  ended: () => (
    <Work
      epic={epic}
      work={[
        create(ContractRequirementWorkSchema, {
          id: "R1",
          text: "A changed table alerts the office within a minute.",
          state: "proven",
          tasks: [task("T1", "watch-publish", "done", "integrated")],
          proof: greenRun("E1"),
        }),
        create(ContractRequirementWorkSchema, {
          id: "R2",
          text: "The alert names the table’s own port.",
          state: "failed",
          tasks: [task("T2", "port-from-table", "gap", "green")],
        }),
        create(ContractRequirementWorkSchema, {
          id: "R3",
          text: "A failed send is retried three times.",
          state: "failed",
        }),
      ]}
    />
  ),
  unstarted: () => (
    <Work
      epic={null}
      work={[
        create(ContractRequirementWorkSchema, {
          id: "R1",
          text: "A changed table alerts the office within a minute.",
          state: "not started",
        }),
        create(ContractRequirementWorkSchema, {
          id: "R2",
          text: "An unchanged table sends nothing.",
          state: "not started",
        }),
      ]}
    />
  ),
};
