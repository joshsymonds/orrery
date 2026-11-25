-- +goose Up
-- Every stored epic summary is cleared. The summary call now reads only an epic's title, intent and approach, so the
-- summaries written from the whole contract are written again: each epic with an empty summary gets a new one at the
-- next start, or on its next wake (see EpicSummaries). SetEpicSummary still writes only while a summary is empty.
UPDATE epics SET summary = '';

-- +goose Down
-- The cleared summaries are fixed forward, not restored: the Down changes nothing, and the summaries the Up cleared
-- stay empty until the summary loop writes them again.
