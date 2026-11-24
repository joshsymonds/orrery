package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import io.github.joshsymonds.orrery.MainThreadComposeRule
import io.github.joshsymonds.orrery.api.ContractDecisionRow
import io.github.joshsymonds.orrery.api.ContractLengthRow
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractRefusalRow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ContractRowWriteRow
import io.github.joshsymonds.orrery.api.ContractShownRow
import io.github.joshsymonds.orrery.api.ContractStageRow
import io.github.joshsymonds.orrery.api.ContractVersionRow
import io.github.joshsymonds.orrery.api.ContractWorkingRow
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.EpicAdditionRow
import io.github.joshsymonds.orrery.api.EpicAlternativeRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicFailureRow
import io.github.joshsymonds.orrery.api.EpicPremiseRow
import io.github.joshsymonds.orrery.api.EpicReleaseStepRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.RecordMakerRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// Unix seconds. Nothing in these tests reads the clock: every time is a fixture, and no time is drawn.
private const val CP_TIME = 1_700_000_000L
private const val CP_CONTRACT_ID = "tidepool/tide-alerts"
private const val CP_TITLE = "Synthetic contract title"

private val cpSectionKeys = listOf(
    "intent", "failures", "additions", "requirements", "premises",
    "exclusions", "approach", "steps", "decisions", "checks",
)

private val cpRequirement = EpicRequirementRow(
    id = "R1",
    text = "Synthetic requirement text",
    evidence = "Synthetic requirement evidence",
    kind = "check",
)

private val cpAddition = EpicAdditionRow(
    location = "Synthetic location",
    what = "Synthetic addition what",
    why = "Synthetic addition why",
    cost = "Synthetic addition cost",
)

private val cpOtherAddition = cpAddition.copy(what = "Synthetic other addition what")

private val cpSteps = listOf(
    EpicReleaseStepRow(step = "Synthetic step one", target = "Synthetic target one", undo = "git revert"),
    EpicReleaseStepRow(step = "Synthetic step two", target = "Synthetic target two", undo = "none"),
    EpicReleaseStepRow(step = "Synthetic step three", target = "Synthetic target three", undo = "none"),
)

private val cpOpenDecision = ContractDecisionRow(
    id = "D1",
    question = "Synthetic decision question",
    options = listOf("Synthetic option one", "Synthetic option two"),
    recommendation = "Synthetic option one",
    reason = "Synthetic decision reason",
    row = "F1",
    answer = "",
)

private val cpSettledDecision = cpOpenDecision.copy(id = "D2", question = "Synthetic settled question", answer = "Synthetic answer")

private fun cpContract(
    intent: String = "Synthetic intent text.",
    care: String = "limited",
    additions: List<EpicAdditionRow> = listOf(cpAddition),
    steps: List<EpicReleaseStepRow> = cpSteps,
) = EpicContractRow(
    title = CP_TITLE,
    approved = "",
    intent = intent,
    failures = listOf(
        EpicFailureRow(id = "F1", text = "Synthetic failure text", rating = "limited", response = "Synthetic failure response"),
    ),
    care = care,
    additions = additions,
    premises = listOf(EpicPremiseRow(id = "P1", text = "Synthetic premise text", ifWrong = "Synthetic premise if wrong")),
    exclusions = listOf("Synthetic exclusion text"),
    approach = "Synthetic approach text.",
    alternatives = listOf(
        EpicAlternativeRow(alternative = "Synthetic alternative", whyNot = "Synthetic why not", reconsider = "Synthetic reconsider"),
    ),
    releaseSteps = steps,
    releaseThen = "",
    checks = "Synthetic checks text.",
    base = "main",
    accepted = 0L,
)

private fun cpVersion(
    number: Long,
    contract: EpicContractRow = cpContract(),
    decisions: List<ContractDecisionRow> = emptyList(),
    changedRows: List<String> = emptyList(),
) = ContractVersionRow(
    version = number,
    contract = contract,
    requirements = listOf(cpRequirement),
    decisions = decisions,
    words = "",
    writer = "director",
    time = CP_TIME,
    changedRows = changedRows,
    changedSections = emptyList(),
)

private fun cpRecord(vararg versions: ContractVersionRow, state: String = "proposed"): ContractRecordRow {
    val newest = versions.last()
    return ContractRecordRow(
        contract = ContractRow(
            id = CP_CONTRACT_ID,
            repository = "tidepool",
            title = newest.contract.title,
            care = newest.contract.care,
            state = state,
            version = newest.version,
            openDecisions = 0,
            conversation = "conversation",
            created = CP_TIME,
            changed = CP_TIME,
        ),
        versions = versions.toList(),
        states = emptyList(),
    )
}

