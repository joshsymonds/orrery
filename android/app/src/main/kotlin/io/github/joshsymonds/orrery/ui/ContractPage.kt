package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.ContractDecisionRow
import io.github.joshsymonds.orrery.api.ContractLengthRow
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractRefusalRow
import io.github.joshsymonds.orrery.api.ContractRowWriteRow
import io.github.joshsymonds.orrery.api.ContractStageRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.EpicAdditionRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicFailureRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.RecordMakerRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The tag of a contract page's root node (O.contractPage and O.contractBody in contract.js). */
internal const val CONTRACT_PAGE_TAG = "contract-page"

/**
 * The tag prefix of a section of the contract page, followed by the section's name, in the document's order (the ten
 * sections of O.contractBody in contract.js).
 */
internal const val CONTRACT_SECTION_TAG_PREFIX = "contract-section:"

/** The tag of the contract's length meter, drawn above its checks (O.lengthMeter in design.js). */
internal const val CONTRACT_LENGTH_TAG = "contract-length"

/** The tag of the decision line, the title, level of care, decisions needed, length and versions (O.dline in contract.js). */
internal const val CONTRACT_DLINE_TAG = "contract-dline"

/**
 * The tag prefix of a failure, requirement, premise or decision row, followed by the row's id (the rows of O.contractBody
 * in contract.js).
 */
internal const val CONTRACT_ROW_TAG_PREFIX = "contract-row:"

/**
 * The tag prefix of a row of the body's additions, followed by the addition's id, A<n> counted from 1 (the additions of
 * O.contractBody in contract.js).
 */
internal const val ADDITION_ROW_TAG_PREFIX = "contract-addition:"

/**
 * The tag prefix of an addition the body no longer holds, drawn from its strike write, followed by the addition's id
 * (the struck rows of O.contractBody in contract.js).
 */
internal const val ADDITION_STRUCK_TAG_PREFIX = "contract-struck:"

/** The tag prefix of an addition's Keep control, followed by the addition's id (the Keep of O.contractBody in contract.js). */
internal const val ADDITION_KEEP_TAG_PREFIX = "addition-keep:"

/** The tag prefix of an addition's Strike control, followed by the addition's id (the Strike of O.contractBody in contract.js). */
internal const val ADDITION_STRIKE_TAG_PREFIX = "addition-strike:"

/**
 * The tag prefix of a step's "none" undo mark, followed by the step's position, counted from 1 as the step is numbered, on
 * a step that cannot be undone (the undo column of O.contractBody in contract.js).
 */
internal const val STEP_UNDO_NONE_TAG_PREFIX = "step-undo-none:"

/** The tag prefix of an alternative's row, followed by its index counted from 0 (the approach table of O.contractBody in contract.js). */
internal const val CONTRACT_ALTERNATIVE_TAG_PREFIX = "contract-alternative:"

/** The tag prefix of a step's row, followed by the step's position, counted from 1 (the steps of O.contractBody in contract.js). */
internal const val CONTRACT_STEP_TAG_PREFIX = "contract-step:"

/** The tag prefix of a decision's row, followed by the decision's id, D<n> (the decisions of O.contractBody in contract.js). */
internal const val CONTRACT_DECISION_TAG_PREFIX = "contract-decision:"

/**
 * The tag prefix of one option of a decision, followed by the decision's id, a colon and the option's index counted from 0,
 * which carries a "recommended" chip when it is the recommendation.
 */
internal const val CONTRACT_OPTION_TAG_PREFIX = "contract-option:"

/** The tag prefix of a row's Talk control, followed by the row's id (the rowCtl of O.contractBody in contract.js). */
internal const val CONTRACT_TALK_TAG_PREFIX = "contract-talk:"

/** The tag prefix of a "Refused by the checks" line, followed by the row or section id it refused (O.contractBody in contract.js). */
internal const val CONTRACT_REFUSAL_TAG_PREFIX = "contract-refused:"

/**
 * The tag prefix of a maker's words, "rated by", "kept by" or "struck by" a maker, followed by the row's id. The node carries
 * the maker as [MakerKey] (O.who and O.by in design.js).
 */
internal const val CONTRACT_WHO_TAG_PREFIX = "contract-who:"

