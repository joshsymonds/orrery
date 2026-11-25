package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import io.github.joshsymonds.orrery.api.DesignApproachRow
import io.github.joshsymonds.orrery.api.DesignChoiceRow
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignDecisionRow
import io.github.joshsymonds.orrery.api.DesignDelegationRow
import io.github.joshsymonds.orrery.api.DesignLedgerItemRow
import io.github.joshsymonds.orrery.api.DesignLogRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.DesignRecordRow
import io.github.joshsymonds.orrery.api.DesignStageRow
import io.github.joshsymonds.orrery.api.DesignStepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.time.Instant
import java.time.ZoneId

/** The tag of a design page's root node (O.designPage and O.designBody in design.js). */
internal const val DESIGN_PAGE_TAG = "design-page"

/** The tag prefix of a stage of the design's stage strip, followed by the stage's name (O.dtrack in design.js). */
internal const val DESIGN_STAGE_TAG_PREFIX = "design-stage:"

/**
 * The tag prefix of a section of the design page, followed by the section's name: now, decided, contracts, steelman,
 * approach, questions, research or log (the sections of O.designBody in design.js).
 */
internal const val DESIGN_SECTION_TAG_PREFIX = "design-section:"

/** The tag of the one note drawn in place of the stage parts when the design's stage is not served. */
internal const val DESIGN_STAGE_NOTE_TAG = "design-stage-note"

/** The tag prefix of a need line under Now, one per open question, followed by the question's key (Q and its id). */
internal const val DESIGN_NEED_TAG_PREFIX = "design-need:"

/** The tag prefix of a delegation's line under Decided for you, followed by the delegation's seq. */
internal const val DESIGN_DELEGATION_TAG_PREFIX = "design-delegation:"

/**
 * The tag prefix of a decision under a delegation, followed by the delegation's seq, a colon and the decision's index in
 * the delegation.
 */
internal const val DESIGN_DECISION_TAG_PREFIX = "design-decision:"

/** The tag prefix of a contract's row, followed by the contract's id. Tapping the row opens the contract. */
internal const val DESIGN_CONTRACT_TAG_PREFIX = "design-contract:"

/** The tag prefix of a budget cell of the steelman, followed by discovery or closure (O.budget in design.js). */
internal const val DESIGN_BUDGET_TAG_PREFIX = "design-budget:"

/** The tag prefix of a pass line of the steelman, followed by the pass's step id (O.steelman in design.js). */
internal const val DESIGN_PASS_TAG_PREFIX = "design-pass:"

/** The tag of the ledger's heading, which carries the count of each disposition and whether the ledger is frozen. */
internal const val DESIGN_LEDGER_TAG = "design-ledger"

/** The tag prefix of a ledger item, followed by the item's id, and carrying its maker when it is settled (O.finding). */
internal const val DESIGN_LEDGER_ITEM_TAG_PREFIX = "design-ledger-item:"

/** The tag prefix of an approach's card, followed by the approach's seq (O.approaches in design.js). */
internal const val DESIGN_APPROACH_TAG_PREFIX = "design-approach:"

/** The tag of the line that says which approach was chosen, who chose it and in what words. */
internal const val DESIGN_CHOSEN_TAG = "design-chosen"

/** The tag prefix of a research or reproduce step's row, followed by the step's id (O.researchList in design.js). */
internal const val DESIGN_RESEARCH_TAG_PREFIX = "design-research:"

/** The tag prefix of a log line, followed by the line's seq (O.dlog in design.js). */
internal const val DESIGN_LOG_TAG_PREFIX = "design-log:"

/**
 * A design's page, the record round 8 draws for a design (O.designPage and O.designBody in design.js): the stage strip, what
 * waits on the owner now, the decisions the Director made for him, the contracts in start order, the steelman, the approach,
 * the questions, the research and the log. [record] is the design and what it holds, and [now] is the time the page is read
 * at, in unix seconds. [thread] draws a question's thread wherever a question is shown, and [onOpenContract] opens a contract
 * of the design. The root node carries [DESIGN_PAGE_TAG].
 */