private fun cpQuestion(id: Long, answer: String = "") = DesignQuestionRow(
    design = "design",
    id = id,
    contract = CP_CONTRACT_ID,
    text = "Synthetic question $id",
    options = listOf("Synthetic choice"),
    recommendation = "Synthetic choice",
    asked = CP_TIME,
    answer = answer,
)

private fun cpWrite(seq: Long, row: String, action: String, words: String, kind: String) = ContractRowWriteRow(
    seq = seq,
    version = 1,
    row = row,
    action = action,
    words = words,
    maker = RecordMakerRow(kind = kind),
    time = CP_TIME,
)

// cpThread draws a question's thread as a node tagged with its key, which is all these tests need of it.
@Composable
private fun cpThread(question: DesignQuestionRow) {
    Text("thread ${question.key}", modifier = Modifier.testTag("thread:${question.key}"))
}

// setContractPage draws the contract page with the given parts, and every other part at its default.
private fun MainThreadComposeRule.setContractPage(
    record: ContractRecordRow,
    stage: ContractStageRow = ContractStageRow(),
    questions: List<DesignQuestionRow> = emptyList(),
    onKeep: (String) -> Unit = {},
    onStrike: (String) -> Unit = {},
    onAbout: ((RowAbout) -> Unit)? = null,
    work: (@Composable () -> Unit)? = null,
) {
    setContent {
        MaterialTheme {
            ContractPage(
                record = record,
                stage = stage,
                questions = questions,
                onKeep = onKeep,
                onStrike = onStrike,
                onShown = { _, _ -> },
                onAbout = onAbout,
                thread = { cpThread(it) },
                work = work,
                foot = {},
            )
        }
    }
}

// cpReach scrolls the page until the node tagged tag is in view, so a click reaches it.
private fun MainThreadComposeRule.cpReach(tag: String) {
    onNodeWithTag(CONTRACT_PAGE_TAG).performScrollToNode(hasTestTag(tag))
}

// cpTextIn asserts a text is drawn inside the node tagged tag.
private fun MainThreadComposeRule.cpTextIn(tag: String, text: String) {
    onAllNodes(hasText(text) and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true).assertCountEquals(1)
}

// cpCount is how many nodes carry the tag.
private fun MainThreadComposeRule.cpCount(tag: String): Int =
    onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().size

// cpTop is the top of the node tagged tag, in the root's coordinates. A node scrolled out of the page's view is clipped to
// the view's edge, so only nodes in view have a top that orders them; the tests that order nodes use cpTagsInOrder.
private fun MainThreadComposeRule.cpTop(tag: String): Float =
    onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.top