/** How long a contract is, as the length signal words it: ok below the warning, warn from it, and crit from the strong count. */
internal enum class LengthLevel { OK, WARN, CRIT }

/** Semantics key: the level of the length signal, on its text node (O.lengthLevel in design.js). */
internal val LengthLevelKey = SemanticsPropertyKey<LengthLevel>("LengthLevel")

/** The body the page draws: a version's contract, its requirements and decisions, and the rows that version changed. */
private class DrawnBody(
    val version: Long,
    val contract: EpicContractRow,
    val requirements: List<EpicRequirementRow>,
    val decisions: List<ContractDecisionRow>,
    val changedRows: Set<String>,
)

/**
 * A contract's page, drawn in its design's pane, the record round 8 draws for a contract (O.contractPage and O.contractBody in
 * contract.js): the decision line, the work once accepted, and the ten sections of its document in order, each row with its
 * controls and its refusals. [record] is the contract and its versions and states, and [stage] what it is being written
 * with. The body drawn is the stage's working copy when there is one, else the newest version. [questions] are the design's
 * questions that concern it, and [thread] draws one of them. [onKeep] and [onStrike] take an addition's id, and [onShown]
 * reports the version and the undo-none rows the owner was shown. [onAbout], when given, opens a row's About, and is drawn
 * only while the contract is proposed. [work] draws the work once the contract is accepted and [foot] the accept bar, both
 * when given. The page decides while the contract is proposed and is frozen once it is accepted or set aside. The root node
 * carries [CONTRACT_PAGE_TAG].
 */
@Composable
internal fun ContractPage(
    record: ContractRecordRow,
    stage: ContractStageRow,
    questions: List<DesignQuestionRow>,
    onKeep: (String) -> Unit,
    onStrike: (String) -> Unit,
    onShown: (Long, List<String>) -> Unit,
    onAbout: ((RowAbout) -> Unit)?,
    thread: @Composable (DesignQuestionRow) -> Unit,
    work: (@Composable () -> Unit)?,
    foot: @Composable () -> Unit,
    modifier: Modifier = Modifier,
) {
    val body = drawnBodyOf(record, stage)
    val deciding = record.contract.state == "proposed"
    val needed = if (deciding) {
        questions.count { it.answer.isEmpty() } + (body?.decisions?.count { it.answer.isEmpty() } ?: 0)
    } else {
        0
    }
    Column(
        modifier = modifier.fillMaxWidth().testTag(CONTRACT_PAGE_TAG).verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
    ) {
        DecisionLine(record, stage, body, needed)
        work?.invoke()
        if (body != null) {
            val talk = if (deciding) onAbout else null
            IntentSection(body, stage)
            FailuresSection(body, stage, talk)
            AdditionsSection(body, stage, deciding, onKeep, onStrike, talk)
            RequirementsSection(body, stage, talk)
            PremisesSection(body, stage, talk)
            ExclusionsSection(body, stage)
            ApproachSection(body, stage)
            StepsSection(body, stage, deciding, onShown)
            DecisionsSection(body, stage, questions, needed, thread)
            ChecksSection(body, stage)
        }
        foot()
    }
}

/** The body the page draws: the stage's working copy when there is one, else the newest version, or null when there is neither. */
private fun drawnBodyOf(record: ContractRecordRow, stage: ContractStageRow): DrawnBody? {
    stage.working?.let { working ->
        return DrawnBody(working.version, working.contract, working.requirements, working.decisions, working.changedRows.toSet())
    }
    val newest = newestVersion(record) ?: return null
    return DrawnBody(newest.version, newest.contract, newest.requirements, newest.decisions, newest.changedRows.toSet())
}

/** The latest write of each row, of every action or of [action] alone, by row id. */
private fun latestWrites(writes: List<ContractRowWriteRow>, action: String? = null): Map<String, ContractRowWriteRow> =
    writes.filter { action == null || it.action == action }.sortedBy { it.seq }.associateBy { it.row }

/** A count as a section head shows it: its number, or nothing when there are none. */
private fun countLabel(count: Int): String = if (count == 0) "" else "$count"

/** A level of care's tone: limited is plain, serious is live, severe is bad. */
private fun careTone(rating: String): ChipTone = when (rating) {
    "serious" -> ChipTone.Live
    "severe" -> ChipTone.Bad
    else -> ChipTone.Neutral
}

