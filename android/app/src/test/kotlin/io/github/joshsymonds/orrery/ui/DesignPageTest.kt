package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.api.DesignApproachRow
import io.github.joshsymonds.orrery.api.DesignChoiceRow
import io.github.joshsymonds.orrery.api.DesignContractRow
import io.github.joshsymonds.orrery.api.DesignDecisionRow
import io.github.joshsymonds.orrery.api.DesignDelegationRow
import io.github.joshsymonds.orrery.api.DesignLedgerItemRow
import io.github.joshsymonds.orrery.api.DesignLogRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.DesignRecordRow
import io.github.joshsymonds.orrery.api.DesignRow
import io.github.joshsymonds.orrery.api.DesignStageRow
import io.github.joshsymonds.orrery.api.DesignStepRow
import io.github.joshsymonds.orrery.api.RecordMakerRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

// 1_700_000_000 is 22:13:20 in UTC, the zone these tests run in, so a time at BASE reads 22:13.
private const val BASE = 1_700_000_000L

// NOW is a minute after BASE, so a step started at BASE has run for one minute.
private const val NOW = BASE + 60L

private val STAGE_NAMES = listOf("Research", "Questions", "Design", "Steelman", "Closure", "Proposed")

private fun design(contracts: List<DesignContractRow> = emptyList(), state: String = "") = DesignRow(
    id = "example-design",
    title = "Example design",
    conversation = "example-conversation",
    created = BASE,
    ofOne = false,
    state = state,
    contracts = contracts,
)

private fun contract(
    id: String,
    position: Long,
    state: String = "proposed",
    after: List<String> = emptyList(),
    epic: String = "",
    epicPhase: String = "",
) = DesignContractRow(
    id = id,
    title = "Example $id",
    repository = "example-repo",
    state = state,
    position = position,
    after = after,
    epic = epic,
    epicPhase = epicPhase,
)

private fun step(
    id: String,
    kind: String,
    status: String = "returned",
    subject: String = "Example subject",
    report: String = "",
    verdict: String = "",
    counted: Boolean = false,
) = DesignStepRow(
    step = id,
    kind = kind,
    subject = subject,
    created = BASE,
    status = status,
    report = report,
    verdict = verdict,
    counted = counted,
)

private fun question(id: Long, answer: String = "", text: String = "Which way?") = DesignQuestionRow(
    design = "example-design",
    id = id,
    contract = "",
    text = text,
    options = listOf("use the table", "use a list"),
    recommendation = "use the table",
    asked = BASE,
    answer = answer,
    accepted = false,
    maker = if (answer.isEmpty()) null else RecordMakerRow(kind = "owner_serve"),
    answered = if (answer.isEmpty()) 0L else BASE,
)

private fun item(
    id: String,
    disposition: String,
    kind: String = "",
    reason: String = "",
    boundary: String = "",
    closureWord: String = "",
    closureNote: String = "",
) = DesignLedgerItemRow(
    id = id,
    contract = "example-a",
    origin = "discovery",
    impact = "Impact $id",
    evidence = "Evidence $id",
    change = "Change $id",
    disposition = disposition,
    reason = reason,
    boundary = boundary,
    maker = if (kind.isEmpty()) null else RecordMakerRow(kind = kind),
    closureWord = closureWord,
    closureNote = closureNote,
)

private fun stage(
    steps: List<DesignStepRow> = emptyList(),
    questions: List<DesignQuestionRow> = emptyList(),
    ledger: List<DesignLedgerItemRow> = emptyList(),
    frozen: Boolean = false,
    closureSkipped: Boolean = false,
) = DesignStageRow(
    steps = steps,
    questions = questions,
    ledger = ledger,
    frozen = frozen,
    closureSkipped = closureSkipped,
)

private fun approach(
    seq: Long,
    name: String,
    shape: String,
    cost: String,
    recommended: Boolean = false,
    reason: String = "",
) = DesignApproachRow(
    seq = seq,
    name = name,
    shape = shape,
    cost = cost,
    wins = "",
    loses = "",
    reconsider = "",
    research = emptyList(),
    recommended = recommended,
    reason = reason,
    time = BASE,
)

