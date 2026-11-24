package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.ContractRequirementTaskRow
import io.github.joshsymonds.orrery.api.ContractRequirementWorkRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The tag of the work's root node, the section of a contract that shows its epic's work (O.contractWork in contract.js). */
internal const val CONTRACT_WORK_TAG = "contract-work"

/** The tag prefix of one requirement's row in the work table, followed by the requirement's id (O.contractWork in contract.js). */
internal const val WORK_ROW_TAG_PREFIX = "work-row:"

private const val PROVEN = "proven"
private const val BEING_WORKED = "being worked"
private const val FAILED = "failed"
private const val OPEN_EPIC = "open the epic"
private const val NO_TASK = "no task yet"
private const val RED = "red"
private const val GREEN = "green"
private const val ENDED = "ended"
private const val RUNNING = "running"

/**
 * The work on a contract once it is accepted, seen from the contract's side (O.contractWork in contract.js): the work's head,
 * how many of its requirements are proven and the epic's name with "open the epic", which calls [onOpenEpic], then a row per
 * requirement in [work] order with its state, the tasks working it, and its red and green proof. [runs] are the evidence runs
 * of each requirement by its id. The root node carries [CONTRACT_WORK_TAG].
 */
@Composable
internal fun ContractWork(
    work: List<ContractRequirementWorkRow>,
    runs: Map<String, List<EvidenceRunRow>>,
    epic: String,
    onOpenEpic: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val proven = work.count { it.state == PROVEN }
    Column(modifier = modifier.fillMaxWidth().testTag(CONTRACT_WORK_TAG)) {
        SectionHead("The work", count = "$proven of ${work.size} requirements proven")
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onOpenEpic)
                .padding(vertical = OrreryTokens.Space.four),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(epic, modifier = Modifier.weight(1f), style = OrreryTokens.Type.body)
            Text(OPEN_EPIC, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        }
        work.forEach { row -> WorkRow(row, runs[row.id].orEmpty()) }
    }
}

@Composable
private fun WorkRow(row: ContractRequirementWorkRow, runs: List<EvidenceRunRow>) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(WORK_ROW_TAG_PREFIX + row.id)
            .padding(vertical = OrreryTokens.Space.four),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(row.id, style = OrreryTokens.Type.body)
            Text(row.text, modifier = Modifier.weight(1f), style = OrreryTokens.Type.small)
            Chip(
                row.state,
                toneOfState(row.state),
                modifier = Modifier.testTag("work-state:${row.id}"),
            )
        }
        TaskList(row.id, row.tasks)
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            ProofText("work-red:${row.id}", redProofOf(runs))
            ProofText("work-green:${row.id}", greenProofOf(runs))
        }
    }
}

/**
 * The tone of a state's chip, carried in its semantics. Proven is ok, being worked is live and failed is bad; not started,
 * and any state the server does not name, is neutral.
 */
private fun toneOfState(state: String): ChipTone = when (state) {
    PROVEN -> ChipTone.Ok
    BEING_WORKED -> ChipTone.Live
    FAILED -> ChipTone.Bad
    else -> ChipTone.Neutral
}

/** The tasks working a requirement, each with its title and status, or "no task yet" when none works it. */
@Composable
private fun TaskList(requirement: String, tasks: List<ContractRequirementTaskRow>) {
    if (tasks.isEmpty()) {
        Text(NO_TASK, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.four)
        return
    }
    Column {
        tasks.forEach { task ->
            Row(
                modifier = Modifier.testTag("work-task:$requirement:${task.id}"),
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(task.title, style = OrreryTokens.Type.small)
                Text(task.status, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            }
        }
    }
}

/** One proof cell's word and its tone, which the cell carries in its semantics. */
private data class ProofWord(val word: String, val tone: ChipTone)

/** The newest of [runs] in [phase], by creation time, or null when there is none. */
private fun newestOf(runs: List<EvidenceRunRow>, phase: String): EvidenceRunRow? =
    runs.filter { it.phase == phase }.maxByOrNull { it.created }

/**
 * The red proof from the newest red run: running while it has not ended, "fails at base" when it passed, else "passes at
 * base". "no test yet" when no red run exists.
 */
private fun redProofOf(runs: List<EvidenceRunRow>): ProofWord {
    val run = newestOf(runs, RED) ?: return ProofWord("no test yet", ChipTone.Neutral)
    return when {
        run.state != ENDED -> ProofWord(RUNNING, ChipTone.Live)
        run.passed -> ProofWord("fails at base", ChipTone.Ok)
        else -> ProofWord("passes at base", ChipTone.Bad)
    }
}

/**
 * The green proof from the newest green run: running while it has not ended, "passes" when it passed, else "fails". "not yet"
 * when no green run exists.
 */
private fun greenProofOf(runs: List<EvidenceRunRow>): ProofWord {
    val run = newestOf(runs, GREEN) ?: return ProofWord("not yet", ChipTone.Neutral)
    return when {
        run.state != ENDED -> ProofWord(RUNNING, ChipTone.Live)
        run.passed -> ProofWord("passes", ChipTone.Ok)
        else -> ProofWord("fails", ChipTone.Bad)
    }
}

@Composable
private fun ProofText(tag: String, proof: ProofWord) {
    Text(
        proof.word,
        modifier = Modifier.testTag(tag).semantics { set(ChipToneKey, proof.tone) },
        color = proofInk(proof.tone),
        style = OrreryTokens.Type.small,
    )
}

private fun proofInk(tone: ChipTone): Color = when (tone) {
    ChipTone.Ok -> OrreryTokens.Role.ok
    ChipTone.Bad -> OrreryTokens.Role.bad
    ChipTone.Live -> OrreryTokens.Role.live
    else -> OrreryTokens.Ink.four
}