/** Who a maker is in the words round 8 gives it (MAKER in design.js). */
private fun whoName(who: String): String = when (who) {
    "you" -> "you"
    "director" -> "the Director"
    else -> "orreryd"
}

/** The decision line: the title, the level of care, the decisions needed, the length and the versions (O.dline in contract.js). */
@Composable
private fun DecisionLine(record: ContractRecordRow, stage: ContractStageRow, body: DrawnBody?, needed: Int) {
    val title = body?.contract?.title ?: record.contract.title
    val care = body?.contract?.care ?: record.contract.care
    val versions = record.versions.size
    Column(
        modifier = Modifier.fillMaxWidth().testTag(CONTRACT_DLINE_TAG),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Text(title, style = OrreryTokens.Type.display)
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text("Level of care", style = OrreryTokens.Type.small)
            Chip(care, careTone(care))
            Text(
                if (needed == 0) "Decisions needed: none" else "Decisions needed: $needed",
                style = OrreryTokens.Type.small,
                color = if (needed == 0) OrreryTokens.Ink.one else OrreryTokens.Role.attention,
            )
        }
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            stage.length?.let { LengthSignal(it) }
            Text(
                if (versions == 1) "1 version" else "$versions versions",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
        }
    }
}

/** The length signal: the contract's lines, with its level's words, which are never a refusal. */
@Composable
private fun LengthSignal(length: ContractLengthRow) {
    val level = when {
        length.lines >= length.strong -> LengthLevel.CRIT
        length.lines >= length.warn -> LengthLevel.WARN
        else -> LengthLevel.OK
    }
    val words = when (level) {
        LengthLevel.CRIT -> " · very long"
        LengthLevel.WARN -> " · long"
        LengthLevel.OK -> ""
    }
    val color = when (level) {
        LengthLevel.CRIT -> OrreryTokens.Role.bad
        LengthLevel.WARN -> OrreryTokens.Role.attention
        LengthLevel.OK -> OrreryTokens.Ink.three
    }
    Text(
        "${length.lines} lines$words",
        modifier = Modifier.testTag(CONTRACT_LENGTH_TAG).semantics {
            set(LengthLevelKey, level)
            contentDescription = "${length.lines} lines of rows and prose above the checks. " +
                "Long from ${length.warn}, very long from ${length.strong}; never refused."
        },
        style = OrreryTokens.Type.small,
        color = color,
    )
}

/** A section of the document: its name and count under the tag of its key, then its content. */
@Composable
private fun PageSection(key: String, name: String, count: String = "", content: @Composable () -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().testTag(CONTRACT_SECTION_TAG_PREFIX + key),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        SectionHead(name, count = count)
        content()
    }
}

/** A row of a table, drawn under [tag]. */
@Composable
private fun PageRow(tag: String, content: @Composable () -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(vertical = OrreryTokens.Space.four).testTag(tag),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
    ) {
        content()
    }
}

/** The chip a row wears when the version changed it. */
@Composable
private fun ChangedIn(changed: Boolean, version: Long) {
    if (changed) Chip("changed in v$version", ChipTone.New)
}

/** The "Refused by the checks" lines of [field], a row's id or a section's key, in the order the checks refused them. */
@Composable
private fun Refusals(refusals: List<ContractRefusalRow>, field: String) {
    refusals.filter { it.field == field }.forEach { refusal ->
        InlineNote(
            "Refused by the checks: ${refusal.text.removePrefix("${refusal.field}: ")}",
            NoteTone.Error,
            Modifier.testTag(CONTRACT_REFUSAL_TAG_PREFIX + field),
        )
    }
}

/** Who made a record, as a line of words with the maker as [MakerKey]: "rated by you", "kept by the Director". */
@Composable
private fun WhoTag(verb: String, recordMaker: RecordMakerRow?, tag: String) {
    val who = whoOf(recordMaker)
    Text(
        "$verb by ${whoName(who)}",
        modifier = Modifier.testTag(tag).semantics { maker = who },
        style = OrreryTokens.Type.small,
        color = if (who == "director") OrreryTokens.Role.engine else OrreryTokens.Ink.two,
    )
}

