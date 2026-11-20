package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val HEAD_TAG = "compact-head"
private const val ASKING_TAG = "compact-asking"
private const val BADGE_PREFIX = "compact-badge:"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PhoneHeadCompactTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val rotaQuiet = HeadBadge(key = "E1", label = "rota", count = 0, asks = false)
    private val rotaAsking = HeadBadge(key = "E1", label = "rota", count = 0, asks = true)
    private val agents = HeadBadge(key = "agents", label = "agents", count = 2, asks = false)
    private val running = HeadBadge(key = "running", label = "running", count = 1, asks = false)

    private fun setLine(asking: String, badges: List<HeadBadge>, onOpen: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme { CompactHead(asking = asking, badges = badges, onOpen = onOpen) }
        }
    }

    private fun spanColours(tag: String): List<Color> =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode()
            .config[SemanticsProperties.Text].single().spanStyles.map { it.item.color }

    private fun hasBadgeTag(): SemanticsMatcher = SemanticsMatcher("compact badge") { node ->
        node.config.contains(SemanticsProperties.TestTag) &&
            node.config[SemanticsProperties.TestTag].startsWith(BADGE_PREFIX)
    }

    @Test
    fun quietLineDrawsNoAskingWordsAndNoAttentionColour() {
        setLine(asking = "", badges = listOf(rotaQuiet, agents))

        composeRule.onNodeWithTag(HEAD_TAG).assertIsDisplayed()
        composeRule.onAllNodesWithTag(ASKING_TAG, useUnmergedTree = true).assertCountEquals(0)
        assertFalse(spanColours("${BADGE_PREFIX}E1").contains(OrreryTokens.Role.attention))
        assertFalse(spanColours("${BADGE_PREFIX}agents").contains(OrreryTokens.Role.attention))
    }

    @Test
    fun askingWordsAreDrawnInAttention() {
        setLine(asking = "rota-export asks", badges = listOf(rotaAsking, agents))

        composeRule.onNodeWithTag(ASKING_TAG, useUnmergedTree = true).assertTextEquals("rota-export asks")
        assertEquals(listOf(OrreryTokens.Role.attention), spanColours(ASKING_TAG))
    }

    @Test
    fun askingBadgeReadsItsLabelAndQuestionMarkInAttention() {
        setLine(asking = "rota-export asks", badges = listOf(rotaAsking))

        composeRule.onNodeWithTag("${BADGE_PREFIX}E1", useUnmergedTree = true).assertTextEquals("rota?")
        assertEquals(listOf(OrreryTokens.Role.attention), spanColours("${BADGE_PREFIX}E1"))
    }

    @Test
    fun countedBadgeReadsItsCountAndUncountedBadgeItsLabel() {
        setLine(asking = "", badges = listOf(rotaQuiet, agents, running))

        composeRule.onNodeWithTag("${BADGE_PREFIX}agents", useUnmergedTree = true).assertTextEquals("agents 2")
        composeRule.onNodeWithTag("${BADGE_PREFIX}running", useUnmergedTree = true).assertTextEquals("running 1")
        composeRule.onNodeWithTag("${BADGE_PREFIX}E1", useUnmergedTree = true).assertTextEquals("rota")
    }

    @Test
    fun badgesDrawInTheirOrder() {
        setLine(asking = "", badges = listOf(rotaQuiet, agents, running))

        val tags = composeRule.onAllNodes(hasBadgeTag(), useUnmergedTree = true)
            .fetchSemanticsNodes()
            .map { it.config[SemanticsProperties.TestTag] }
        assertEquals(listOf("${BADGE_PREFIX}E1", "${BADGE_PREFIX}agents", "${BADGE_PREFIX}running"), tags)
    }

    @Test
    fun tappingTheLineCallsOnOpenOnce() {
        var opened = 0
        setLine(asking = "", badges = listOf(agents), onOpen = { opened++ })

        composeRule.onNodeWithTag(HEAD_TAG).performClick()

        assertEquals(1, opened)
    }

    @Test
    fun lineIsDescribedAsShowTheHeader() {
        setLine(asking = "", badges = emptyList())

        assertEquals(
            listOf("Show the header"),
            composeRule.onNodeWithTag(HEAD_TAG).fetchSemanticsNode().config[SemanticsProperties.ContentDescription],
        )
    }
}
