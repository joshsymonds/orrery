package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

private const val RED = "red"
private const val GREEN = "green"
private const val REVIEW = "review"
private const val ENDED = "ended"

private enum class ProofTone { OK, BAD, LIVE, NONE }

/** One proof cell of a requirement: its SPEC 7 [word] and its [tone]. */
private data class ProofCell(val word: String, val tone: ProofTone)

/**
 * The requirements of an epic as one table. Each row shows its id, what will be true, its red and green proof cells and
 * whether it is `proven` or `open`. A tap opens the row to its evidence runs, red first then green, each in created
 * order; a second tap closes it.
 */
@Composable
fun RequirementTable(requirements: List<EpicRequirementRow>, loadArtifact: ArtifactLoader) {
    var opened by remember { mutableStateOf(emptySet<String>()) }
    Column(modifier = Modifier.fillMaxWidth()) {
        requirements.forEachIndexed { index, requirement ->
            val isOpen = requirement.id in opened
            Column(modifier = Modifier.fillMaxWidth()) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("req-row:${requirement.id}")
                        .clickable {
                            opened = if (isOpen) opened - requirement.id else opened + requirement.id
                        }
                        .padding(vertical = OrreryTokens.Space.four),
                ) {
                    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                        Text(requirement.id, style = OrreryTokens.Type.body)
                        Text(requirement.text, modifier = Modifier.weight(1f), style = OrreryTokens.Type.small)
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                        ProofCellText(tag = "req-red:${requirement.id}", cell = redCellOf(requirement))
                        ProofCellText(tag = "req-green:${requirement.id}", cell = greenCellOf(requirement))
                        Text(
                            if (isProven(requirement)) "proven" else "open",
                            style = OrreryTokens.Type.small,
                            modifier = Modifier.testTag("req-proven:${requirement.id}"),
                        )
                    }
                }
                if (isOpen) {
                    EvidenceRuns(
                        requirement = index,
                        runs = orderedRunsOf(requirement),
                        loadArtifact = loadArtifact,
                        modifier = Modifier.testTag("req-runs:${requirement.id}"),
                    )
                }
            }
        }
    }
}

@Composable
private fun ProofCellText(tag: String, cell: ProofCell) {
    val mark = when (cell.tone) {
        ProofTone.OK -> "✓ "
        ProofTone.BAD -> "✗ "
        ProofTone.LIVE, ProofTone.NONE -> ""
    }
    Text(
        buildAnnotatedString { withStyle(SpanStyle(color = colourOf(cell.tone))) { append(mark + cell.word) } },
        style = OrreryTokens.Type.small,
        modifier = Modifier.testTag(tag),
    )
}

private fun colourOf(tone: ProofTone): Color = when (tone) {
    ProofTone.OK -> OrreryTokens.Role.ok
    ProofTone.BAD -> OrreryTokens.Role.bad
    ProofTone.LIVE -> OrreryTokens.Role.live
    ProofTone.NONE -> OrreryTokens.Ink.four
}

// redCellOf is the red cell from the newest red run. A review requirement with no red run reads "by review".
private fun redCellOf(requirement: EpicRequirementRow): ProofCell {
    val run = newestOf(requirement, RED)
    return when {
        run == null && requirement.kind == REVIEW -> ProofCell("by review", ProofTone.NONE)
        run == null -> ProofCell("no test yet", ProofTone.NONE)
        run.state != ENDED -> ProofCell("running", ProofTone.LIVE)
        run.passed -> ProofCell("fails at base", ProofTone.OK)
        else -> ProofCell("passes at base", ProofTone.BAD)
    }
}

// greenCellOf is the green cell from the newest green run. A review requirement with no green run reads "by review".
private fun greenCellOf(requirement: EpicRequirementRow): ProofCell {
    val run = newestOf(requirement, GREEN)
    return when {
        run == null && requirement.kind == REVIEW -> ProofCell("by review", ProofTone.NONE)
        run == null -> ProofCell("not yet", ProofTone.NONE)
        run.state != ENDED -> ProofCell("running", ProofTone.LIVE)
        run.passed -> ProofCell("passes", ProofTone.OK)
        else -> ProofCell("fails", ProofTone.BAD)
    }
}

// isProven is whether a green run of the requirement passed.
private fun isProven(requirement: EpicRequirementRow): Boolean =
    requirement.runs.any { it.phase == GREEN && it.passed }

private fun newestOf(requirement: EpicRequirementRow, phase: String) =
    requirement.runs.filter { it.phase == phase }.maxByOrNull { it.created }

// orderedRunsOf is the requirement's runs, red then green, each phase in created order.
private fun orderedRunsOf(requirement: EpicRequirementRow) =
    listOf(RED, GREEN).flatMap { phase -> requirement.runs.filter { it.phase == phase }.sortedBy { it.created } }