// cpTagsInOrder lists the test tags of the nodes whose tag starts with prefix, in the order the page draws them.
private fun MainThreadComposeRule.cpTagsInOrder(prefix: String): List<String> =
    onAllNodes(
        SemanticsMatcher("a tag starting with $prefix") { node ->
            node.config.getOrNull(SemanticsProperties.TestTag)?.startsWith(prefix) == true
        },
        useUnmergedTree = true,
    ).fetchSemanticsNodes().map { node -> node.config[SemanticsProperties.TestTag] }

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractPageTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun theDecisionLineShowsTitleCareDecisionsAndVersions() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1), cpVersion(2, decisions = listOf(cpOpenDecision))),
        )

        composeRule.cpTextIn(CONTRACT_DLINE_TAG, CP_TITLE)
        composeRule.cpTextIn(CONTRACT_DLINE_TAG, "limited")
        composeRule.cpTextIn(CONTRACT_DLINE_TAG, "Decisions needed: 1")
        composeRule.cpTextIn(CONTRACT_DLINE_TAG, "2 versions")
    }

    @Test
    fun theDecisionsSectionShowsEachDecisionsOptionsWithTheRecommendationMarked() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1, decisions = listOf(cpOpenDecision, cpSettledDecision))),
        )

        composeRule.cpTextIn(CONTRACT_DECISION_TAG_PREFIX + "D1", "Synthetic decision question")
        composeRule.cpTextIn(CONTRACT_OPTION_TAG_PREFIX + "D1:0", "Synthetic option one")
        composeRule.cpTextIn(CONTRACT_OPTION_TAG_PREFIX + "D1:1", "Synthetic option two")
        composeRule.cpTextIn(CONTRACT_OPTION_TAG_PREFIX + "D1:0", "recommended")
        composeRule.cpTextIn(CONTRACT_OPTION_TAG_PREFIX + "D2:0", "recommended")
        assertEquals(2, composeRule.onAllNodesWithText("recommended", useUnmergedTree = true).fetchSemanticsNodes().size)
        composeRule.cpTextIn(CONTRACT_DECISION_TAG_PREFIX + "D2", "Answer: Synthetic answer")
    }

    @Test
    fun openQuestionsComeFirstThenSettledOnesUnderSettled() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1)),
            questions = listOf(cpQuestion(2, answer = "Synthetic choice"), cpQuestion(1)),
        )

        val drawn = composeRule.onAllNodes(
            hasTestTag("thread:Q1") or hasText("Settled:") or hasTestTag("thread:Q2"),
            useUnmergedTree = true,
        ).fetchSemanticsNodes().map { node ->
            node.config.getOrNull(SemanticsProperties.TestTag) ?: node.config[SemanticsProperties.Text].single().text
        }
        assertEquals(listOf("thread:Q1", "Settled:", "thread:Q2"), drawn)
        composeRule.onAllNodesWithText("None.", useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun noOpenDecisionReadsNone() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1)),
            questions = listOf(cpQuestion(1, answer = "Synthetic choice")),
        )

        composeRule.onAllNodesWithText("None.", useUnmergedTree = true).assertCountEquals(1)
        composeRule.onAllNodesWithTag("thread:Q1", useUnmergedTree = true).assertCountEquals(1)
    }

    @Test
    fun eachUndecidedAdditionOffersKeepAndStrikeForItsId() {
        val kept = mutableListOf<String>()
        val struck = mutableListOf<String>()
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1, cpContract(additions = listOf(cpAddition, cpOtherAddition)))),
            onKeep = { kept += it },
            onStrike = { struck += it },
        )

        composeRule.cpReach(ADDITION_KEEP_TAG_PREFIX + "A1")
        composeRule.onNodeWithTag(ADDITION_KEEP_TAG_PREFIX + "A1").performClick()
        composeRule.cpReach(ADDITION_STRIKE_TAG_PREFIX + "A2")
        composeRule.onNodeWithTag(ADDITION_STRIKE_TAG_PREFIX + "A2").performClick()

        assertEquals(listOf("A1"), kept)
        assertEquals(listOf("A2"), struck)
        assertEquals(
            2,
            composeRule.onAllNodesWithText(
                "Asked in the conversation. Say “keep it” or “strike it” there, or press here.",
            ).fetchSemanticsNodes().size,
        )
    }

    @Test
    fun keptAndStruckRowsShowTheirMakersAndWords() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1, cpContract(additions = listOf(cpAddition, cpOtherAddition)))),
            stage = ContractStageRow(
                writes = listOf(
                    cpWrite(seq = 1, row = "A1", action = "keep", words = "keep it", kind = "owner_conversation"),
                    cpWrite(seq = 2, row = "A3", action = "strike", words = "strike it", kind = "conversation"),
                ),
            ),
        )

        composeRule.cpTextIn(ADDITION_ROW_TAG_PREFIX + "A1", "kept")
        composeRule.cpTextIn(ADDITION_ROW_TAG_PREFIX + "A1", "kept by you")
        composeRule.cpTextIn(ADDITION_ROW_TAG_PREFIX + "A1", "keep it")
        composeRule.onNodeWithTag(CONTRACT_WHO_TAG_PREFIX + "A1", useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(MakerKey, "you"))
        assertEquals(0, composeRule.cpCount(ADDITION_KEEP_TAG_PREFIX + "A1"))
        assertEquals(1, composeRule.cpCount(ADDITION_KEEP_TAG_PREFIX + "A2"))

        composeRule.cpTextIn(ADDITION_STRUCK_TAG_PREFIX + "A3", "A3")
        composeRule.cpTextIn(ADDITION_STRUCK_TAG_PREFIX + "A3", "struck")
        composeRule.cpTextIn(ADDITION_STRUCK_TAG_PREFIX + "A3", "struck by the Director")
        composeRule.cpTextIn(ADDITION_STRUCK_TAG_PREFIX + "A3", "strike it")
        composeRule.onNodeWithTag(CONTRACT_WHO_TAG_PREFIX + "A3", useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(MakerKey, "director"))
    }

    @Test
    fun aFrozenContractOffersNoKeepOrStrikeAndSaysDecisionsNone() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1, cpContract(), decisions = listOf(cpOpenDecision)), state = "accepted"),
        )

        assertEquals(0, composeRule.cpCount(ADDITION_KEEP_TAG_PREFIX + "A1"))
        assertEquals(0, composeRule.cpCount(ADDITION_STRIKE_TAG_PREFIX + "A1"))
        composeRule.cpTextIn(ADDITION_ROW_TAG_PREFIX + "A1", "not decided")
        composeRule.cpTextIn(CONTRACT_DLINE_TAG, "Decisions needed: none")
    }

    @Test
    fun stepsThatCannotBeUndoneAreMarkedWithTheirCount() {
        composeRule.setContractPage(record = cpRecord(cpVersion(1)))

        composeRule.onAllNodesWithText("3 steps · 2 cannot be undone", useUnmergedTree = true).assertCountEquals(1)
        assertEquals(1, composeRule.cpCount(STEP_UNDO_NONE_TAG_PREFIX + "2"))
        assertEquals(1, composeRule.cpCount(STEP_UNDO_NONE_TAG_PREFIX + "3"))
        assertEquals(0, composeRule.cpCount(STEP_UNDO_NONE_TAG_PREFIX + "1"))
        composeRule.onNodeWithTag(STEP_UNDO_NONE_TAG_PREFIX + "2", useUnmergedTree = true).assertTextEquals("none: cannot be undone")
    }

    @Test
    fun readBeforeAcceptTurnsToYouReadThisOnceTheRowsAreShown() {
        val reported = mutableListOf<Pair<Long, List<String>>>()
        composeRule.setContent {
            var shownRows by remember { mutableStateOf(emptyList<ContractShownRow>()) }
            MaterialTheme {
                ContractPage(
                    record = cpRecord(cpVersion(1)),
                    stage = ContractStageRow(shown = shownRows),
                    questions = emptyList(),
                    onKeep = {},
                    onStrike = {},
                    onShown = { version, rows ->
                        reported += version to rows
                        shownRows = shownRows + rows.map { ContractShownRow(version = version, row = it) }
                    },
                    onAbout = null,
                    thread = { cpThread(it) },
                    work = null,
                    foot = {},
                )
            }
        }

        composeRule.waitForIdle()
        assertEquals(listOf(1L to listOf("step2", "step3")), reported)
        composeRule.onAllNodesWithText("you read this", useUnmergedTree = true).assertCountEquals(1)
        composeRule.onAllNodesWithText("read this before Accept", useUnmergedTree = true).assertCountEquals(0)
        composeRule.waitForIdle()
        assertEquals(1, reported.size)
    }

    @Test
    fun onShownIsNotCalledForRowsAlreadyShown() {
        val reported = mutableListOf<Pair<Long, List<String>>>()
        composeRule.setContent {
            MaterialTheme {
                ContractPage(
                    record = cpRecord(cpVersion(1)),
                    stage = ContractStageRow(
                        shown = listOf(
                            ContractShownRow(version = 1, row = "step2", time = CP_TIME),
                            ContractShownRow(version = 1, row = "step3", time = CP_TIME),
                        ),
                    ),
                    questions = emptyList(),
                    onKeep = {},
                    onStrike = {},
                    onShown = { version, rows -> reported += version to rows },
                    onAbout = null,
                    thread = { cpThread(it) },
                    work = null,
                    foot = {},
                )
            }
        }

        composeRule.waitForIdle()
        composeRule.onAllNodesWithText("you read this", useUnmergedTree = true).assertCountEquals(1)
        assertEquals(emptyList<Pair<Long, List<String>>>(), reported)
    }

    @Test
    fun theLengthSignalIsOkBelowTheWarningLine() {
        assertLengthSignal(lines = 40, warn = 41, strong = 61, words = "40 lines", level = LengthLevel.OK)
    }

    @Test
    fun theLengthSignalIsLongFromTheWarningLine() {
        assertLengthSignal(lines = 41, warn = 41, strong = 61, words = "41 lines · long", level = LengthLevel.WARN)
    }

    @Test
    fun theLengthSignalIsVeryLongFromTheStrongLine() {
        assertLengthSignal(lines = 61, warn = 41, strong = 61, words = "61 lines · very long", level = LengthLevel.CRIT)
    }

    @Test
    fun theLengthSignalIsNotDrawnWithoutALength() {
        composeRule.setContractPage(record = cpRecord(cpVersion(1)), stage = ContractStageRow(length = null))

        assertEquals(0, composeRule.cpCount(CONTRACT_LENGTH_TAG))
    }

    @Test
    fun theWorkIsDrawnAboveTheSectionsWhenGiven() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1)),
            work = { Text("Synthetic work", modifier = Modifier.testTag("synthetic-work")) },
        )

        val workTop = composeRule.cpTop("synthetic-work")
        assertTrue(composeRule.cpTop(CONTRACT_DLINE_TAG) < workTop)
        assertTrue(workTop < composeRule.cpTop(CONTRACT_SECTION_TAG_PREFIX + "intent"))
    }

    @Test
    fun theTenSectionsAreDrawnInTheDocumentsOrder() {
        composeRule.setContractPage(record = cpRecord(cpVersion(1)))

        assertEquals(cpSectionKeys.map { CONTRACT_SECTION_TAG_PREFIX + it }, composeRule.cpTagsInOrder(CONTRACT_SECTION_TAG_PREFIX))
    }

    @Test
    fun eachRatingShowsWhoRatedIt() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1)),
            stage = ContractStageRow(writes = listOf(cpWrite(seq = 1, row = "F1", action = "write", words = "", kind = "conversation"))),
        )

        composeRule.cpTextIn(CONTRACT_ROW_TAG_PREFIX + "F1", "limited")
        composeRule.onNodeWithTag(CONTRACT_WHO_TAG_PREFIX + "F1", useUnmergedTree = true).assertTextEquals("rated by the Director")
        composeRule.onNodeWithTag(CONTRACT_WHO_TAG_PREFIX + "F1", useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(MakerKey, "director"))
    }

    @Test
    fun theWorkingCopyIsTheBodyDrawnWhenThereIsOne() {
        val working = ContractWorkingRow(
            version = 2,
            contract = cpContract(intent = "Synthetic working intent."),
            requirements = listOf(cpRequirement),
            decisions = emptyList(),
            writer = "director",
            updated = CP_TIME,
            changedRows = listOf("F1"),
        )
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1, cpContract(intent = "Synthetic newest intent."))),
            stage = ContractStageRow(working = working),
        )

        composeRule.cpTextIn(CONTRACT_SECTION_TAG_PREFIX + "intent", "Synthetic working intent.")
        assertEquals(0, composeRule.onAllNodesWithText("Synthetic newest intent.", useUnmergedTree = true).fetchSemanticsNodes().size)
        composeRule.cpTextIn(CONTRACT_ROW_TAG_PREFIX + "F1", "changed in v2")
    }

    @Test
    fun eachRowsTalkControlAsksAboutThatRowWhileProposed() {
        val asked = mutableListOf<RowAbout>()
        composeRule.setContractPage(record = cpRecord(cpVersion(1)), onAbout = { asked += it })

        composeRule.cpReach(CONTRACT_TALK_TAG_PREFIX + "F1")
        composeRule.onNodeWithTag(CONTRACT_TALK_TAG_PREFIX + "F1").performClick()
        composeRule.cpReach(CONTRACT_TALK_TAG_PREFIX + "P1")
        composeRule.onNodeWithTag(CONTRACT_TALK_TAG_PREFIX + "P1").performClick()

        assertEquals(
            listOf(RowAbout("F1", "Synthetic failure text"), RowAbout("P1", "Synthetic premise text")),
            asked,
        )
    }

    @Test
    fun withoutAnAboutThereIsNoTalkControl() {
        composeRule.setContractPage(record = cpRecord(cpVersion(1)), onAbout = null)

        assertEquals(0, composeRule.cpCount(CONTRACT_TALK_TAG_PREFIX + "F1"))
    }

    @Test
    fun aRefusalIsShownUnderTheRowItRefused() {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1)),
            stage = ContractStageRow(
                refusals = listOf(
                    ContractRefusalRow(
                        seq = 1,
                        version = 1,
                        field = "F1",
                        text = "F1: the text is not a failure",
                        session = "session",
                        time = CP_TIME,
                    ),
                ),
            ),
        )

        composeRule.cpTextIn(CONTRACT_ROW_TAG_PREFIX + "F1", "Refused by the checks: the text is not a failure")
    }

    private fun assertLengthSignal(lines: Long, warn: Long, strong: Long, words: String, level: LengthLevel) {
        composeRule.setContractPage(
            record = cpRecord(cpVersion(1)),
            stage = ContractStageRow(length = ContractLengthRow(lines = lines, warn = warn, strong = strong)),
        )

        composeRule.onNodeWithTag(CONTRACT_LENGTH_TAG, useUnmergedTree = true).assertTextEquals(words)
        composeRule.onNodeWithTag(CONTRACT_LENGTH_TAG, useUnmergedTree = true)
            .assert(SemanticsMatcher.expectValue(LengthLevelKey, level))
        composeRule.onNodeWithTag(CONTRACT_LENGTH_TAG, useUnmergedTree = true).assert(
            SemanticsMatcher.expectValue(
                SemanticsProperties.ContentDescription,
                listOf("$lines lines of rows and prose above the checks. Long from $warn, very long from $strong; never refused."),
            ),
        )
    }
}