@Composable
internal fun DesignPage(
    record: DesignRecordRow,
    now: Long,
    thread: @Composable (DesignQuestionRow) -> Unit,
    onOpenContract: (DesignContractRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    val design = record.design
    val stage = record.stage
    val questions = stage?.questions.orEmpty()
    Column(modifier = modifier.fillMaxWidth().verticalScroll(rememberScrollState()).testTag(DESIGN_PAGE_TAG)) {
        if (stage == null) {
            InlineNote(STAGE_NOT_SERVED, NoteTone.Quiet, Modifier.testTag(DESIGN_STAGE_NOTE_TAG))
        } else {
            DesignStageStrip(record, stage)
        }
        DesignNow(design.state, questions.filter { it.answer.isEmpty() })
        DesignDecided(record.delegations)
        DesignContracts(design.contracts, onOpenContract)
        if (stage != null) DesignSteelman(stage, now)
        DesignApproaches(record.approaches, record.choice)
        DesignQuestions(questions, thread)
        DesignResearch(stage?.steps.orEmpty())
        DesignLog(record.log)
    }
}

// STAGE_NOT_SERVED is the one note a design draws in place of its stage parts when its stage is not served.
private const val STAGE_NOT_SERVED = "The design's stage is not served."

// The six stages of a design, in the order the strip draws them.
private val DESIGN_STAGES = listOf("Research", "Questions", "Design", "Steelman", "Closure", "Proposed")

// The kinds of a design step, the stage tool that started it, and the status of a step that has not ended.
private const val KIND_RESEARCH = "research"
private const val KIND_REPRODUCE = "reproduce"
private const val KIND_DISCOVERY = "discovery"
private const val KIND_CLOSURE = "closure"
private const val STATUS_RUNNING = "running"
private const val STATUS_RETURNED = "returned"

// The dispositions of a ledger item, in the order their counts are drawn, each with its words.
private const val DISPOSITION_OPEN = "open"
private val DISPOSITION_WORDS = listOf(
    DISPOSITION_OPEN to "open",
    "adopted" to "adopted",
    "rejected" to "rejected",
    "deferred" to "deferred",
    "revised_after_closure" to "revised after closure",
    "accepted_as_risk" to "accepted as a risk",
)

// The words of each verdict a steelman pass returns, as round 8 says them (O.VERDICTS in design.js).
private val VERDICT_WORDS = mapOf(
    "READY" to "ready",
    "REVISE" to "revise",
    "NEEDS_DECISION" to "needs your decision",
    "BLOCKED" to "blocked",
    "STILL_OPEN" to "still open",
    "CHANGE_INDUCED_CONCERN" to "a change raised a concern",
)

// The states a steelman budget cell can be in, which are its state description.
private const val BUDGET_LEFT = "left"
private const val BUDGET_RUNNING = "running"
private const val BUDGET_USED = "used"
private const val BUDGET_NOT_COUNTED = "not counted"
private const val BUDGET_NOT_USED = "will not be used"

/**
 * The index of the stage the design is at: the latest stage begun, in [DESIGN_STAGES]. It is the number of stages when all
 * of them are done, which is when every contract is accepted.
 */
private fun currentStageOf(record: DesignRecordRow, stage: DesignStageRow): Int {
    val contracts = record.design.contracts
    return when {
        contracts.isNotEmpty() && contracts.all { it.state == "accepted" } -> DESIGN_STAGES.size
        contracts.any { it.state == "proposed" || it.state == "accepted" } -> DESIGN_STAGES.indexOf("Proposed")
        stage.closureSkipped || stage.steps.any { it.kind == KIND_CLOSURE } -> DESIGN_STAGES.indexOf("Closure")
        stage.steps.any { it.kind == KIND_DISCOVERY } -> DESIGN_STAGES.indexOf("Steelman")
        record.approaches.isNotEmpty() -> DESIGN_STAGES.indexOf("Design")
        stage.questions.isNotEmpty() -> DESIGN_STAGES.indexOf("Questions")
        else -> DESIGN_STAGES.indexOf("Research")
    }
}

// stageWordsOf says what each stage that has something to say writes under its name: the research returned, the
// questions settled, and the verdict of the discovery and closure passes, or skipped.
private fun stageWordsOf(stage: DesignStageRow): Map<String, String> = buildMap {
    val research = researchOf(stage.steps)
    if (research.isNotEmpty()) {
        put("Research", "${research.count { it.status == STATUS_RETURNED }} of ${research.size} returned")
    }
    if (stage.questions.isNotEmpty()) {
        put("Questions", "${settledOf(stage.questions)} of ${stage.questions.size} settled")
    }
    latestStep(stage, KIND_DISCOVERY)?.let { put("Steelman", passWordOf(it)) }
    val closure = latestStep(stage, KIND_CLOSURE)
    when {
        closure != null -> put("Closure", passWordOf(closure))
        stage.closureSkipped -> put("Closure", "skipped")
    }
}

// passWordOf is a pass's word on the strip: its verdict, else running while it runs, else nothing.
private fun passWordOf(step: DesignStepRow): String = when {
    step.verdict.isNotEmpty() -> verdictWordOf(step.verdict)
    step.status == STATUS_RUNNING -> "running"
    else -> ""
}

/** The verdict's words, or the verdict itself when round 8 has no words for it. */
private fun verdictWordOf(verdict: String): String = VERDICT_WORDS[verdict] ?: verdict

// researchOf is the research and reproduce steps of a design, which the strip counts and the Research section draws.
private fun researchOf(steps: List<DesignStepRow>): List<DesignStepRow> =
    steps.filter { it.kind == KIND_RESEARCH || it.kind == KIND_REPRODUCE }

private fun settledOf(questions: List<DesignQuestionRow>): Int = questions.count { it.answer.isNotEmpty() }

// latestStep is the newest step of kind, the steps being oldest first.
private fun latestStep(stage: DesignStageRow, kind: String): DesignStepRow? = stage.steps.lastOrNull { it.kind == kind }

@Composable
private fun DesignStageStrip(record: DesignRecordRow, stage: DesignStageRow) {
    val current = currentStageOf(record, stage)
    val words = stageWordsOf(stage)
    Column(modifier = Modifier.fillMaxWidth()) {
        DESIGN_STAGES.forEachIndexed { index, name ->
            val state = when {
                index < current -> StageState.DONE
                index == current -> StageState.NOW
                else -> StageState.AHEAD
            }
            DesignStageCell(name, state, words[name].orEmpty())
        }
    }
}

// DesignStageCell draws one stage of the strip: its name, its state as its state description, and its words, which
// say not reached when it is ahead and nothing else has been said.
@Composable
private fun DesignStageCell(name: String, state: StageState, words: String) {
    val ink = when (state) {
        StageState.NOW -> OrreryTokens.Ink.one
        StageState.DONE -> OrreryTokens.Ink.two
        StageState.AHEAD -> OrreryTokens.Ink.four
    }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_STAGE_TAG_PREFIX + name)
            .semantics(mergeDescendants = true) { stateDescription = state.name.lowercase() },
    ) {
        Text(if (state == StageState.DONE) "✓ $name" else name, color = ink, style = OrreryTokens.Type.label)
        val shown = words.ifEmpty { if (state == StageState.AHEAD) "not reached" else "" }
        if (shown.isNotEmpty()) Text(shown, color = ink, style = OrreryTokens.Type.small)
    }
}

