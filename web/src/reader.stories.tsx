// The stories of the reader's head: ReaderHead draws the head over a conversation, titled, or with where.

import type { Stories } from "./catalog/story";
import * as reader from "./reader";
import { TitleParts } from "./titleparts";

export const ReaderHead: Stories = {
  titled: () => (
    <reader.ReaderHead
      label={<TitleParts dir="/home/owner/code/lighthouse" line="Retry backoff and jitter for the telemetry uploader" />}
      where=""
    />
  ),
  where: () => <reader.ReaderHead label="rota-export" where="lighthouse › T2 · implementing" />,
};
