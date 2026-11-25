// The stories of UsageReadout: a session's list total in the title row with the Usage panel shut, and the same readout lit
// while the panel is open, as the title row draws it beside the context and Find.

import type { JSX } from "preact";

import type { Stories } from "./catalog/story";
import { sessionTotals } from "./fixtures/usage";
import { TitleRow } from "./titlerow";
import * as usagereadout from "./usagereadout";

const noop = () => {};

// titleRow draws a readout in a title row's commands, as the conversation's head does.
function titleRow(readout: JSX.Element) {
  return <TitleRow crumbs={[{ label: "api" }]}>{readout}</TitleRow>;
}

export const UsageReadout: Stories = {
  shut: () => titleRow(<usagereadout.UsageReadout totals={sessionTotals} open={false} onOpen={noop} />),
  lit: () => titleRow(<usagereadout.UsageReadout totals={sessionTotals} open={true} onOpen={noop} />),
};