/** A section of the page: its title and count in label case, then what the section draws. */
@Composable
private fun DesignSection(name: String, title: String, count: String = "", content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(top = OrreryTokens.Space.twentyFour).testTag(DESIGN_SECTION_TAG_PREFIX + name),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        SectionHead(title, count = count)
        content()
    }
}

// DesignNow draws what the design is doing and each open question as a need line. Nothing is drawn with neither.
@Composable
private fun DesignNow(state: String, open: List<DesignQuestionRow>) {
    if (state.isEmpty() && open.isEmpty()) return
    DesignSection("now", "Now") {
        if (state.isNotEmpty()) Text(state, style = OrreryTokens.Type.body)
        open.forEach { question ->
            Text(
                designWords(question.key, question.text),
                modifier = Modifier.testTag(DESIGN_NEED_TAG_PREFIX + question.key),
                style = OrreryTokens.Type.small,
            )
        }
    }
}

// DesignDecided draws what the Director decided for the owner, each delegation's message with its kinds and time, and
// under it each decision it made, marked as the Director's.
@Composable
private fun DesignDecided(delegations: List<DesignDelegationRow>) {
    if (delegations.isEmpty()) return
    DesignSection("decided", "Decided for you", count = delegations.sumOf { it.decisions.size }.toString()) {
        delegations.forEach { delegation ->
            DesignDelegationLine(delegation)
            delegation.decisions.forEachIndexed { index, decision ->
                DesignDecisionLine(delegation.seq, index, decision)
            }
        }
    }
}

