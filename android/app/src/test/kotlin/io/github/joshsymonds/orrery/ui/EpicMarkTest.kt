package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.assertWidthIsEqualTo
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The EpicMark on Android: an epic's mark drawn from its requirements' states and its centre state, in the inbox row at
// 32 and in every other place at 24, and in no place a Gauge. Each test reads the mark's model from its semantics,
// since the drawing itself is not read back, and runs at a synthetic clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicMarkTest {
    private companion object {
        const val NOW = 100_000L
        const val EPIC_ID = "lighthouse/rota-export"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun requirement(id: String, state: String) =
        EpicRequirementRow(id = id, text = "", evidence = "", kind = "check", state = state)

    // One requirement in each of the five states a rim segment is toned by, in contract order.
    private val fiveStates = listOf(
        requirement("R1", "proven"),
        requirement("R2", "in_flight"),
        requirement("R3", "held"),
        requirement("R4", "failed"),
        requirement("R5", "not_reached"),
    )

    private fun buildTask(id: String, title: String, stage: String, requirement: String) = EpicBuildTaskRow(
        id = id,
        title = title,
        status = "open",
        stage = stage,
        red = null,
        green = null,
        requirements = listOf(requirement),
    )

    private fun epic() = EpicRow(
        id = EPIC_ID,
        repository = "/home/user/lighthouse",
        slug = "rota-export",
        outcome = "accepted",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        phaseAt = NOW - 49 * 60,
    )

    private fun detail(
        requirements: List<EpicRequirementRow> = fiveStates,
        centre: String = "running",
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
    ) = EpicDetail(
        epic = epic(),
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        requirements = requirements,
        buildTasks = buildTasks,
        centre = centre,
    )

    private fun epicNode(detail: EpicDetail = detail()) = EpicNode(epic(), detail, contract = null)

    private fun convo(node: EpicNode) =
        Convo(key = "session:s1", pane = null, standIn = StandIn.NONE, again = false, hung = listOf(node))

    private fun markOf(tag: String) =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().config[EpicMarkKey]

    private fun hasTagStartingWith(prefix: String) = SemanticsMatcher("a test tag starting with $prefix") { node ->
        node.config.getOrNull(SemanticsProperties.TestTag)?.startsWith(prefix) == true
    }

    @Test
    fun `EpicMark draws every rim tone on its own segment, in requirement order`() {
        composeRule.setContent { EpicMark(EPIC_ID, epicMarkOf(fiveStates, "running"), 32.dp) }

        val rim = markOf("epicmark:$EPIC_ID").rim
        assertEquals(listOf("R1", "R2", "R3", "R4", "R5"), rim.map { it.id })
        assertEquals(listOf(RimTone.OK, RimTone.RUN, RimTone.MINE, RimTone.BAD, RimTone.FIRM), rim.map { it.tone })
    }

    @Test
    fun `EpicMark draws every centre with its own glyph and tone`() {
        val centres = listOf("running", "asks", "paused", "releasing", "shipped", "ended_short")
        composeRule.setContent {
            Column {
                for (centre in centres) EpicMark("centre-$centre", epicMarkOf(fiveStates, centre), 24.dp)
            }
        }

        val drawn = centres.associateWith { markOf("epicmark:centre-$it").centre }
        assertEquals(
            mapOf(
                "running" to CentreMark("running", CentreTone.LIVE, CentreGlyph.COUNT, proven = 1),
                "asks" to CentreMark("asks you a question", CentreTone.ATTENTION, CentreGlyph.QUESTION),
                "paused" to CentreMark("paused", CentreTone.QUIET, CentreGlyph.PAUSE),
                "releasing" to CentreMark("releasing", CentreTone.ENGINE, CentreGlyph.ARROW),
                "shipped" to CentreMark("shipped", CentreTone.OK, CentreGlyph.CHECK),
                "ended_short" to CentreMark("ended short", CentreTone.QUIET, CentreGlyph.DASH),
            ),
            drawn,
        )
    }

    @Test
    fun `EpicMark in the inbox row is 32 across`() {
        composeRule.setContent { ConvoRowItem(convo(epicNode()), now = NOW, onOpen = {}, onLongPress = {}) }

        val mark = composeRule.onNodeWithTag("epicmark:$EPIC_ID", useUnmergedTree = true)
        mark.assertWidthIsEqualTo(32.dp)
        mark.assertHeightIsEqualTo(32.dp)
    }

    @Test
    fun `EpicMark in the work card is 24 across`() {
        val card = workCardOf(epicNode(), NOW)
        composeRule.setContent { WorkCard(card, onOpen = {}) }

        val mark = composeRule.onNodeWithTag("epicmark:${card.key}", useUnmergedTree = true)
        mark.assertWidthIsEqualTo(24.dp)
        mark.assertHeightIsEqualTo(24.dp)
    }

    @Test
    fun `EpicMark in a notice lead is 24 across`() {
        composeRule.setContent { NoticeLeadMark(NoticeLead.Mark(epicMarkOf(fiveStates, "running")), EPIC_ID) }

        val mark = composeRule.onNodeWithTag("epicmark:$EPIC_ID", useUnmergedTree = true)
        mark.assertWidthIsEqualTo(24.dp)
        mark.assertHeightIsEqualTo(24.dp)
    }

    @Test
    fun `EpicMark replaces the gauge in every place an epic is drawn`() {
        val node = epicNode()
        val card = workCardOf(node, NOW)
        composeRule.setContent {
            Column {
                ConvoRowItem(convo(node), now = NOW, onOpen = {}, onLongPress = {})
                WorkCard(card, onOpen = {})
                NoticeLeadMark(NoticeLead.Mark(epicMarkOf(fiveStates, "running")), EPIC_ID)
            }
        }

        composeRule.onAllNodes(hasTagStartingWith("gauge:"), useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodes(hasTagStartingWith("epicmark:"), useUnmergedTree = true).assertCountEquals(3)
    }

    @Test
    fun `EpicMark of an orreryd epic building tasks draws its requirements and never reads as no tasks`() {
        val requirements = listOf(
            requirement("R1", "proven"),
            requirement("R2", "in_flight"),
            requirement("R3", "not_reached"),
        )
        val buildTasks = listOf(
            buildTask("T1", "shift-store", stage = "implement", requirement = "R1"),
            buildTask("T2", "csv-writer", stage = "", requirement = "R2"),
        )
        val building = detail(requirements = requirements, buildTasks = buildTasks)
        composeRule.setContent { ConvoRowItem(convo(epicNode(building)), now = NOW, onOpen = {}, onLongPress = {}) }

        val mark = markOf("epicmark:$EPIC_ID")
        assertEquals(listOf("R1", "R2", "R3"), mark.rim.map { it.id })
        assertEquals("1 of 3 requirements proven, running", mark.label)
        composeRule.onNodeWithTag("epicmark:$EPIC_ID", useUnmergedTree = true)
            .assert(hasContentDescription("1 of 3 requirements proven, running"))
    }
}
