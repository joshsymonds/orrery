// The stories of Reply: round 6's Reply states, Default, a finished answer, and Live, an answer still being written, which
// ends in its caret.

import type { Stories } from "./catalog/story";
import { Reply as ReplyShape } from "./reply";

const answer = [
  "The rota export now writes **one file per week**.",
  "",
  "- the uploader retries each batch once",
  "- the export keeps `shifts.csv` as its header",
  "",
  "No other file changed.",
].join("\n");

const streaming = "The rota export now writes **one file per week**.\n\n- the uploader retries each";

export const Reply: Stories = {
  default: () => <ReplyShape text={answer} />,
  live: () => <ReplyShape text={streaming} live />,
};
