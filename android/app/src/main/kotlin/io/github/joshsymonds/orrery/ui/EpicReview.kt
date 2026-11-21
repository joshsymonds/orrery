package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The Review section of an epic: its heading with its count words, one row per reviewer, then one row per ledger
 * finding with its closures by round.
 */
@Composable
internal fun EpicReviewSection(view: ReviewView) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        SectionHeading("Review")
        Text(
            view.words,
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
            modifier = Modifier.testTag("review-words"),
        )
    }
    view.reviewers.forEach { ReviewerRow(it) }
    view.findings.forEach { FindingRow(it) }
}

@Composable
private fun ReviewerRow(line: ReviewerLine) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("review-reviewer:${line.reviewer}")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(line.reviewer, style = OrreryTokens.Type.body, modifier = Modifier.weight(1f))
            Chip(line.verdict, line.tone, modifier = Modifier.testTag("review-verdict:${line.reviewer}"))
        }
        Text(line.reason, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
    }
}

@Composable
private fun FindingRow(line: FindingLine) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("review-finding:${line.id}")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(line.id, style = OrreryTokens.Type.body)
            Text(
                if (line.where.isEmpty()) line.what else "${line.what} · ${line.where}",
                modifier = Modifier.weight(1f),
                style = OrreryTokens.Type.small,
            )
            Chip(line.result, line.tone, modifier = Modifier.testTag("review-result:${line.id}"))
        }
        if (line.reason.isNotEmpty()) {
            Text(
                line.reason,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("review-reason:${line.id}"),
            )
        }
        line.closures.forEachIndexed { index, closure ->
            Chip(closure.text, closure.tone, modifier = Modifier.testTag("review-closure:${line.id}:$index"))
        }
    }
}