// DesignDelegationLine draws a delegation: the owner's message quoted, its kinds and when it was stored. It is the owner's.
@Composable
private fun DesignDelegationLine(delegation: DesignDelegationRow) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_DELEGATION_TAG_PREFIX + delegation.seq)
            .semantics(mergeDescendants = true) { maker = "you" },
    ) {
        Text(designWords("Delegated by you", designClock(delegation.time)), style = OrreryTokens.Type.small)
        Text("“${delegation.message}”", style = OrreryTokens.Type.body)
        if (delegation.kinds.isNotEmpty()) {
            Text(delegation.kinds.joinToString(" · "), color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
        }
    }
}

// DesignDecisionLine draws one decision the Director made under a delegation: its kind and ref, its text and its reason.
// Its maker is the Director, which the Maker mark names.
@Composable
private fun DesignDecisionLine(seq: Long, index: Int, decision: DesignDecisionRow) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen)
            .testTag("$DESIGN_DECISION_TAG_PREFIX$seq:$index")
            .semantics(mergeDescendants = true) { maker = "director" },
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(designWords(decision.kind, decision.ref), style = OrreryTokens.Type.label)
            DesignWhoTag("director")
        }
        Text(decision.text, style = OrreryTokens.Type.small)
        if (decision.reason.isNotEmpty()) Text(decision.reason, style = OrreryTokens.Type.small)
    }
}

// DesignContracts draws the design's contracts in start order, each a row that opens its contract.
@Composable
private fun DesignContracts(contracts: List<DesignContractRow>, onOpenContract: (DesignContractRow) -> Unit) {
    if (contracts.isEmpty()) return
    DesignSection("contracts", "Contracts", count = contracts.size.toString()) {
        contracts.sortedBy { it.position }.forEach { DesignContractLine(it, onOpenContract) }
    }
}

@Composable
private fun DesignContractLine(contract: DesignContractRow, onOpenContract: (DesignContractRow) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_CONTRACT_TAG_PREFIX + contract.id)
            .semantics(mergeDescendants = true) {}
            .clickable(onClick = { onOpenContract(contract) }),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(contract.id, style = OrreryTokens.Type.label)
            if (contract.title.isNotEmpty()) Text(contract.title, style = OrreryTokens.Type.small)
            Text(startsWordsOf(contract), color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
            if (contract.epic.isNotEmpty()) {
                Text(designWords("epic ${contract.epic}", contract.epicPhase), color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
            }
        }
        Text(contractStateWordOf(contract.state), style = OrreryTokens.Type.small)
    }
}

// startsWordsOf says what a contract starts after, or that it starts first.
private fun startsWordsOf(contract: DesignContractRow): String =
    if (contract.after.isEmpty()) "starts first" else "after ${contract.after.joinToString(", ")}"

// contractStateWordOf is a contract's state in words; a set-aside contract reads as round 8 says it.
private fun contractStateWordOf(state: String): String = if (state == "set_aside") "set aside" else state

