// The stories of EpicPage: the epic page of the fixture epics, each drawn with the page's clock, no-op commands and
// openers, and the evidence reads of the fixture transport. Each state names an epic the fixtures hold, or a change of
// one.

import type { Stories } from "./catalog/story";
import { type EpicCommands, EpicPage as Page } from "./epicpage";
import type { LoadArtifact } from "./evidence";
import { liveEpoch } from "./fixtures/clock";
import { gets, histories } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import type { EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { Handover } from "./handover";

const rota = "lighthouse/rota-export";
const holder = "orrery/holder-scopes";
const tide = "tidepool/tide-alerts";
const offline = "tidepool/offline-tables";
const followups = "orrery/orrery-followups";

const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

const clients = createFixtureTransport().clients;

// loadArtifact reads a run's log or artifact over the fixture clients, as the app's page does.
const loadArtifact: LoadArtifact = (run, name, stderr) => clients.evidence.artifact({ run, name, stderr });

const commands: EpicCommands = {
  pause: noop,
  resume: noop,
  stop: noop,
  retry: noop,
  answer: () => Promise.resolve(""),
};

// detailOf is the Epics.Get answer of an epic the fixtures hold.
function detailOf(id: string): EpicsGetResponse {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

// historyOf is the history of an epic, which is empty for an epic the fixtures hold none for.
function historyOf(id: string): EpicHistoryEntry[] {
  return histories.get(id)?.entries ?? [];
}

// shared are the props every state gives the page: its clock, its commands, its reads and the openers the app passes.
const shared = {
  now,
  loadArtifact,
  commands,
  numberKeys: true,
  onOpenContract: noop,
  onOpenSession: noop,
  onBack: noop,
  onOpenPhase: noop,
};

export const EpicPage: Stories = {
  // rota-decision is an epic with an open question, whose page draws the question in its now block.
  "rota-decision": () => <Page {...shared} detail={detailOf(rota)} history={historyOf(rota)} />,
  // holder-released is a released epic, with its release proof reached, its report and its handover.
  "holder-released": () => (
    <Page
      {...shared}
      detail={detailOf(holder)}
      history={historyOf(holder)}
      handover={<Handover detail={detailOf(holder)} />}
    />
  ),
  // tide-alerts-contract is an epic that has written its contract and built nothing, so its later sections are dim.
  "tide-alerts-contract": () => <Page {...shared} detail={detailOf(tide)} history={historyOf(tide)} />,
  // offline-stopped is a stopped epic whose release has a failed step, with that step's reason.
  "offline-stopped": () => <Page {...shared} detail={detailOf(offline)} history={historyOf(offline)} />,
  // followups-paused is a building epic that is paused, so its page offers Resume.
  "followups-paused": () => (
    <Page {...shared} detail={{ ...detailOf(followups), paused: true }} history={historyOf(followups)} />
  ),
};
