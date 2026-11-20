package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performScrollToNode
import io.github.joshsymonds.orrery.api.ContractDecisionRow
import io.github.joshsymonds.orrery.api.EpicAdditionRow
import io.github.joshsymonds.orrery.api.EpicAlternativeRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicFailureRow
import io.github.joshsymonds.orrery.api.EpicPremiseRow
import io.github.joshsymonds.orrery.api.EpicReleaseStepRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.MainThreadComposeRule
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

internal const val BODY_TAG = "contract-body"

// Unix seconds 1_700_000_000 is 22:13 in UTC, the zone these tests run in.
internal const val ACCEPTED = 1_700_000_000L

internal val SECTION_NAMES = listOf(
    "What you asked for",
    "What could go wrong, and how much we care",
    "Things you did not ask for",
    "What will be true when done",
    "What I’m assuming",
    "What we won’t do",
    "How, and why not the other ways",
    "What leaves this machine or can’t be undone",
    "Decisions I need from you",
    "Checks the machines run",
)

internal val ADDITION = EpicAdditionRow(
    location = "Synthetic addition location",
    what = "Synthetic addition what",
    why = "Synthetic addition why",
    cost = "Synthetic addition cost",
)

internal val REQUIREMENT = EpicRequirementRow(
    id = "R1",
    text = "Synthetic requirement text",
    evidence = "Synthetic requirement evidence",
    kind = "check",
)

internal val OPEN_DECISION = ContractDecisionRow(
    id = "D1",
    question = "Synthetic decision question",
    options = listOf("Synthetic option"),
    recommendation = "Synthetic recommendation",
    reason = "Synthetic decision reason",
    row = "F1",
    answer = "",
)

internal val SETTLED_DECISION = OPEN_DECISION.copy(id = "D2", question = "Synthetic settled question", answer = "Synthetic answer")

internal fun syntheticContract(
    additions: List<EpicAdditionRow> = listOf(ADDITION),
    care: String = "limited",
    base: String = "main",
) = EpicContractRow(
    title = "Synthetic contract title",
    approved = "Synthetic approved line",
    intent = "Synthetic intent text.",
    failures = listOf(
        EpicFailureRow(id = "F1", text = "Synthetic failure text", rating = "limited", response = "Synthetic failure response"),
        EpicFailureRow(id = "F2", text = "Synthetic serious failure", rating = "serious", response = "Synthetic serious response"),
        EpicFailureRow(id = "F3", text = "Synthetic severe failure", rating = "severe", response = "Synthetic severe response"),
    ),
    care = care,
    additions = additions,
    premises = listOf(
        EpicPremiseRow(id = "P1", text = "Synthetic premise text", ifWrong = "Synthetic premise if wrong"),
    ),
    exclusions = listOf("Synthetic exclusion text"),
    approach = "Synthetic approach text.",
    alternatives = listOf(
        EpicAlternativeRow(alternative = "Synthetic alternative", whyNot = "Synthetic why not", reconsider = "Synthetic reconsider"),
    ),
    releaseSteps = listOf(
        EpicReleaseStepRow(step = "Synthetic release step", target = "Synthetic release target", undo = "Synthetic release undo"),
    ),
    releaseThen = "Synthetic release then text.",
    checks = "Synthetic checks text.",
    base = base,
    accepted = ACCEPTED,
)

// setContractBody draws the body with the given parts, and every other part at its Epics.Get default.
internal fun MainThreadComposeRule.setContractBody(
    contract: EpicContractRow = syntheticContract(),
    decisions: List<ContractDecisionRow>? = null,
    marks: DocMarks? = null,
    onAbout: ((RowAbout) -> Unit)? = null,
    onIrreversibleShown: (() -> Unit)? = null,
) {
    setContent {
        MaterialTheme {
            ContractBody(
                contract = contract,
                requirements = listOf(REQUIREMENT),
                decisions = decisions,
                marks = marks,
                onAbout = onAbout,
                onIrreversibleShown = onIrreversibleShown,
            )
        }
    }
}

// scrollTo brings the node with the tag into the body's view.
internal fun MainThreadComposeRule.scrollTo(tag: String) {
    onNodeWithTag(BODY_TAG).performScrollToNode(hasTestTag(tag))
}

// assertShownIn asserts a text is drawn inside the node with the tag, which it scrolls into view first.
internal fun MainThreadComposeRule.assertShownIn(tag: String, text: String) {
    scrollTo(tag)
    onNode(hasText(text) and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true).assertIsDisplayed()
}

// assertShows asserts a text is drawn in the body, which it scrolls to first.
internal fun MainThreadComposeRule.assertShows(text: String) {
    onNodeWithTag(BODY_TAG).performScrollToNode(hasText(text))
    onNodeWithText(text).assertIsDisplayed()
}