private fun record(
    design: DesignRow = design(),
    stage: DesignStageRow? = stage(),
    approaches: List<DesignApproachRow> = emptyList(),
    choice: DesignChoiceRow? = null,
    log: List<DesignLogRow> = emptyList(),
    delegations: List<DesignDelegationRow> = emptyList(),
) = DesignRecordRow(
    design = design,
    approaches = approaches,
    choice = choice,
    log = log,
    delegations = delegations,
    stage = stage,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DesignPageTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @Before
    fun useUtc() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    @Test
    fun aDesignWithNoStageDrawsOnlyTheStageNoteForItsStageParts() {
        val contracts = listOf(contract("example-a", 1L, state = "accepted"))
        val delegation = DesignDelegationRow(
            seq = 1L,
            kinds = listOf("question"),
            message = "Decide the rest for me",
            session = "example-session",
            time = BASE,
            decisions = listOf(DesignDecisionRow(kind = "question", ref = "Q1", text = "use the table", reason = "it fits", time = BASE)),
        )

        draw(record(design = design(contracts = contracts), stage = null, delegations = listOf(delegation)))

        composeRule.onNodeWithTag(DESIGN_STAGE_NOTE_TAG).assertTextEquals("The design's stage is not served.")
        STAGE_NAMES.forEach { name -> assertNoNode(DESIGN_STAGE_TAG_PREFIX + name) }
        listOf("steelman", "questions", "research").forEach { name -> assertNoNode(DESIGN_SECTION_TAG_PREFIX + name) }
        assertNoNode(DESIGN_BUDGET_TAG_PREFIX + "discovery")
        assertOneNode(DESIGN_SECTION_TAG_PREFIX + "decided")
        assertOneNode(DESIGN_SECTION_TAG_PREFIX + "contracts")
    }

    @Test
    fun aDesignWithNothingYetIsAtResearchAndEveryLaterStageIsNotReached() {
        draw(record(stage = stage()))

        assertStrip("now", "ahead", "ahead", "ahead", "ahead", "ahead")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Questions").assertTextEquals("Questions", "not reached")
    }

    @Test
    fun aQuestionPutsTheDesignAtQuestionsAndSaysHowManyAreSettled() {
        val questions = listOf(question(1L, answer = "use the table"), question(2L))

        draw(record(stage = stage(questions = questions)))

        assertStrip("done", "now", "ahead", "ahead", "ahead", "ahead")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Questions").assertTextEquals("Questions", "1 of 2 settled")
    }

    @Test
    fun anApproachPutsTheDesignAtDesign() {
        val approaches = listOf(approach(1L, "Use the table", shape = "A table", cost = "Cheap", recommended = true))

        draw(record(stage = stage(questions = listOf(question(1L, answer = "use the table"))), approaches = approaches))

        assertStrip("done", "done", "now", "ahead", "ahead", "ahead")
    }

    @Test
    fun aDiscoveryStepPutsTheDesignAtSteelmanWithItsVerdict() {
        val steps = listOf(step("example-discovery", "discovery", verdict = "READY", counted = true))

        draw(record(stage = stage(steps = steps)))

        assertStrip("done", "done", "done", "now", "ahead", "ahead")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Steelman").assertTextEquals("Steelman", "ready")
    }

    @Test
    fun aClosureStepPutsTheDesignAtClosureWithItsVerdict() {
        val steps = listOf(
            step("example-discovery", "discovery", verdict = "READY", counted = true),
            step("example-closure", "closure", verdict = "READY", counted = true),
        )

        draw(record(stage = stage(steps = steps)))

        assertStrip("done", "done", "done", "done", "now", "ahead")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Closure").assertTextEquals("Closure", "ready")
    }

    @Test
    fun aSkippedClosureAlsoPutsTheDesignAtClosureAndSaysSkipped() {
        val steps = listOf(step("example-discovery", "discovery", verdict = "READY", counted = true))

        draw(record(stage = stage(steps = steps, closureSkipped = true)))

        assertStrip("done", "done", "done", "done", "now", "ahead")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Closure").assertTextEquals("Closure", "skipped")
    }

    @Test
    fun aProposedContractPutsTheDesignAtProposed() {
        val steps = listOf(
            step("example-discovery", "discovery", verdict = "READY", counted = true),
            step("example-closure", "closure", verdict = "READY", counted = true),
        )
        val contracts = listOf(contract("example-a", 1L, state = "proposed"))

        draw(record(design = design(contracts = contracts), stage = stage(steps = steps)))

        assertStrip("done", "done", "done", "done", "done", "now")
    }

    @Test
    fun aDesignWithEveryContractAcceptedHasEveryStageDone() {
        val contracts = listOf(contract("example-a", 1L, state = "accepted"), contract("example-b", 2L, state = "accepted"))

        draw(record(design = design(contracts = contracts), stage = stage()))

        assertStrip("done", "done", "done", "done", "done", "done")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Proposed").assertTextEquals("✓ Proposed")
    }

    @Test
    fun researchReportsDrawTheirSubjectStatusAndReportForResearchAndReproduceOnly() {
        val steps = listOf(
            step("example-research", "research", status = "returned", subject = "Where is the queue?", report = "The queue is in queue.go"),
            step("example-reproduce", "reproduce", status = "running", subject = "go test ./queue"),
            step("example-discovery", "discovery", status = "running"),
        )

        draw(record(stage = stage(steps = steps)))

        composeRule.onNodeWithTag(DESIGN_RESEARCH_TAG_PREFIX + "example-research")
            .assertTextEquals("Where is the queue?", "returned", "The queue is in queue.go")
        composeRule.onNodeWithTag(DESIGN_RESEARCH_TAG_PREFIX + "example-reproduce").assertTextEquals("go test ./queue", "running")
        assertNoNode(DESIGN_RESEARCH_TAG_PREFIX + "example-discovery")
        composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + "Research").assertTextEquals("✓ Research", "1 of 2 returned")
    }

    @Test
    fun theBudgetCellsAreLeftWhenNoPassHasStarted() {
        draw(record(stage = stage()))

        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "discovery")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "left"))
            .assertTextEquals("Discovery · 1", "1 left · the Director runs it when the drafts are whole")
        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "closure")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "left"))
            .assertTextEquals("Closure · at most 1", "1 left · after discovery")
    }

    @Test
    fun aRunningDiscoveryIsRunningAndSaysForHowLong() {
        draw(record(stage = stage(steps = listOf(step("example-discovery", "discovery", status = "running")))), now = NOW)

        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "discovery")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "running"))
            .assertTextEquals("Discovery · 1", "running · 1 min")
        composeRule.onNodeWithTag(DESIGN_PASS_TAG_PREFIX + "example-discovery").assertTextEquals("Discovery is running · 1 min")
    }

    @Test
    fun aCountedDiscoveryIsUsedWithItsVerdictAndClosureIsLeftAfterRevising() {
        val steps = listOf(step("example-discovery", "discovery", verdict = "READY", counted = true))

        draw(record(stage = stage(steps = steps)))

        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "discovery")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "used"))
            .assertTextEquals("Discovery · 1", "used · 22:13 · ready")
        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "closure")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "left"))
            .assertTextEquals("Closure · at most 1", "1 left · the Director runs it after revising")
        composeRule.onNodeWithTag(DESIGN_PASS_TAG_PREFIX + "example-discovery").assertTextEquals("Discovery returned ready · 22:13")
    }

    @Test
    fun aDiscoveryThatDidNotCountIsNotCountedAndSaysItsVerdict() {
        val steps = listOf(step("example-discovery", "discovery", verdict = "BLOCKED", counted = false))

        draw(record(stage = stage(steps = steps)))

        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "discovery")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "not counted"))
            .assertTextEquals("Discovery · 1", "not counted · blocked")
        composeRule.onNodeWithTag(DESIGN_PASS_TAG_PREFIX + "example-discovery").assertTextEquals("Discovery returned blocked · 22:13")
    }

    @Test
    fun aSkippedClosureWillNotBeUsed() {
        val steps = listOf(step("example-discovery", "discovery", verdict = "READY", counted = true))

        draw(record(stage = stage(steps = steps, closureSkipped = true)))

        composeRule.onNodeWithTag(DESIGN_BUDGET_TAG_PREFIX + "closure")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "will not be used"))
            .assertTextEquals("Closure · at most 1", "closure was skipped")
    }

    @Test
    fun theLedgerDrawsEachDispositionWithItsMakerMarkAndSaysItIsFrozen() {
        val ledger = listOf(
            item("F1", disposition = "open"),
            item("F2", disposition = "adopted", kind = "owner_serve", closureWord = "satisfied", closureNote = "checked"),
            item("F3", disposition = "rejected", kind = "conversation", reason = "Out of scope"),
            item("F4", disposition = "deferred", kind = "orreryd", boundary = "after the migration"),
        )

        draw(record(stage = stage(ledger = ledger, frozen = true)))

        composeRule.onNodeWithTag(DESIGN_LEDGER_TAG)
            .assertTextEquals("LEDGER", "4 findings · 1 open · 1 adopted · 1 rejected · 1 deferred", "frozen")
        composeRule.onNodeWithTag(DESIGN_LEDGER_ITEM_TAG_PREFIX + "F1")
            .assert(SemanticsMatcher.keyNotDefined(MakerKey))
            .assertTextEquals("F1", "Impact F1", "Evidence: Evidence F1", "Change: Change F1", "open")
        composeRule.onNodeWithTag(DESIGN_LEDGER_ITEM_TAG_PREFIX + "F2")
            .assert(SemanticsMatcher.expectValue(MakerKey, "you"))
            .assertTextEquals("F2", "Impact F2", "Evidence: Evidence F2", "Change: Change F2", "adopted", "you", "closure · satisfied · checked")
        composeRule.onNodeWithTag(DESIGN_LEDGER_ITEM_TAG_PREFIX + "F3")
            .assert(SemanticsMatcher.expectValue(MakerKey, "director"))
            .assertTextEquals("F3", "Impact F3", "Evidence: Evidence F3", "Change: Change F3", "rejected because: Out of scope", "the Director")
        composeRule.onNodeWithTag(DESIGN_LEDGER_ITEM_TAG_PREFIX + "F4")
            .assert(SemanticsMatcher.expectValue(MakerKey, "orreryd"))
            .assertTextEquals("F4", "Impact F4", "Evidence: Evidence F4", "Change: Change F4", "deferred until after the migration", "orreryd")
    }

    @Test
    fun theChosenApproachIsMarkedWithItsMakerAndWords() {
        val approaches = listOf(
            approach(1L, "Use the table", shape = "A table", cost = "Cheap", recommended = true, reason = "it fits the repo"),
            approach(2L, "Use a list", shape = "A list", cost = "Dear"),
        )
        val choice = DesignChoiceRow(approach = 2L, words = "use the list", maker = RecordMakerRow(kind = "owner_serve"), time = BASE)

        draw(record(stage = stage(), approaches = approaches, choice = choice))

        composeRule.onNodeWithTag(DESIGN_APPROACH_TAG_PREFIX + "1")
            .assertTextEquals("Use the table", "recommended", "Shape: A table", "Costs: Cheap", "Recommended because: it fits the repo")
        composeRule.onNodeWithTag(DESIGN_APPROACH_TAG_PREFIX + "2")
            .assertTextEquals("Use a list", "chosen", "Shape: A list", "Costs: Dear")
        composeRule.onNodeWithTag(DESIGN_CHOSEN_TAG)
            .assert(SemanticsMatcher.expectValue(MakerKey, "you"))
            .assertTextEquals("Chosen · Use a list", "you", "22:13", "“use the list”")
    }

    @Test
    fun theQuestionsAreDrawnThroughTheThreadWithTheOpenOnesFirst() {
        val questions = listOf(question(1L, answer = "use the table"), question(2L), question(3L))

        draw(
            record(stage = stage(questions = questions)),
            thread = { question -> Text("thread ${question.key}", modifier = Modifier.testTag("thread:${question.key}")) },
        )

        // The count is in the strip's Questions stage and in the section's head.
        composeRule.onAllNodesWithText("1 of 3 settled").assertCountEquals(2)
        assertEquals(listOf("thread:Q2", "thread:Q3", "thread:Q1"), childTagsOf(DESIGN_SECTION_TAG_PREFIX + "questions"))
    }

    @Test
    fun theLogIsNewestFirstWithItsMakers() {
        val log = listOf(
            DesignLogRow(seq = 1L, time = BASE, maker = RecordMakerRow(kind = "owner_serve"), what = "created", ids = listOf("example-design"), words = "Let's design it"),
            DesignLogRow(seq = 2L, time = BASE, maker = RecordMakerRow(kind = "conversation"), what = "asked", ids = listOf("Q1"), words = "Which way?"),
            DesignLogRow(seq = 3L, time = BASE, maker = null, what = "proposed", ids = listOf("example-a")),
        )

        draw(record(stage = stage(), log = log))

        assertEquals(listOf("design-log:3", "design-log:2", "design-log:1"), childTagsOf(DESIGN_SECTION_TAG_PREFIX + "log"))
        composeRule.onNodeWithTag(DESIGN_LOG_TAG_PREFIX + "3").assert(SemanticsMatcher.expectValue(MakerKey, "orreryd"))
        composeRule.onNodeWithTag(DESIGN_LOG_TAG_PREFIX + "2")
            .assert(SemanticsMatcher.expectValue(MakerKey, "director"))
            .assertTextEquals("22:13", "the Director", "asked · Q1", "Which way?")
        composeRule.onNodeWithTag(DESIGN_LOG_TAG_PREFIX + "1")
            .assert(SemanticsMatcher.expectValue(MakerKey, "you"))
            .assertTextEquals("22:13", "you", "created · example-design", "“Let's design it”")
    }

    @Test
    fun aDecisionTheDirectorMadeUnderADelegationIsMarkedAsTheDirectors() {
        val delegation = DesignDelegationRow(
            seq = 1L,
            kinds = listOf("question", "finding"),
            message = "Decide the rest for me",
            session = "example-session",
            time = BASE,
            decisions = listOf(
                DesignDecisionRow(kind = "question", ref = "Q2", text = "use the table", reason = "it is the recommendation", time = BASE),
            ),
        )

        draw(record(stage = stage(), delegations = listOf(delegation)))

        composeRule.onNodeWithTag(DESIGN_DELEGATION_TAG_PREFIX + "1")
            .assert(SemanticsMatcher.expectValue(MakerKey, "you"))
            .assertTextEquals("Delegated by you · 22:13", "“Decide the rest for me”", "question · finding")
        composeRule.onNodeWithTag(DESIGN_DECISION_TAG_PREFIX + "1:0")
            .assert(SemanticsMatcher.expectValue(MakerKey, "director"))
            .assertTextEquals("question · Q2", "the Director", "use the table", "it is the recommendation")
    }

    @Test
    fun theContractsAreDrawnInStartOrderWithWhatTheyStartAfterAndTheirEpics() {
        val contracts = listOf(
            contract("example-c2", 2L, state = "proposed", after = listOf("example-c1")),
            contract("example-c1", 1L, state = "accepted", epic = "example-epic", epicPhase = "building"),
        )

        draw(record(design = design(contracts = contracts), stage = stage()))

        assertEquals(
            listOf("design-contract:example-c1", "design-contract:example-c2"),
            childTagsOf(DESIGN_SECTION_TAG_PREFIX + "contracts"),
        )
        composeRule.onNodeWithTag(DESIGN_CONTRACT_TAG_PREFIX + "example-c1")
            .assertTextEquals("example-c1", "Example example-c1", "starts first", "epic example-epic · building", "accepted")
        composeRule.onNodeWithTag(DESIGN_CONTRACT_TAG_PREFIX + "example-c2")
            .assertTextEquals("example-c2", "Example example-c2", "after example-c1", "proposed")
    }

    @Test
    fun tappingAContractOpensThatContract() {
        val contracts = listOf(contract("example-c1", 1L, state = "accepted"), contract("example-c2", 2L, state = "proposed"))
        var opened: DesignContractRow? = null

        draw(record(design = design(contracts = contracts), stage = stage()), onOpenContract = { opened = it })
        composeRule.onNodeWithTag(DESIGN_CONTRACT_TAG_PREFIX + "example-c2").performScrollTo().performClick()

        assertEquals("example-c2", opened?.id)
    }

    @Test
    fun theNowSectionSaysWhatTheDesignIsDoingAndEachOpenQuestion() {
        val questions = listOf(question(1L, answer = "use the table"), question(2L, text = "Which store?"))

        draw(record(design = design(state = "drafting: 1 of 2 accepted"), stage = stage(questions = questions)))

        composeRule.onAllNodesWithText("drafting: 1 of 2 accepted").assertCountEquals(1)
        composeRule.onNodeWithTag(DESIGN_NEED_TAG_PREFIX + "Q2").assertTextEquals("Q2 · Which store?")
        assertNoNode(DESIGN_NEED_TAG_PREFIX + "Q1")
    }

    @Test
    fun aPartWithNothingInTheRecordIsNotDrawn() {
        draw(record(stage = stage()))

        listOf("now", "decided", "contracts", "approach", "questions", "research", "log").forEach { name ->
            assertNoNode(DESIGN_SECTION_TAG_PREFIX + name)
        }
        assertNoNode(DESIGN_LEDGER_TAG)
        assertOneNode(DESIGN_SECTION_TAG_PREFIX + "steelman")
    }

    private fun draw(
        record: DesignRecordRow,
        now: Long = NOW,
        thread: @Composable (DesignQuestionRow) -> Unit = {},
        onOpenContract: (DesignContractRow) -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme { DesignPage(record = record, now = now, thread = thread, onOpenContract = onOpenContract) }
        }
    }

    // assertStrip checks the six stages' states, in strip order: done, now or ahead.
    private fun assertStrip(vararg states: String) {
        STAGE_NAMES.zip(states.toList()).forEach { (name, state) ->
            composeRule.onNodeWithTag(DESIGN_STAGE_TAG_PREFIX + name)
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state))
        }
    }

    // assertNoNode checks that no node carries tag, and assertOneNode that exactly one does.
    private fun assertNoNode(tag: String) = composeRule.onAllNodesWithTag(tag).assertCountEquals(0)

    private fun assertOneNode(tag: String) = composeRule.onAllNodesWithTag(tag).assertCountEquals(1)

    // childTagsOf is the test tags of the nodes directly under the node tagged tag, in the order they are drawn.
    private fun childTagsOf(tag: String): List<String> =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().children
            .mapNotNull { it.config.getOrNull(SemanticsProperties.TestTag) }
}