/** A row's Talk control, which puts the row on the next message as what it is about; drawn only when [onAbout] is given. */
@Composable
private fun RowTalk(about: RowAbout, onAbout: ((RowAbout) -> Unit)?) {
    if (onAbout == null) return
    Canvas(
        modifier = Modifier
            .size(OrreryTokens.Size.glyph)
            .testTag(CONTRACT_TALK_TAG_PREFIX + about.id)
            .clickable { onAbout(about) }
            .semantics { contentDescription = "Talk about this row" },
    ) {
        drawRoundRect(
            color = OrreryTokens.Ink.two,
            cornerRadius = CornerRadius(OrreryTokens.Radius.one.toPx()),
            style = Stroke(width = OrreryTokens.Size.hairline.toPx()),
        )
    }
}

/**
 * Reports the undo-none rows the owner was shown, once: [onShown] is called with the version and [rows] when the section
 * is composed with rows, and again only when the rows change.
 */
@Composable
private fun ReportShown(version: Long, rows: List<String>, onShown: (Long, List<String>) -> Unit) {
    val report by rememberUpdatedState(onShown)
    LaunchedEffect(version, rows) {
        if (rows.isNotEmpty()) report(version, rows)
    }
}

@Composable
private fun IntentSection(body: DrawnBody, stage: ContractStageRow) {
    PageSection("intent", "What you asked for") {
        Text(body.contract.intent, style = OrreryTokens.Type.body)
        Refusals(stage.refusals, "intent")
    }
}

@Composable
private fun FailuresSection(body: DrawnBody, stage: ContractStageRow, onAbout: ((RowAbout) -> Unit)?) {
    val writes = latestWrites(stage.writes)
    val failures = body.contract.failures
    val setter: EpicFailureRow? = failures.firstOrNull { it.rating == body.contract.care }
    val careLine = if (setter == null) {
        "Level of care: ${body.contract.care}"
    } else {
        "Level of care: ${body.contract.care}, set by ${setter.id}"
    }
    PageSection("failures", "What could go wrong, and how much we care", count = countLabel(failures.size)) {
        failures.forEach { failure ->
            PageRow(CONTRACT_ROW_TAG_PREFIX + failure.id) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    IdChip(failure.id)
                    Text(failure.text, style = OrreryTokens.Type.body)
                    Chip(failure.rating, careTone(failure.rating))
                    ChangedIn(failure.id in body.changedRows, body.version)
                }
                Text(failure.response, style = OrreryTokens.Type.small)
                writes[failure.id]?.let { WhoTag("rated", it.maker, CONTRACT_WHO_TAG_PREFIX + failure.id) }
                RowTalk(RowAbout(failure.id, failure.text), onAbout)
                Refusals(stage.refusals, failure.id)
            }
        }
        Text(careLine, style = OrreryTokens.Type.small)
        Refusals(stage.refusals, "failures")
    }
}