// DesignSteelman draws the steelman: the budget's two cells, each pass with its verdict and when, then the ledger.
@Composable
private fun DesignSteelman(stage: DesignStageRow, now: Long) {
    DesignSection("steelman", "Steelman") {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve)) {
            DesignBudgetCell("discovery", "Discovery · 1", discoveryCellOf(stage, now))
            DesignBudgetCell("closure", "Closure · at most 1", closureCellOf(stage, now))
        }
        stage.steps.filter { it.kind == KIND_DISCOVERY || it.kind == KIND_CLOSURE }.forEach { step ->
            Text(
                passLineOf(step, now),
                modifier = Modifier.testTag(DESIGN_PASS_TAG_PREFIX + step.step),
                style = OrreryTokens.Type.small,
            )
        }
        if (stage.ledger.isNotEmpty()) DesignLedger(stage)
    }
}

// discoveryCellOf is the discovery cell: the newest discovery step's state, or left when the Director has not started one.
private fun discoveryCellOf(stage: DesignStageRow, now: Long): DesignBudgetState {
    val step = latestStep(stage, KIND_DISCOVERY)
        ?: return DesignBudgetState(BUDGET_LEFT, "1 left · the Director runs it when the drafts are whole")
    return passCellOf(step, now)
}

// closureCellOf is the closure cell: the newest closure step's state, or will not be used when closure was skipped, else
// left, which is said after discovery or after its revising.
private fun closureCellOf(stage: DesignStageRow, now: Long): DesignBudgetState {
    val step = latestStep(stage, KIND_CLOSURE)
    return when {
        step != null -> passCellOf(step, now)
        stage.closureSkipped -> DesignBudgetState(BUDGET_NOT_USED, "closure was skipped")
        latestStep(stage, KIND_DISCOVERY)?.counted == true -> DesignBudgetState(BUDGET_LEFT, "1 left · the Director runs it after revising")
        else -> DesignBudgetState(BUDGET_LEFT, "1 left · after discovery")
    }
}

// passCellOf is a budget cell of a pass that has a step: running while it runs, used with its verdict when it counts, else
// not counted.
private fun passCellOf(step: DesignStepRow, now: Long): DesignBudgetState = when {
    step.status == STATUS_RUNNING -> DesignBudgetState(BUDGET_RUNNING, designWords("running", elapsedOf(step, now)))
    step.counted -> DesignBudgetState(BUDGET_USED, designWords("used", designClock(step.created), verdictWordOf(step.verdict)))
    else -> DesignBudgetState(BUDGET_NOT_COUNTED, designWords("not counted", verdictWordOf(step.verdict)))
}

// DesignBudgetState is a budget cell's state, as its state description, and its words.
private data class DesignBudgetState(val state: String, val words: String)

@Composable
private fun RowScope.DesignBudgetCell(name: String, label: String, cell: DesignBudgetState) {
    Column(
        modifier = Modifier
            .weight(1f)
            .testTag(DESIGN_BUDGET_TAG_PREFIX + name)
            .semantics(mergeDescendants = true) { stateDescription = cell.state },
    ) {
        Text(label, style = OrreryTokens.Type.label)
        Text(cell.words, style = OrreryTokens.Type.small)
    }
}

// passLineOf says one pass's result: that it is running and for how long, or its verdict and when it started.
private fun passLineOf(step: DesignStepRow, now: Long): String {
    val name = if (step.kind == KIND_DISCOVERY) "Discovery" else "Closure"
    return when {
        step.status == STATUS_RUNNING -> designWords("$name is running", elapsedOf(step, now))
        step.verdict.isNotEmpty() -> designWords("$name returned ${verdictWordOf(step.verdict)}", designClock(step.created))
        else -> designWords(name, step.status, designClock(step.created))
    }
}

// DesignLedger draws the ledger: how many findings it has, how many of each disposition, whether it is frozen, then each
// item.
@Composable
private fun DesignLedger(stage: DesignStageRow) {
    Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
        Column(
            modifier = Modifier.fillMaxWidth().testTag(DESIGN_LEDGER_TAG).semantics(mergeDescendants = true) {},
        ) {
            Text("Ledger".inLabelCase(), style = OrreryTokens.Type.label)
            Text(ledgerCountsOf(stage), style = OrreryTokens.Type.small)
            if (stage.frozen) Text("frozen", color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
        }
        stage.ledger.forEach { DesignLedgerItem(it) }
    }
}

