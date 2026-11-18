// The stories of EpicWidget: the live card of an epic a tool call created, over the fixture transport's clients. The
// card reads its epic through those clients, so each state names an epic the shipped fixtures hold, or one they do not.

import type { Stories } from "./catalog/story";
import { EpicWidget as Card, epicCallsOf } from "./card";
import { createFixtureTransport } from "./fixtures/transport";

const clients = createFixtureTransport().clients;

const noop = () => {};

// calls are the epic calls the card makes over the clients, with no-op openers and the first poll tick.
const calls = epicCallsOf(clients, noop, noop, 0);

export const EpicWidget: Stories = {
  // working is an epic that is building, with its steps running.
  working: () => <Card epic="orrery/orrery-followups" calls={calls} />,
  // question is an epic with an open question, which the card draws under its track.
  question: () => <Card epic="lighthouse/rota-export" calls={calls} />,
  // released is an epic that has ended with its outcome released.
  released: () => <Card epic="orrery/holder-scopes" calls={calls} />,
  // unavailable is an epic the fixtures hold no detail for, so its read fails and the card says so.
  unavailable: () => <Card epic="orrery/no-such-epic" calls={calls} />,
};
