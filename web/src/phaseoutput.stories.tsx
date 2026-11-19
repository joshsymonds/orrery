// The stories of PhaseOutputPage: a task's phase output over the rota-export epic's Get answer, drawn at each phase. Its
// Hold and Stop… calls go to the fixture's Steps, which absorbs them, and its evidence reads go to Evidence, which serves
// them, so no state makes a call the fixture refuses.

import type { JSX } from "preact";

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { PhaseOutputPage as Page } from "./phaseoutput";
import type { Phase } from "./phases";
import { stepCalls } from "./readercolumn";

const rotaEpic = "lighthouse/rota-export";
const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// rota is the Get answer of the rota-export epic, which the fixture holds.
function rota(): EpicsGetResponse {
  const detail = gets.get(rotaEpic);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no Get answer for ${rotaEpic}`);
  }
  return detail;
}

// phasePage draws the phase output of task at phase, over the rota-export epic.
function phasePage(task: string, phase: Phase): JSX.Element {
  const { clients } = createFixtureTransport();
  return (
    <Page
      detail={rota()}
      epic="rota-export"
      task={task}
      phase={phase}
      now={now}
      loadArtifact={(run, name, stderr) => clients.evidence.artifact({ run, name, stderr })}
      onBack={noop}
      onOpenPhase={noop}
      onOpenSession={noop}
      hold={(step) => stepCalls(clients, step).hold()}
      stop={(step) => stepCalls(clients, step).stop()}
    />
  );
}

export const PhaseOutputPage: Stories = {
  // T2's step is running, so Hold and Stop… show, and its red run and attempts draw.
  implementing: () => phasePage("T2", "implementing"),
  // T1's step is held, so Stop… shows and Hold does not.
  "implementing-held": () => phasePage("T1", "implementing"),
  // T2's checks ran two rounds, the first with a failed gate.
  checking: () => phasePage("T2", "checking"),
  // T2's review holds one finding.
  reviewing: () => phasePage("T2", "reviewing"),
  // T2 is not yet integrated, so the phase says it waits to integrate.
  "integrating-waiting": () => phasePage("T2", "integrating"),
  // T4 is done, so the phase says it is integrated.
  "integrating-integrated": () => phasePage("T4", "integrating"),
};
