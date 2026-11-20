package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val BACK_TAG = "title-back"
private const val STATE_TAG = "title-state"
private const val BLOCKER_TAG = "title-blocker"
private const val ACTION_TAG = "synthetic-action"
private const val ASK_TAG = "ask-stop"
private const val ASK_QUESTION = "synthetic question?"
private val CRUMBS = listOf(Crumb("orrery"), Crumb("slug"))

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class TitleRowTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun backCallsOnBackOnce() {
        var backs = 0
        setRow(crumbs = CRUMBS, onBack = { backs++ })

        composeRule.onNodeWithTag(BACK_TAG).performClick()

        assertEquals(1, backs)
    }

    @Test
    fun earlierCrumbCallsItsOnGo() {
        var orreryGoes = 0
        var slugGoes = 0
        setRow(crumbs = listOf(Crumb("orrery", onGo = { orreryGoes++ }), Crumb("slug", onGo = { slugGoes++ })))

        composeRule.onNodeWithTag("crumb:0").performClick()

        assertEquals(1, orreryGoes)
        assertEquals(0, slugGoes)
    }

    @Test
    fun lastCrumbHasNoClickAction() {
        setRow(crumbs = listOf(Crumb("orrery", onGo = {}), Crumb("slug", onGo = {})))

        composeRule.onNodeWithTag("crumb:1").assertHasNoClickAction()
    }

    @Test
    fun crumbsReadOrreryThenSlugWithAnArrowBetween() {
        setRow(crumbs = CRUMBS)

        composeRule.onNodeWithTag("crumb:0").assertTextEquals("orrery")
        composeRule.onNodeWithTag("crumb:1").assertTextEquals("slug")
        val first = composeRule.onNodeWithTag("crumb:0").fetchSemanticsNode().boundsInRoot
        val arrow = composeRule.onNodeWithText("›").fetchSemanticsNode().boundsInRoot
        val second = composeRule.onNodeWithTag("crumb:1").fetchSemanticsNode().boundsInRoot
        assertTrue(first.right <= arrow.left && arrow.right <= second.left)
    }

    @Test
    fun stateAndBlockerShowWhenGiven() {
        setRow(crumbs = CRUMBS, state = "running", blocker = "waiting on review")

        composeRule.onNodeWithTag(STATE_TAG).assertTextEquals("running")
        composeRule.onNodeWithTag(BLOCKER_TAG).assertTextEquals("waiting on review")
    }

    @Test
    fun stateAndBlockerAreAbsentWhenNotGiven() {
        setRow(crumbs = CRUMBS)

        assertNoNode(STATE_TAG)
        assertNoNode(BLOCKER_TAG)
    }

    @Test
    fun aStateToneColoursTheStateInItsColour() {
        setRow(crumbs = CRUMBS, state = "building", stateTone = WordTone.QUIET)

        assertEquals(listOf(OrreryTokens.Ink.three), coloursOf(STATE_TAG))
    }

    @Test
    fun aBlockerToneColoursTheBlockerInItsColour() {
        setRow(crumbs = CRUMBS, blocker = "parked", blockerTone = WordTone.LIVE)

        assertEquals(listOf(OrreryTokens.Role.live), coloursOf(BLOCKER_TAG))
    }

    @Test
    fun withNoToneTheStateDrawsInTheDefaultColourAndTheBlockerInAttention() {
        setRow(crumbs = CRUMBS, state = "running", blocker = "waiting on review")

        assertEquals(listOf(Color.Unspecified), coloursOf(STATE_TAG))
        assertEquals(listOf(OrreryTokens.Role.attention), coloursOf(BLOCKER_TAG))
    }

    @Test
    fun actionsShowInTheRow() {
        setRow(crumbs = CRUMBS, actions = { Text("synthetic action", modifier = Modifier.testTag(ACTION_TAG)) })

        composeRule.onNodeWithTag(ACTION_TAG).assertIsDisplayed()
    }

    @Test
    fun oneTapOnAskFirstCallsNothingAndShowsTheQuestion() {
        var confirms = 0
        setAsk { confirms++ }

        composeRule.onNodeWithTag(ASK_TAG).performClick()

        assertEquals(0, confirms)
        composeRule.onNodeWithTag("$ASK_TAG-sure").assertTextEquals(ASK_QUESTION)
        composeRule.onNodeWithTag("$ASK_TAG-confirm").assertIsDisplayed()
        composeRule.onNodeWithTag("$ASK_TAG-keep").assertIsDisplayed()
    }

    @Test
    fun confirmCallsOnConfirmOnceAndReturnsToTheButton() {
        var confirms = 0
        setAsk { confirms++ }

        composeRule.onNodeWithTag(ASK_TAG).performClick()
        composeRule.onNodeWithTag("$ASK_TAG-confirm").performClick()

        assertEquals(1, confirms)
        composeRule.onNodeWithTag(ASK_TAG).assertIsDisplayed()
        assertNoNode("$ASK_TAG-sure")
        assertNoNode("$ASK_TAG-confirm")
    }

    @Test
    fun keepCallsNothingAndReturnsToTheButton() {
        var confirms = 0
        setAsk { confirms++ }

        composeRule.onNodeWithTag(ASK_TAG).performClick()
        composeRule.onNodeWithTag("$ASK_TAG-keep").performClick()

        assertEquals(0, confirms)
        composeRule.onNodeWithTag(ASK_TAG).assertIsDisplayed()
        assertNoNode("$ASK_TAG-sure")
        assertNoNode("$ASK_TAG-keep")
    }

    private fun assertNoNode(tag: String) {
        assertTrue(composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isEmpty())
    }

    private fun coloursOf(tag: String): List<Color> =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode()
            .config[SemanticsProperties.Text].flatMap { text -> text.spanStyles.map { it.item.color } }

    private fun setRow(
        crumbs: List<Crumb>,
        onBack: () -> Unit = {},
        state: String = "",
        stateTone: WordTone? = null,
        blocker: String = "",
        blockerTone: WordTone? = null,
        actions: @Composable RowScope.() -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                TitleRow(
                    crumbs = crumbs,
                    onBack = onBack,
                    state = state,
                    stateTone = stateTone,
                    blocker = blocker,
                    blockerTone = blockerTone,
                    actions = actions,
                )
            }
        }
    }

    private fun setAsk(onConfirm: () -> Unit) {
        composeRule.setContent {
            MaterialTheme {
                Row {
                    AskFirst(
                        label = "Stop",
                        question = ASK_QUESTION,
                        confirm = "Confirm stop",
                        keep = "Keep",
                        tag = ASK_TAG,
                        onConfirm = onConfirm,
                    )
                }
            }
        }
    }
}
