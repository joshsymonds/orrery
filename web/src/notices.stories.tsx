// The stories of Notice, as round 6 draws them: a stack row asking you, an epic asking, an epic shipped, a failure, and
// a row of the centre. Each head is written out here as noticeHead writes it for that notice.

import { create } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { EpicRequirementSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { Notice as Row } from "./notices";

const noop = () => {};

// markOf is an epic's detail in centre, with one requirement per state in states, named R1, R2 and so on in order.
function markOf(centre: string, states: readonly string[]) {
  return create(EpicsGetResponseSchema, {
    centre,
    requirements: states.map((state, i) => create(EpicRequirementSchema, { id: `R${i + 1}`, state })),
  });
}

export const Notice: Stories = {
  "asks-you": () => (
    <Row
      head={{ lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" }}
      when="2m"
      body="Which fix should I make?"
      act="Answer"
      onAct={noop}
      onRead={noop}
    />
  ),
  "epic-asks": () => (
    <Row
      head={{
        lead: { kind: "mark", detail: markOf("asks", ["proven", "proven", "in_flight", "not_reached", "not_reached"]) },
        dir: "",
        what: "rota-export asks you a question",
      }}
      when="14m"
      body="Bump the embedded tz database?"
      act="Answer"
      onAct={noop}
      onRead={noop}
    />
  ),
  shipped: () => (
    <Row
      head={{
        lead: { kind: "mark", detail: markOf("shipped", ["proven", "proven", "proven", "proven", "proven"]) },
        dir: "",
        what: "tide-alerts finished",
      }}
      when="1h"
      body="epic integrated"
      act="Open"
      onAct={noop}
      onRead={noop}
    />
  ),
  failed: () => (
    <Row
      head={{ lead: { kind: "glyph", state: "failed" }, dir: "halyard", what: "failed" }}
      when="3h"
      body="the release build exited 1"
      act="Open"
      onAct={noop}
      onRead={noop}
    />
  ),
  "in-the-centre": () => (
    <Row
      head={{ lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" }}
      when="14:31"
      body="Which fix should I make?"
      flat
      unread
      onClick={noop}
    />
  ),
};