@Composable
private fun AdditionsSection(
    body: DrawnBody,
    stage: ContractStageRow,
    deciding: Boolean,
    onKeep: (String) -> Unit,
    onStrike: (String) -> Unit,
    onAbout: ((RowAbout) -> Unit)?,
) {
    val keeps = latestWrites(stage.writes, "keep")
    val strikes = latestWrites(stage.writes, "strike")
    val held: List<Pair<String, EpicAdditionRow>> = body.contract.additions.mapIndexed { index, addition ->
        "A${index + 1}" to addition
    }
    val heldIds = held.map { it.first }.toSet()
    val struck = strikes.values.filter { it.row !in heldIds }.sortedBy { it.seq }
    PageSection("additions", "Things you did not ask for", count = countLabel(held.size)) {
        if (held.isEmpty() && struck.isEmpty()) {
            Text("Nothing was added. Every row traces to something you said.", style = OrreryTokens.Type.small)
        }
        held.forEach { (id, addition) ->
            val keep = keeps[id]
            PageRow(ADDITION_ROW_TAG_PREFIX + id) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    IdChip(id)
                    Text(addition.what, style = OrreryTokens.Type.body)
                    ChangedIn(id in body.changedRows, body.version)
                }
                Text("at ${addition.location}", style = OrreryTokens.Type.small)
                Text(addition.why, style = OrreryTokens.Type.small)
                Text(addition.cost, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                if (keep != null) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                    ) {
                        Chip("kept", ChipTone.Ok)
                        WhoTag("kept", keep.maker, CONTRACT_WHO_TAG_PREFIX + id)
                        if (keep.words.isNotEmpty()) Text(keep.words, style = OrreryTokens.Type.small)
                    }
                } else if (deciding) {
                    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                        Button(onClick = { onKeep(id) }, modifier = Modifier.testTag(ADDITION_KEEP_TAG_PREFIX + id)) {
                            Text("Keep", style = OrreryTokens.Type.small)
                        }
                        OutlinedButton(onClick = { onStrike(id) }, modifier = Modifier.testTag(ADDITION_STRIKE_TAG_PREFIX + id)) {
                            Text("Strike", style = OrreryTokens.Type.small)
                        }
                    }
                    Text(
                        "Asked in the conversation. Say “keep it” or “strike it” there, or press here.",
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                    )
                } else {
                    Chip("not decided", ChipTone.Neutral)
                }
                RowTalk(RowAbout(id, addition.what), onAbout)
                Refusals(stage.refusals, id)
            }
        }
        struck.forEach { strike ->
            PageRow(ADDITION_STRUCK_TAG_PREFIX + strike.row) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    IdChip(strike.row)
                    Chip("struck", ChipTone.Bad)
                    WhoTag("struck", strike.maker, CONTRACT_WHO_TAG_PREFIX + strike.row)
                }
                if (strike.words.isNotEmpty()) Text(strike.words, style = OrreryTokens.Type.small)
                Refusals(stage.refusals, strike.row)
            }
        }
        Refusals(stage.refusals, "additions")
    }
}

@Composable
private fun RequirementsSection(body: DrawnBody, stage: ContractStageRow, onAbout: ((RowAbout) -> Unit)?) {
    PageSection("requirements", "What will be true when done", count = countLabel(body.requirements.size)) {
        body.requirements.forEach { requirement ->
            PageRow(CONTRACT_ROW_TAG_PREFIX + requirement.id) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    IdChip(requirement.id)
                    Text(requirement.text, style = OrreryTokens.Type.body)
                    ChangedIn(requirement.id in body.changedRows, body.version)
                }
                Text(requirement.evidence, style = OrreryTokens.Type.small)
                RowTalk(RowAbout(requirement.id, requirement.text), onAbout)
                Refusals(stage.refusals, requirement.id)
            }
        }
        Refusals(stage.refusals, "requirements")
    }
}

@Composable
private fun PremisesSection(body: DrawnBody, stage: ContractStageRow, onAbout: ((RowAbout) -> Unit)?) {
    PageSection("premises", "What I’m assuming", count = countLabel(body.contract.premises.size)) {
        body.contract.premises.forEach { premise ->
            PageRow(CONTRACT_ROW_TAG_PREFIX + premise.id) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    IdChip(premise.id)
                    Text(premise.text, style = OrreryTokens.Type.body)
                    ChangedIn(premise.id in body.changedRows, body.version)
                }
                Text(premise.ifWrong, style = OrreryTokens.Type.small)
                RowTalk(RowAbout(premise.id, premise.text), onAbout)
                Refusals(stage.refusals, premise.id)
            }
        }
        Refusals(stage.refusals, "premises")
    }
}

@Composable
private fun ExclusionsSection(body: DrawnBody, stage: ContractStageRow) {
    PageSection("exclusions", "What we won’t do") {
        body.contract.exclusions.forEach { exclusion ->
            Text(exclusion, style = OrreryTokens.Type.body)
        }
        Refusals(stage.refusals, "exclusions")
    }
}

