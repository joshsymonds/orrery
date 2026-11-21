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
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.HostStat
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
private const val MACHINE_BAND_TAG = "machine-band"
private const val BAND_TAG = "band"

/**
 * The conversation head as option A4 draws it: the context sits with the title, and the machine row is a band of its
 * own under the title row, holding every meter, with the goal's band after it.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class HeadMachineBandTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val stats = listOf(
        HostStat("net", "↓551K ↑72K", null, "ok"),
        HostStat("cpu", "93% 25/32", 93.0, "warn"),
        HostStat("ram", "19G", 30.0, "ok"),
        HostStat("disk io", "↓6K ↑2M", null, "ok"),
        HostStat("disk", "50%", 50.0, "ok"),
    )

    private fun setHead(fold: HeadFold = HeadFold.OPEN, otherHost: String? = null) {
        composeRule.setContent {
            MaterialTheme {
                Head(
                    dir = "commonframe",
                    host = "vermission",
                    title = "Merging main into a story-out client branch",
                    context = ConversationContext(tokens = 86_000, window = 1_000_000),
                    note = "",
                    stats = if (otherHost == null) stats else null,
                    otherHost = otherHost,
                    goal = "Accomplish all epics using the gambit workflow.",
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
        composeRule.onAllNodes(hasTestTag(CONTEXT_TAG) and hasAnyAncestor(hasTestTag(MACHINE_BAND_TAG)))
            .assertCountEquals(0)
    }

    @Test
    fun theMachineRowIsItsOwnBandUnderTheTitleRowAndOverTheGoal() {
        setHead()

        composeRule.onNodeWithTag(MACHINE_BAND_TAG).assertIsDisplayed()
        composeRule.onAllNodes(hasTestTag(MACHINE_BAND_TAG) and hasAnyAncestor(hasTestTag(TITLE_TAG)))
            .assertCountEquals(0)
        val title = composeRule.onNodeWithTag(TITLE_TAG).getUnclippedBoundsInRoot()
        val machine = composeRule.onNodeWithTag(MACHINE_BAND_TAG).getUnclippedBoundsInRoot()
        val goal = composeRule.onNodeWithTag(BAND_TAG).getUnclippedBoundsInRoot()
        assertTrue("the band starts below the title row", machine.top >= title.bottom)
        assertTrue("the goal starts below the band", goal.top >= machine.bottom)
    }

    @Test
    fun theMachineBandHoldsEveryMeterWithItsBar() {
        setHead()

        for (stat in stats) {
            composeRule.onNode(hasTestTag("meter:${stat.label}") and hasAnyAncestor(hasTestTag(MACHINE_BAND_TAG)))
                .assertIsDisplayed()
        }
        for (label in listOf("cpu", "ram", "disk")) {
            composeRule.onNode(hasTestTag("meter-bar:$label") and hasAnyAncestor(hasTestTag(MACHINE_BAND_TAG)))
                .assertIsDisplayed()
        }
    }

    @Test
    fun anotherHostIsNamedInTheMachineBand() {
        setHead(otherHost = "halyard")

        composeRule.onNode(hasTestTag("head-other-host") and hasAnyAncestor(hasTestTag(MACHINE_BAND_TAG)))
            .assertIsDisplayed()
    }

    @Test
    fun theBareContextNamesItselfAndItsTokensForTheReader() {
        setHead()

        val words = listOf("context: 86k of a 1M window")
        composeRule.onNodeWithTag(CONTEXT_TAG)
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, words))
    }

    @Test
    fun theProjectLineNamesNoHostAndTheBandDoes() {
        setHead()

        composeRule.onNodeWithTag("head-dir").assertTextEquals("commonframe")
        composeRule.onNodeWithTag("machine-host").assertTextEquals("vermission")
    }

    @Test
    fun anotherHostIsNamedInTheBandAndNotInTheProjectLine() {
        setHead(otherHost = "halyard")

        composeRule.onNodeWithTag("head-dir").assertTextEquals("commonframe")
        composeRule.onNodeWithTag("machine-host").assertTextEquals("halyard")
        composeRule.onNodeWithText("on another host: no meters").assertIsDisplayed()
    }

    @Test
    fun aFoldedHeadDrawsNoMachineBand() {
        setHead(fold = HeadFold.READING)

        composeRule.onAllNodesWithTag(MACHINE_BAND_TAG).assertCountEquals(0)
        composeRule.onNodeWithTag(CONTEXT_TAG).assertIsDisplayed()
    }
}
