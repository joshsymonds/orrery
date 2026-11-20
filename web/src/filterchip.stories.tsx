// The stories of FilterChip: the inbox head's two chips, each in the states round 6 draws them.

import type { Stories } from "./catalog/story";
import { FilterChip as FilterChipView } from "./filterchip";

const noop = () => {};

export const FilterChip: Stories = {
  off: () => <FilterChipView count={6} label="wait on you" tone="attention" on={false} onPress={noop} />,
  on: () => <FilterChipView count={6} label="wait on you" tone="attention" on={true} onPress={noop} />,
  new: () => <FilterChipView count={2} label="new" tone="new" on={false} onPress={noop} />,
};
