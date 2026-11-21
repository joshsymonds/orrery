package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.StepFacts
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ShapesBlocksTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun youBlockDrawsYouTheTimeAndTheYouRail() {
        setBlock { YouBlock(text = "Plan the rota export.", time = "13:48") }

        composeRule.onNodeWithTag("block:you").assertIsDisplayed()
        composeRule.onNodeWithText("YOU").assertIsDisplayed()
        composeRule.onNodeWithTag("at").assertTextEquals("13:48")
        composeRule.onNodeWithText("Plan the rota export.").assertIsDisplayed()
        assertRailTone("block:you", "you")
    }

    @Test
    fun youBlocksYouSharesItsRowWithTheTimeAndItsWordsRunOnFromThatRow() {
        setBlock { YouBlock(text = "Plan the rota export.", time = "13:48") }

        val you = composeRule.onNodeWithText("YOU").getBoundsInRoot()
        val at = composeRule.onNodeWithTag("at").getBoundsInRoot()
        val words = composeRule.onNodeWithText("Plan the rota export.").getBoundsInRoot()
        assertTrue("the time is on YOU's row: YOU $you, time $at", at.top < you.bottom && you.top < at.bottom)
        assertTrue("the words start on YOU's row, not under it: YOU $you, words $words", words.top < you.bottom)
        val first = firstCharacterLeft("Plan the rota export.")
        assertTrue("the words' first line starts after the time: time ends at ${at.right}, words start at $first", first >= at.right)
    }

    @Test
    fun aYouBlockThatOpensOnAListKeepsItsHeadOnALineOfItsOwn() {
        setBlock { YouBlock(text = "- Plan the rota export.", time = "13:48") }

        val you = composeRule.onNodeWithText("YOU").getBoundsInRoot()
        val item = composeRule.onNodeWithText("Plan the rota export.").getBoundsInRoot()
        assertTrue("the list starts under the head: YOU $you, item $item", item.top >= you.bottom)
    }

    @Test
    fun youBlockRailIsRailWideAndHasHeight() {
        setBlock { YouBlock(text = "Plan the rota export.", time = "13:48") }

        val block = composeRule.onNodeWithTag("block:you").getBoundsInRoot()
        val rail = composeRule.onNodeWithTag("rail", useUnmergedTree = true).getBoundsInRoot()
        val railWidth = rail.right - rail.left
        val railHeight = rail.bottom - rail.top
        assertEquals(OrreryTokens.Size.rail, railWidth)
        assertTrue(railWidth < block.right - block.left)
        assertTrue(railHeight > 0.dp)
    }

    @Test
    fun phonePromptDrawsThePhoneIconAndFromYourPhone() {
        setBlock { YouBlock(text = "Ship the rota export.", time = "14:02", phone = true) }

        composeRule.onNodeWithTag("block:you").assertIsDisplayed()
        composeRule.onNodeWithText("YOU").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:phone").assertIsDisplayed()
        composeRule.onNodeWithText("· from your phone").assertIsDisplayed()
        composeRule.onAllNodesWithTag("icon:person").assertCountEquals(0)
    }

    @Test
    fun deskPromptDrawsThePersonIconAndNoPhoneWords() {
        setBlock { YouBlock(text = "Ship the rota export.", time = "14:02") }

        composeRule.onNodeWithTag("icon:person").assertIsDisplayed()
        composeRule.onAllNodesWithTag("icon:phone").assertCountEquals(0)
        composeRule.onAllNodesWithText("from your phone", substring = true).assertCountEquals(0)
    }

    @Test
    fun sendingYouBlockDrawsSendingWithNoTime() {
        setBlock { YouBlock(text = "Keep the pin.", time = "14:40", sending = true) }

        composeRule.onNodeWithTag("block:you").assertIsDisplayed()
        composeRule.onNodeWithText("SENDING").assertIsDisplayed()
        composeRule.onAllNodesWithText("YOU").assertCountEquals(0)
        composeRule.onAllNodesWithTag("at").assertCountEquals(0)
        assertRailTone("block:you", "you")
    }

    @Test
    fun eventBlockDrawsEventFromTheEngineWithTheGearAndTheEngineRail() {
        setBlock { EventBlock(text = "The planner outlined 7 tasks.", time = "13:58") }

        composeRule.onNodeWithTag("block:event").assertIsDisplayed()
        composeRule.onNodeWithText("EVENT").assertIsDisplayed()
        composeRule.onNodeWithText("from the engine").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:gear").assertIsDisplayed()
        assertRailTone("block:event", "engine")
    }

    @Test
    fun cronEventBlockDrawsCronWithTheClockAndNoEngineWords() {
        setBlock { EventBlock(text = "Nightly sweep.", time = "03:00", cron = true) }

        composeRule.onNodeWithTag("block:event").assertIsDisplayed()
        composeRule.onNodeWithText("CRON").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:clock").assertIsDisplayed()
        composeRule.onAllNodesWithText("from the engine").assertCountEquals(0)
        assertRailTone("block:event", "engine")
    }

    @Test
    fun doneStepEndDrawsItsLabelWhoOkChipAndOkRail() {
        setBlock { StepEndBlock(who = "rota-export", agent = "", end = "done", summary = "Wrote the CSV writer.", facts = null) }

        composeRule.onNodeWithTag("block:stepend").assertIsDisplayed()
        composeRule.onNodeWithText("STEP ENDED").assertIsDisplayed()
        composeRule.onNodeWithText("rota-export").assertIsDisplayed()
        composeRule.onNodeWithText("done").assertIsDisplayed()
        composeRule.onNodeWithText("Wrote the CSV writer.").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:check").assertIsDisplayed()
        assertRailTone("block:stepend", "ok")
    }

    @Test
    fun stepEndNamesItsTaskAsWhoAndItsAgentAsWords() {
        setBlock {
            StepEndBlock(who = "rota-export", agent = "csv-writer", end = "done", summary = "Wrote the CSV writer.", facts = null)
        }

        composeRule.onNodeWithText("rota-export").assertIsDisplayed()
        composeRule.onNodeWithText("csv-writer").assertIsDisplayed()
    }

    @Test
    fun failedStepEndDrawsItsWordsAndBadRail() {
        setBlock { StepEndBlock(agent = "shift-store", end = "checks_failed", summary = "Stored shifts.", facts = null) }

        composeRule.onNodeWithText("checks failed").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:cross").assertIsDisplayed()
        assertRailTone("block:stepend", "bad")
    }

    @Test
    fun engineRestartedStepEndDrawsItsWordsAndBadCross() {
        setBlock { StepEndBlock(agent = "csv-writer", end = "engine_restarted", summary = "Cut off.", facts = null) }

        composeRule.onNodeWithText("engine restarted").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:cross").assertIsDisplayed()
        assertRailTone("block:stepend", "bad")
    }

    @Test
    fun stepEndWithAnEndTheBlocksDoNotNameDrawsItsWordAsWrittenWithBadCross() {
        setBlock { StepEndBlock(agent = "csv-writer", end = "paused_for_review", summary = "Waiting.", facts = null) }

        composeRule.onNodeWithText("paused_for_review").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:cross").assertIsDisplayed()
        assertRailTone("block:stepend", "bad")
    }

    @Test
    fun stoppedStepEndDrawsItsWordsAndQuietRail() {
        setBlock { StepEndBlock(agent = "audit-log", end = "stopped", summary = "Stopped by you.", facts = null) }

        composeRule.onNodeWithText("stopped").assertIsDisplayed()
        composeRule.onNodeWithTag("icon:stop").assertIsDisplayed()
        assertRailTone("block:stepend", "quiet")
    }

    @Test
    fun stepEndDrawsItsFactsLineUnderItsSummary() {
        val facts = StepFacts(rounds = 1, seconds = 480, counted = true, files = 2, added = 96, removed = 4)
        setBlock { StepEndBlock(agent = "csv-writer", end = "done", summary = "Wrote the CSV writer.", facts = facts) }

        composeRule.onNodeWithText("2 files").assertIsDisplayed()
        composeRule.onNodeWithText("+96").assertIsDisplayed()
        composeRule.onNodeWithText("−4").assertIsDisplayed()
        composeRule.onNodeWithText("1 round").assertIsDisplayed()
        composeRule.onNodeWithText("8m").assertIsDisplayed()
    }

    @Test
    fun checkBlockDrawsRoundWordsChipsAndBadRailWhenACheckFailed() {
        setBlock {
            CheckBlock(
                round = 2,
                text = "owned files: go.mod is not owned.",
                checks = listOf(CheckResult("tests", true), CheckResult("lint", true), CheckResult("owned files", false)),
            )
        }

        composeRule.onNodeWithTag("block:checks").assertIsDisplayed()
        composeRule.onNodeWithText("CHECKS").assertIsDisplayed()
        composeRule.onNodeWithText("round 2 from the engine").assertIsDisplayed()
        composeRule.onNodeWithText("tests").assertIsDisplayed()
        composeRule.onNodeWithText("lint").assertIsDisplayed()
        composeRule.onNodeWithText("owned files").assertIsDisplayed()
        composeRule.onAllNodesWithTag("icon:cross").assertCountEquals(2)
        assertRailTone("block:checks", "bad")
    }

    @Test
    fun checkBlockWithPassingChecksHasAnOkRail() {
        setBlock {
            CheckBlock(
                round = 3,
                text = "All three checks pass.",
                checks = listOf(CheckResult("tests", true), CheckResult("lint", true)),
            )
        }

        composeRule.onAllNodesWithTag("icon:check").assertCountEquals(3)
        assertRailTone("block:checks", "ok")
    }

    @Test
    fun checkBlockWithoutChecksHasNoChipsAndAnEngineRail() {
        setBlock { CheckBlock(round = 1, text = "Round 1 went back to the engine.", checks = emptyList()) }

        composeRule.onNodeWithText("round 1 from the engine").assertIsDisplayed()
        composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(ChipToneKey)).assertCountEquals(0)
        assertRailTone("block:checks", "engine")
    }

    @Test
    fun checkBlockOpensItsRestUnderItsFirstLine() {
        setBlock {
            CheckBlock(
                round = 3,
                text = "All three checks pass.\nGo ahead and merge.",
                checks = listOf(CheckResult("tests", true)),
            )
        }

        composeRule.onNodeWithText("All three checks pass.").assertIsDisplayed()
        composeRule.onAllNodesWithText("Go ahead and merge.").assertCountEquals(0)
        composeRule.onNodeWithText("All three checks pass.").performClick()
        composeRule.onNodeWithText("Go ahead and merge.").assertIsDisplayed()
    }

    private fun assertRailTone(blockTag: String, tone: String) {
        composeRule.onNodeWithTag(blockTag).assert(
            hasAnyDescendant(hasTestTag("rail") and SemanticsMatcher.expectValue(ShapeToneKey, tone)),
        )
    }

    // Where the first character of the text node reads, in the root's dp: the node's left plus the character's left in
    // its laid-out text.
    private fun firstCharacterLeft(text: String): Dp {
        val node = composeRule.onNodeWithText(text).fetchSemanticsNode()
        val layouts = mutableListOf<TextLayoutResult>()
        checkNotNull(node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action).invoke(layouts)
        val density = composeRule.density
        return composeRule.onNodeWithText(text).getBoundsInRoot().left + with(density) { layouts.single().getBoundingBox(0).left.toDp() }
    }

    private fun setBlock(content: @Composable () -> Unit) {
        composeRule.setContent {
            MaterialTheme { content() }
        }
    }
}
