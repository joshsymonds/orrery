package io.github.joshsymonds.orrery.ui

// EpicUsageSection is an epic's Usage section, as web/src/epicusage.tsx draws it: each session of the epic as a row, largest
// list cost first and named as the epic names it, with the model of its largest total, its summed requests, cache write
// tokens and list cost, and a bar of its list cost against the largest row's. The steerer's row is marked shared · not in
// total, and the header's list and billed sums leave it out. Under narrow, a phone's, a row has no bar. The section draws
// nothing without a session.

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicUsageSession
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.util.Locale

/**
 * The Usage section of an epic: its header's label and sums, then one row per session of [sessions], its rows and sums
 * built by [epicUsageFigures] from the epic's [steps] and [requirements]. [narrow] draws the rows without their bars. Draws
 * nothing when [sessions] is empty.
 */
@Composable
fun EpicUsageSection(
    sessions: List<EpicUsageSession>,
    steps: List<StepRow>,
    requirements: List<EpicRequirementRow>,
    narrow: Boolean,
) {
    val figures = remember(sessions, steps, requirements) { epicUsageFigures(sessions, steps, requirements) }
    if (figures.rows.isEmpty()) return
    val largest = figures.rows.maxOf { it.list }
    Column(modifier = Modifier.fillMaxWidth().testTag("epic-usage")) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            SectionHeading("Usage")
            Text(
                "${dollars(figures.list)} at list · billed ${dollars(figures.billed)}",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
        }
        figures.rows.forEach { row -> UsageRowLine(row = row, largest = largest, narrow = narrow) }
    }
}

// UsageRowLine is one session of the epic: its name, with its shared mark under it, then its model and figures, then its bar
// unless the row is narrow.
@Composable
private fun UsageRowLine(row: EpicUsageRow, largest: Long, narrow: Boolean) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("epic-usage-row")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Column(modifier = Modifier.weight(1.6f)) {
            Text(
                row.name,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.one,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.testTag("epic-usage-who"),
            )
            if (row.note.isNotEmpty()) {
                Text(
                    row.note,
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.four,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.testTag("epic-usage-shared"),
                )
            }
        }
        Text(
            row.model,
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.two,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1.2f).testTag("epic-usage-model"),
        )
        Text(
            "%,d".format(Locale.US, row.requests),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.two,
            textAlign = TextAlign.End,
            maxLines = 1,
            modifier = Modifier.weight(0.8f).testTag("epic-usage-requests"),
        )
        Text(
            tokens(row.cacheWriteTokens),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.two,
            textAlign = TextAlign.End,
            maxLines = 1,
            modifier = Modifier.weight(0.8f).testTag("epic-usage-cache"),
        )
        Text(
            dollars(row.list),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.one,
            textAlign = TextAlign.End,
            maxLines = 1,
            modifier = Modifier.weight(0.9f).testTag("epic-usage-list"),
        )
        if (!narrow) {
            UsageBar(
                fraction = if (largest == 0L) 0f else row.list.toFloat() / largest.toFloat(),
                modifier = Modifier.weight(1.4f),
            )
        }
    }
}

// UsageBar is a track with its fill drawn at fraction of the track's width, from 0 to 1.
@Composable
private fun UsageBar(fraction: Float, modifier: Modifier) {
    Box(
        modifier = modifier
            .height(OrreryTokens.Size.meterBarHeight)
            .testTag("epic-usage-bar")
            .background(OrreryTokens.Surface.raised),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth(fraction)
                .height(OrreryTokens.Size.meterBarHeight)
                .testTag("epic-usage-bar-fill")
                .background(OrreryTokens.Role.primary),
        )
    }
}