// assertNoControlIn scrolls the row into view, so the row is composed, and asserts the control tagged controlTag is not in it.
internal fun MainThreadComposeRule.assertNoControlIn(rowTag: String, controlTag: String) {
    scrollTo(rowTag)
    assertEquals(0, onAllNodesWithTag(controlTag).fetchSemanticsNodes().size)
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractDocTest {
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
    fun theTenSectionsAreInTheDocumentsOrder() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION))

        SECTION_NAMES.forEachIndexed { index, name ->
            val tag = "contract-section:$index"
            composeRule.scrollTo(tag)
            composeRule.onNode(
                hasText(name, ignoreCase = true) and hasAnyAncestor(hasTestTag(tag)),
                useUnmergedTree = true,
            ).assertIsDisplayed()
        }
    }

    @Test
    fun eachTableShowsItsColumnNames() {
        composeRule.setContractBody()

        listOf(
            "if this happened", "how bad", "what we do",
            "where", "what", "why", "cost",
            "must be true", "how we’ll know",
            "assumption", "if wrong",
            "alternative", "why not", "reconsider when",
            "step", "target", "undo",
        ).forEach { name -> composeRule.assertShows(name) }
    }

    @Test
    fun theDecisionsTableShowsItsColumnNames() {
        composeRule.setContractBody(contract = syntheticContract(additions = emptyList()), decisions = listOf(OPEN_DECISION))

        listOf("question", "recommended", "why").forEach { name -> composeRule.assertShows(name) }
    }

    @Test
    fun everyFieldIsShownAsItsOwnText() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION))

        listOf(
            "Synthetic contract title",
            "Synthetic approved line",
            "Synthetic intent text.",
            "F1",
            "Synthetic failure text",
            "Synthetic failure response",
            "Level of care: limited, set by F1",
            "Synthetic addition location",
            "Synthetic addition what",
            "Synthetic addition why",
            "Synthetic addition cost",
            "R1",
            "Synthetic requirement text",
            "Synthetic requirement evidence",
            "P1",
            "Synthetic premise text",
            "Synthetic premise if wrong",
            "Synthetic exclusion text",
            "Synthetic approach text.",
            "Synthetic alternative",
            "Synthetic why not",
            "Synthetic reconsider",
            "Synthetic release step",
            "Synthetic release target",
            "Synthetic release undo",
            "Synthetic release then text.",
            "Synthetic checks text.",
            "Synthetic decision question",
            "Synthetic decision reason",
            "Synthetic recommendation",
        ).forEach { text -> composeRule.assertShows(text) }
    }

    @Test
    fun ratingChipsCarryTheToneOfTheirRating() {
        composeRule.setContractBody(contract = syntheticContract(care = "severe"), decisions = listOf(OPEN_DECISION))

        assertToneOf("contract-row:F1", "limited", ChipTone.Neutral)
        assertToneOf("contract-row:F2", "serious", ChipTone.Live)
        assertToneOf("contract-row:F3", "severe", ChipTone.Bad)
    }

    @Test
    fun idChipsCarryTheKindOfTheirIdAsTheFirstLetter() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION))

        listOf("F1" to "F", "R1" to "R", "P1" to "P", "D1" to "D").forEach { (id, kind) ->
            composeRule.scrollTo("idc:$id")
            composeRule.onNodeWithTag("idc:$id").assert(SemanticsMatcher.expectValue(IdKindKey, kind))
        }
    }

    @Test
    fun decisionLineShowsTitleApprovedAndRecord() {
        composeRule.setContractBody()

        composeRule.assertShownIn("contract-dline", "Synthetic contract title")
        composeRule.assertShownIn("contract-dline", "Synthetic approved line")
        composeRule.assertShownIn("contract-dline", "accepted 22:13")
        composeRule.assertShownIn("contract-dline", "Decisions needed: none")
    }

    @Test
    fun aBaseIsACommitMarkNamedByItsId() {
        composeRule.setContractBody()

        composeRule.onNodeWithTag("contract-base").assert(
            SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf("main")),
        )
    }

    @Test
    fun acceptedLineWithoutABaseHasNoCommitMark() {
        composeRule.setContractBody(contract = syntheticContract(base = ""))

        composeRule.assertShownIn("contract-dline", "accepted 22:13")
        assertEquals(0, composeRule.onAllNodesWithTag("contract-base").fetchSemanticsNodes().size)
    }

    @Test
    fun decisionLineCountsTheOpenDecisions() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION, SETTLED_DECISION))

        composeRule.assertShownIn("contract-dline", "Decisions needed: 1")
    }

    @Test
    fun anEpicsGetContractDrawsNoControls() {
        composeRule.setContractBody()

        composeRule.assertShows("None.")
        listOf("F1", "R1", "P1").forEach { id -> composeRule.assertNoControlIn("contract-row:$id", "contract-talk:$id") }
        composeRule.assertNoControlIn("contract-addition:0", "contract-keep:0")
        composeRule.assertNoControlIn("contract-addition:0", "contract-strike:0")
    }

    @Test
    fun noAdditionsShowsNone() {
        composeRule.setContractBody(contract = syntheticContract(additions = emptyList()), decisions = listOf(OPEN_DECISION))

        composeRule.assertShows("None.")
        assertEquals(0, composeRule.onAllNodesWithText(ADDITION.what).fetchSemanticsNodes().size)
    }

    @Test
    fun bodyHoldsNoEditableNode() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION))

        composeRule.onNodeWithTag(BODY_TAG).assertIsDisplayed()
        val editable = composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsActions.SetText))
        assertEquals(0, editable.fetchSemanticsNodes().size)
    }

    private fun assertToneOf(rowTag: String, text: String, tone: ChipTone) {
        composeRule.scrollTo(rowTag)
        val nodes = composeRule.onAllNodesWithText(text).fetchSemanticsNodes()
        assertTrue(nodes.isNotEmpty())
        nodes.forEach { node -> assertEquals(tone, node.config[ChipToneKey]) }
    }
}
