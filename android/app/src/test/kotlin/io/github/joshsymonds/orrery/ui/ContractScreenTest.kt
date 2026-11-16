package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performScrollToNode
import io.github.joshsymonds.orrery.api.EpicAdditionRow
import io.github.joshsymonds.orrery.api.EpicAlternativeRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicFailureRow
import io.github.joshsymonds.orrery.api.EpicPremiseRow
import io.github.joshsymonds.orrery.api.EpicReleaseStepRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
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

private const val BODY_TAG = "contract-body"

// Unix seconds 1_700_000_000 is 22:13 in UTC, the zone these tests run in.
private const val ACCEPTED = 1_700_000_000L

private val HEADINGS = listOf(
    "What you asked for",
    "What could go wrong, and how much we care",
    "Things you did not ask for",
    "What will be true when done",
    "What I'm assuming",
    "What we won't do",
    "How, and why not the other ways",
    "What leaves this machine or can't be undone",
    "Checks the machines run",
)

private val ADDITION = EpicAdditionRow(
    location = "Synthetic addition location",
    what = "Synthetic addition what",
    why = "Synthetic addition why",
    cost = "Synthetic addition cost",
)

private val REQUIREMENT = EpicRequirementRow(
    id = "R1",
    text = "Synthetic requirement text",
    evidence = "Synthetic requirement evidence",
    kind = "check",
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractScreenTest {
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
    fun headingsAreTheNineSectionsInTheContractsOrder() {
        setBody(contract(), listOf(REQUIREMENT))

        HEADINGS.forEachIndexed { index, heading ->
            val tag = "contract-heading:$index"
            composeRule.onNodeWithTag(BODY_TAG).performScrollToNode(hasTestTag(tag))
            composeRule.onNodeWithTag(tag).assertTextEquals(heading)
        }
    }

    @Test
    fun everyFieldIsShownAsItsOwnText() {
        setBody(contract(), listOf(REQUIREMENT))

        val fields = listOf(
            "Synthetic contract title",
            "Synthetic approved line",
            "Synthetic intent text.",
            "F1",
            "Synthetic failure text",
            "limited",
            "Synthetic failure response",
            "Synthetic care text.",
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
        )
        fields.forEach { text -> assertShows(text) }
    }

    @Test
    fun noAdditionsShowsNone() {
        setBody(contract(additions = emptyList()), listOf(REQUIREMENT))

        assertShows("None")
        assertEquals(0, composeRule.onAllNodes(hasText(ADDITION.what)).fetchSemanticsNodes().size)
    }

    @Test
    fun acceptedLineIsTheAcceptedClockAndTheBase() {
        setBody(contract(base = "main"), listOf(REQUIREMENT))

        assertShows("accepted 22:13 · base main")
    }

    @Test
    fun acceptedLineWithoutABaseIsTheClockAlone() {
        setBody(contract(base = ""), listOf(REQUIREMENT))

        assertShows("accepted 22:13")
    }

    @Test
    fun bodyHoldsNoEditableNode() {
        setBody(contract(), listOf(REQUIREMENT))

        composeRule.onNodeWithTag(BODY_TAG).assertIsDisplayed()
        val editable = composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(SemanticsActions.SetText))
        assertEquals(0, editable.fetchSemanticsNodes().size)
    }

    private fun setBody(contract: EpicContractRow, requirements: List<EpicRequirementRow>) {
        composeRule.setContent {
            MaterialTheme {
                ContractBody(contract = contract, requirements = requirements)
            }
        }
    }

    private fun assertShows(text: String) {
        composeRule.onNodeWithTag(BODY_TAG).performScrollToNode(hasText(text))
        composeRule.onNodeWithText(text).assertIsDisplayed()
    }

    private fun contract(
        additions: List<EpicAdditionRow> = listOf(ADDITION),
        base: String = "main",
    ) = EpicContractRow(
        title = "Synthetic contract title",
        approved = "Synthetic approved line",
        intent = "Synthetic intent text.",
        failures = listOf(
            EpicFailureRow(
                id = "F1",
                text = "Synthetic failure text",
                rating = "limited",
                response = "Synthetic failure response",
            ),
        ),
        care = "Synthetic care text.",
        additions = additions,
        premises = listOf(
            EpicPremiseRow(id = "P1", text = "Synthetic premise text", ifWrong = "Synthetic premise if wrong"),
        ),
        exclusions = listOf("Synthetic exclusion text"),
        approach = "Synthetic approach text.",
        alternatives = listOf(
            EpicAlternativeRow(
                alternative = "Synthetic alternative",
                whyNot = "Synthetic why not",
                reconsider = "Synthetic reconsider",
            ),
        ),
        releaseSteps = listOf(
            EpicReleaseStepRow(step = "Synthetic release step", target = "Synthetic release target", undo = "Synthetic release undo"),
        ),
        releaseThen = "Synthetic release then text.",
        checks = "Synthetic checks text.",
        base = base,
        accepted = ACCEPTED,
    )
}
