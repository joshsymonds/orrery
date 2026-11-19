// The stories of WorkCard: the live card of an epic a tool call created, which EpicWidget draws, over the fixture
// transport's clients. The card reads its epic through those clients, so each state names an epic the shipped fixtures
// hold, or one they do not.

import type { Stories } from "./catalog/story";
import { EpicWidget as Card, epicCallsOf } from "./card";
import { noEpics } from "./epics";
import { createFixtureTransport } from "./fixtures/transport";

const clients = createFixtureTransport().clients;

const noop = () => {};

// calls are the epic calls the card makes over the clients, with no-op openers and none of the poll's epics, so each
// card reads its epic through the fixture.
const calls = epicCallsOf(clients, noop, noop, noEpics);

export const WorkCard: Stories = {
  // epic-building is an epic that is building, with its steps running.
  "epic-building": () => <Card epic="orrery/orrery-followups" calls={calls} />,
  // epic-asks is an epic with an open question, which the card draws under its track.
  "epic-asks": () => <Card epic="lighthouse/rota-export" calls={calls} />,
  // released is an epic that has ended with its outcome released.
  released: () => <Card epic="orrery/holder-scopes" calls={calls} />,
  // unavailable is an epic the fixtures hold no detail for, so its read fails and the card says so.
  unavailable: () => <Card epic="orrery/no-such-epic" calls={calls} />,
};
