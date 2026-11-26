package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val TITLE_TAG = "head-title"
private const val CONTEXT_TAG = "head-context"
private const val BAND_TAG = "band"
private const val HOST = "vermission"

/**
 * The conversation head with no host meters: the context sits with the title, the title row labels the host and nothing
 * else of it, and the goal's band sits under the title row. Neither the open head nor a folded one draws a host meter.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class HeadMachineBandTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    /** A host meter's node: the meters' grid, or one meter's own node. */
    private val hostMeter = SemanticsMatcher("a host meter") { node ->
        val tag = node.config.getOrElse(SemanticsProperties.TestTag) { "" }
        tag == "host-meters" || tag.startsWith("meter")
    }

    private fun setHead(fold: HeadFold = HeadFold.OPEN) {
        composeRule.setContent {
            MaterialTheme {
                Head(
                    dir = "commonframe",
                    host = HOST,
                    title = "Merging main into a story-out client branch",
                    context = ConversationContext(tokens = 86_000, window = 1_000_000),
                    note = "",
                    goal = "Accomplish all epics using the epic workflow.",
                    lines = emptyList(),
                    asking = "",
                    badges = emptyList(),
                    fold = fold,
                    onBack = {},
                    onOpenEpic = {},
                    onHide = {},
                    onShow = {},
                )
            }
        }
    }

    @Test
    fun theContextSitsInTheTitleRow() {
        setHead()

        composeRule.onNode(hasTestTag(CONTEXT_TAG) and hasAnyAncestor(hasTestTag(TITLE_TAG))).assertIsDisplayed()
    }

    @Test
    fun theOpenHeadDrawsNoHostMeters() {
        setHead()

        composeRule.onNodeWithTag(BAND_TAG).assertIsDisplayed()
        composeRule.onAllNodes(hostMeter, useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun theGoalBandSitsUnderTheTitleRow() {
        setHead()

        val title = composeRule.onNodeWithTag(TITLE_TAG).getUnclippedBoundsInRoot()
        val goal = composeRule.onNodeWithTag(BAND_TAG).getUnclippedBoundsInRoot()
        assertTrue("the band starts below the title row", goal.top >= title.bottom)
    }

    @Test
    fun theBareContextNamesItselfAndItsTokensForTheReader() {
        setHead()

        val words = listOf("context: 86k of a 1M window")
        composeRule.onNodeWithTag(CONTEXT_TAG)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, words))
    }

    @Test
    fun theProjectLineNamesNoHostAndTheTitleRowLabelsTheHost() {
        setHead()

        composeRule.onNodeWithTag("head-dir").assertTextEquals("commonframe")
        composeRule.onNode(hasTestTag("host-label") and hasAnyAncestor(hasTestTag(TITLE_TAG))).assertTextEquals(HOST)
    }

    @Test
    fun aFoldedHeadDrawsNoHostMeters() {
        setHead(fold = HeadFold.READING)

        composeRule.onNodeWithTag(CONTEXT_TAG).assertIsDisplayed()
        composeRule.onAllNodes(hostMeter, useUnmergedTree = true).assertCountEquals(0)
    }
}