// ledgerCountsOf says how many findings the ledger has and how many of each disposition it holds, where any.
private fun ledgerCountsOf(stage: DesignStageRow): String {
    val counts = DISPOSITION_WORDS.mapNotNull { (disposition, word) ->
        stage.ledger.count { it.disposition == disposition }.takeIf { it > 0 }?.let { "$it $word" }
    }
    return designWords("${stage.ledger.size} findings", *counts.toTypedArray())
}

// DesignLedgerItem draws one item: its id, impact, evidence and change, its disposition with who made it, and closure's
// word and note. A settled item carries its maker as the Maker mark.
@Composable
private fun DesignLedgerItem(item: DesignLedgerItemRow) {
    val settled = item.disposition != DISPOSITION_OPEN
    val who = whoOf(item.maker)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_LEDGER_ITEM_TAG_PREFIX + item.id)
            .semantics(mergeDescendants = true) { if (settled) maker = who },
    ) {
        Text(item.id, style = OrreryTokens.Type.label)
        Text(item.impact, style = OrreryTokens.Type.small)
        if (item.evidence.isNotEmpty()) Text("Evidence: ${item.evidence}", style = OrreryTokens.Type.small)
        if (item.change.isNotEmpty()) Text("Change: ${item.change}", style = OrreryTokens.Type.small)
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(dispositionWordOf(item), style = OrreryTokens.Type.small)
            if (settled) DesignWhoTag(who)
        }
        if (item.closureWord.isNotEmpty() || item.closureNote.isNotEmpty()) {
            Text(designWords("closure", item.closureWord, item.closureNote), color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
        }
    }
}

// dispositionWordOf says an item's disposition with its reason or boundary: rejected because, deferred until.
private fun dispositionWordOf(item: DesignLedgerItemRow): String = when (item.disposition) {
    "rejected" -> "rejected because: ${item.reason}"
    "deferred" -> "deferred until ${item.boundary}"
    else -> designWords(DISPOSITION_WORDS.firstOrNull { it.first == item.disposition }?.second ?: item.disposition, item.reason)
}

// DesignApproaches draws the approaches the Director compared, the recommended one marked, and the chosen one with who
// chose it.
@Composable
private fun DesignApproaches(approaches: List<DesignApproachRow>, choice: DesignChoiceRow?) {
    if (approaches.isEmpty()) return
    DesignSection("approach", "Approach", count = if (choice == null) "${approaches.size} compared" else "") {
        approaches.forEach { DesignApproachCard(it, chosen = choice?.approach == it.seq) }
        if (choice != null) {
            DesignChosenLine(choice, approaches.firstOrNull { it.seq == choice.approach }?.name.orEmpty())
        }
    }
}

@Composable
private fun DesignApproachCard(approach: DesignApproachRow, chosen: Boolean) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_APPROACH_TAG_PREFIX + approach.seq)
            .semantics(mergeDescendants = true) {},
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(approach.name, style = OrreryTokens.Type.label)
            if (approach.recommended) Text("recommended", color = OrreryTokens.Role.attention, style = OrreryTokens.Type.small)
            if (chosen) Text("chosen", color = OrreryTokens.Role.ok, style = OrreryTokens.Type.small)
        }
        DesignLabelled("Shape", approach.shape)
        DesignLabelled("Costs", approach.cost)
        DesignLabelled("Why this one", approach.wins)
        DesignLabelled("Where it loses", approach.loses)
        DesignLabelled("Reconsider when", approach.reconsider)
        if (approach.recommended) DesignLabelled("Recommended because", approach.reason)
        if (approach.research.isNotEmpty()) DesignLabelled("From research", approach.research.joinToString(" "))
    }
}

// DesignChosenLine draws the approach chosen, with the maker who chose it and their words.
@Composable
private fun DesignChosenLine(choice: DesignChoiceRow, name: String) {
    val who = whoOf(choice.maker)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_CHOSEN_TAG)
            .semantics(mergeDescendants = true) { maker = who },
    ) {
        Text(designWords("Chosen", name), style = OrreryTokens.Type.label)
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            DesignWhoTag(who)
            Text(designClock(choice.time), style = OrreryTokens.Type.small)
        }
        if (choice.words.isNotEmpty()) Text(quoteOf(who, choice.words), style = OrreryTokens.Type.small)
    }
}

