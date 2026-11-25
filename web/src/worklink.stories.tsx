// The stories of WorkLink, what a conversation is working on in its title row: one link, two links at the desk, and the
// phone's one link, with the "+n" control for the rest.

import type { Stories } from "./catalog/story";
import type { WorkLinkItem } from "./work";
import { WorkLink as Links } from "./worklink";

const noop = () => undefined;

const design: WorkLinkItem = { kind: "design", key: "design:d-round8", id: "d-round8", name: "Round 8 work links" };
const epic: WorkLinkItem = { kind: "epic", key: "epic:widgets/rail", id: "widgets/rail", name: "rail" };
const contract: WorkLinkItem = { kind: "contract", key: "contract:c-picker", id: "c-picker", name: "picker" };
const other: WorkLinkItem = { kind: "epic", key: "epic:widgets/picker", id: "widgets/picker", name: "picker-ui" };

export const WorkLink: Stories = {
  one: () => <Links items={[epic]} narrow={false} onOpen={noop} />,
  two: () => <Links items={[design, epic]} narrow={false} onOpen={noop} />,
  many: () => <Links items={[design, epic, contract, other]} narrow={false} onOpen={noop} />,
  phone: () => <Links items={[design, epic, contract]} narrow={true} onOpen={noop} />,
};