@Composable
private fun ApproachSection(body: DrawnBody, stage: ContractStageRow) {
    PageSection("approach", "How, and why not the other ways") {
        Text(body.contract.approach, style = OrreryTokens.Type.body)
        body.contract.alternatives.forEachIndexed { index, alternative ->
            PageRow(CONTRACT_ALTERNATIVE_TAG_PREFIX + index) {
                Text(alternative.alternative, style = OrreryTokens.Type.body)
                Text(alternative.whyNot, style = OrreryTokens.Type.small)
                Text(alternative.reconsider, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            }
        }
        Refusals(stage.refusals, "approach")
    }
}

@Composable
private fun StepsSection(body: DrawnBody, stage: ContractStageRow, deciding: Boolean, onShown: (Long, List<String>) -> Unit) {
    val steps = body.contract.releaseSteps
    val irreversible = steps.mapIndexedNotNull { index, step -> if (step.undo == "none") "step${index + 1}" else null }
    val shown = stage.shown.filter { it.version == body.version }.map { it.row }.toSet()
    val unshown = irreversible.filter { it !in shown }
    val count = if (steps.isEmpty()) {
        ""
    } else if (irreversible.isEmpty()) {
        "${steps.size} steps"
    } else {
        "${steps.size} steps · ${irreversible.size} cannot be undone"
    }
    PageSection("steps", "What leaves this machine or can’t be undone", count = count) {
        if (deciding && irreversible.isNotEmpty()) {
            if (unshown.isEmpty()) {
                Text("you read this", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            } else {
                Text("read this before Accept", style = OrreryTokens.Type.small, color = OrreryTokens.Role.attention)
            }
            ReportShown(body.version, unshown, onShown)
        }
        steps.forEachIndexed { index, step ->
            val position = index + 1
            PageRow(CONTRACT_STEP_TAG_PREFIX + position) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    Text("$position", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                    Text(step.step, style = OrreryTokens.Type.body)
                }
                Text("→ ${step.target}", style = OrreryTokens.Type.small)
                if (step.undo == "none") {
                    Chip("none: cannot be undone", ChipTone.Bad, Modifier.testTag(STEP_UNDO_NONE_TAG_PREFIX + position))
                } else {
                    Text("undo: ${step.undo}", style = OrreryTokens.Type.small)
                }
                Refusals(stage.refusals, "step$position")
            }
        }
        if (body.contract.releaseThen.isNotEmpty()) {
            Text("Then: ${body.contract.releaseThen}", style = OrreryTokens.Type.small)
        }
        Refusals(stage.refusals, "steps")
    }
}

@Composable
private fun DecisionsSection(
    body: DrawnBody,
    stage: ContractStageRow,
    questions: List<DesignQuestionRow>,
    needed: Int,
    thread: @Composable (DesignQuestionRow) -> Unit,
) {
    val openQuestions = questions.filter { it.answer.isEmpty() }
    val settledQuestions = questions.filter { it.answer.isNotEmpty() }
    val openDecisions = body.decisions.filter { it.answer.isEmpty() }
    PageSection("decisions", "Decisions I need from you", count = countLabel(needed)) {
        openQuestions.forEach { question -> thread.invoke(question) }
        if (openQuestions.isEmpty() && openDecisions.isEmpty()) {
            Text("None.", style = OrreryTokens.Type.small)
        }
        if (settledQuestions.isNotEmpty()) {
            Text("Settled:", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            settledQuestions.forEach { question -> thread.invoke(question) }
        }
        body.decisions.forEach { decision ->
            PageRow(CONTRACT_DECISION_TAG_PREFIX + decision.id) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    IdChip(decision.id)
                    Text(decision.question, style = OrreryTokens.Type.body)
                    ChangedIn(decision.id in body.changedRows, body.version)
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
                    decision.options.forEachIndexed { index, option ->
                        Row(
                            modifier = Modifier.testTag(CONTRACT_OPTION_TAG_PREFIX + "${decision.id}:$index"),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
                        ) {
                            Text(option, style = OrreryTokens.Type.small)
                            if (option == decision.recommendation) Chip("recommended", ChipTone.Ok)
                        }
                    }
                }
                Text(decision.reason, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
                Text(
                    if (decision.answer.isEmpty()) "Not answered yet." else "Answer: ${decision.answer}",
                    style = OrreryTokens.Type.small,
                )
                Refusals(stage.refusals, decision.id)
            }
        }
        Refusals(stage.refusals, "decisions")
    }
}

@Composable
private fun ChecksSection(body: DrawnBody, stage: ContractStageRow) {
    PageSection("checks", "Checks the machines run") {
        Text(body.contract.checks, style = OrreryTokens.Type.body)
        Refusals(stage.refusals, "checks")
    }
}