// DesignLabelled draws a labelled fact of an approach, when it has one.
@Composable
private fun DesignLabelled(label: String, text: String) {
    if (text.isNotEmpty()) Text("$label: $text", style = OrreryTokens.Type.small)
}

// DesignQuestions draws the design's questions, the open ones first, each as the thread the caller draws for it.
@Composable
private fun DesignQuestions(questions: List<DesignQuestionRow>, thread: @Composable (DesignQuestionRow) -> Unit) {
    if (questions.isEmpty()) return
    val open = questions.filter { it.answer.isEmpty() }
    val settled = questions.filter { it.answer.isNotEmpty() }
    DesignSection("questions", "Questions", count = "${settled.size} of ${questions.size} settled") {
        // The thread is invoked through invoke: the app's scan for thread starts reads a call by the bare name as one.
        (open + settled).forEach { question -> thread.invoke(question) }
    }
}

// DesignResearch draws each research or reproduce step: its subject, its status and its report, once it has one.
@Composable
private fun DesignResearch(steps: List<DesignStepRow>) {
    val research = researchOf(steps)
    if (research.isEmpty()) return
    DesignSection("research", "Research") {
        research.forEach { step ->
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag(DESIGN_RESEARCH_TAG_PREFIX + step.step)
                    .semantics(mergeDescendants = true) {},
            ) {
                Text(step.subject, style = OrreryTokens.Type.label)
                Text(step.status, color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
                if (step.report.isNotEmpty()) Text(step.report, style = OrreryTokens.Type.small)
            }
        }
    }
}

// DesignLog draws the log newest first, each line with its time, its maker and what was done, and its words under it.
@Composable
private fun DesignLog(log: List<DesignLogRow>) {
    if (log.isEmpty()) return
    DesignSection("log", "Log") {
        log.sortedByDescending { it.seq }.forEach { DesignLogLine(it) }
    }
}

@Composable
private fun DesignLogLine(line: DesignLogRow) {
    val who = whoOf(line.maker)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(DESIGN_LOG_TAG_PREFIX + line.seq)
            .semantics(mergeDescendants = true) { maker = who },
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve)) {
            Text(designClock(line.time), style = OrreryTokens.Type.small)
            DesignWhoTag(who)
            Text(designWords(line.what, *line.ids.toTypedArray()), modifier = Modifier.weight(1f), style = OrreryTokens.Type.small)
        }
        if (line.words.isNotEmpty()) Text(quoteOf(who, line.words), style = OrreryTokens.Type.small)
    }
}

// DesignWhoTag draws who made something: the owner, the Director in the engine's colour, or orreryd.
@Composable
private fun DesignWhoTag(who: String) {
    Text(
        makerNameOf(who),
        color = if (who == "director") OrreryTokens.Role.engine else OrreryTokens.Ink.two,
        style = OrreryTokens.Type.small,
    )
}

// makerNameOf is a maker's name as round 8 gives it: you, the Director, or orreryd.
private fun makerNameOf(who: String): String = when (who) {
    "you" -> "you"
    "director" -> "the Director"
    else -> "orreryd"
}

// quoteOf is the owner's words in quotation marks; another maker's words are drawn as they are.
private fun quoteOf(who: String, words: String): String = if (who == "you") "“$words”" else words

// designWords joins the parts that are not empty with a middle dot.
private fun designWords(vararg parts: String): String = parts.filter { it.isNotEmpty() }.joinToString(" · ")

// designClock is a time as the clock reads it, or nothing for a time that is not set.
private fun designClock(time: Long): String =
    if (time <= 0L) "" else Instant.ofEpochSecond(time).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)

// elapsedOf is how long a step has run by now.
private fun elapsedOf(step: DesignStepRow, now: Long): String = durationText((now - step.created).coerceAtLeast(0L))
