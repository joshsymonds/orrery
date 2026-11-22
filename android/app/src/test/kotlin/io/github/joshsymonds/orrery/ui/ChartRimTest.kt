package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.click
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performMouseInput
import androidx.compose.ui.test.performTouchInput
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import kotlin.math.PI
import kotlin.math.min

// The chart's square, in units, centred on point(0, 0).
private val BOX = 2 * point(0f, 0f).x

// The requirement rim's radius in units, where a tap on a segment lands, and the radius a tap on a task's cells lands at.
private const val RIM = 177f
private const val CELLS = 130f

// The fixture's slots: one for each of its three tasks, and one for each of the three requirements no task names (R3 to R5).
private const val SLOTS = 6

// A requirement in [state], as the server gives it, with its text.
private fun requirement(id: String, state: String): EpicRequirementRow =
    EpicRequirementRow(id = id, text = "$id is true", evidence = "", kind = "check", state = state)

// A task that names [requirements] in order, running in implement.
private fun task(name: String, requirements: List<String>): RecordTask = RecordTask(
    task = name,
    title = "title of $name",
    requirements = requirements,
    place = RingPlace.IMPLEMENT,
    integrated = false,
    tone = WedgeTone.RUN,
    cells = cellsOf(RingPlace.IMPLEMENT, integrated = false, tone = WedgeTone.RUN),
    words = "implement",
    since = 0L,
)

// Five requirements, one in each state the server gives, and three tasks: T1 names R2 then R1, so it sits in R2; T2 names
// R1; T3 names none. The rim is R1 (T2), R2 (T1), R3, R4 and R5 empty, then the unlabelled group (T3).
private val requirements = listOf(
    requirement("R1", "proven"),
    requirement("R2", "failed"),
    requirement("R3", "held"),
    requirement("R4", "in_flight"),
    requirement("R5", "not_reached"),
)
private val tasks = listOf(task("T1", listOf("R2", "R1")), task("T2", listOf("R1")), task("T3", emptyList()))

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ChartRimTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setChart(proven: Set<String> = emptySet()) {
        composeRule.setContent {
            MaterialTheme { EpicOrrery(tasks = tasks, proven = proven, requirements = requirements) }
        }
    }

    private fun marks(): List<RimMark> = composeRule.onNodeWithTag("epic-orrery").fetchSemanticsNode().config[EpicRimKey]

    private fun captionNodes() = composeRule.onAllNodesWithTag("orrery-caption").fetchSemanticsNodes()

    // The middle of slot [slot] of the fixture's slots, in degrees clockwise from 12 o'clock.
    private fun middleOf(slot: Int): Float = sliceStart(slot, SLOTS) + sliceSpan(SLOTS) / 2

    // The point in the chart's touch pixels, [radius] units out at [degrees] clockwise from 12 o'clock.
    private fun touchAt(radius: Float, degrees: Float): Offset {
        val size = composeRule.onNodeWithTag("epic-orrery").fetchSemanticsNode().size
        val unit = min(size.width, size.height) / BOX
        val offset = point(radius, degrees * PI.toFloat() / 180f) - point(0f, 0f)
        return Offset(size.width / 2f + offset.x * unit, size.height / 2f + offset.y * unit)
    }

    private fun tap(radius: Float, degrees: Float) {
        val at = touchAt(radius, degrees)
        composeRule.onNodeWithTag("epic-orrery").performTouchInput { click(at) }
    }

    @Test
    fun ChartRimHasOneSegmentPerRequirementInTheOrderTheEpicListsThem() {
        assertEquals(
            listOf("R1", "R2", "R3", "R4", "R5"),
            rimSegmentsOf(tasks, requirements.map { it.id }).mapNotNull { it.id },
        )
    }

    @Test
    fun ChartRimPassesOverANameTheEpicDoesNotListWhenPlacingATask() {
        val placed = rimSegmentsOf(
            tasks + listOf(task("T4", listOf("R9")), task("T5", listOf("R9", "R1"))),
            requirements.map { it.id },
        )

        // No segment for R9, and T5 sits in R1 because R1 is the first name it lists; T4 names only R9, so it is unlabelled.
        assertEquals(listOf("R1", "R2", "R3", "R4", "R5", null), placed.map { it.id })
        assertEquals(listOf("T2", "T5"), placed.first { it.id == "R1" }.tasks.map { it.task })
        assertEquals(listOf("T3", "T4"), placed.last().tasks.map { it.task })
    }

    @Test
    fun ChartRimLabelsEachSegmentWithItsRequirementsIdAndLeavesTheUnlabelledGroupBare() {
        setChart()

        assertEquals(listOf("R1", "R2", "R3", "R4", "R5", null), marks().map { it.id })
    }

    @Test
    fun ChartRimTonesEachSegmentByItsRequirementsServerState() {
        setChart()

        assertEquals(
            listOf(
                OrreryTokens.Role.ok,
                OrreryTokens.Role.bad,
                OrreryTokens.Tone.mine,
                OrreryTokens.Tone.run,
                OrreryTokens.Line.firm,
                OrreryTokens.Line.soft,
            ),
            marks().map { it.colour },
        )
    }

    @Test
    fun ChartRimPlacesEachTaskInsideTheFirstRequirementItNames() {
        setChart()

        // T1 names R2 then R1, so it sits in R2 alone; T2 names R1 alone.
        assertEquals(listOf("T2"), marks().first { it.id == "R1" }.tasks)
        assertEquals(listOf("T1"), marks().first { it.id == "R2" }.tasks)
        assertEquals(listOf("T1", "T2", "T3"), marks().flatMap { it.tasks }.sorted())
    }

    @Test
    fun ChartRimPutsTasksNamingNoneInAnUnlabelledLastSegment() {
        setChart()

        assertNull(marks().last().id)
        assertEquals(listOf("T3"), marks().last().tasks)
        assertTrue(rimSegmentsOf(listOf(task("T2", listOf("R1"))), listOf("R1")).none { it.id == null })
    }

    @Test
    fun ChartRimKeepsAnEmptySegmentForARequirementNoTaskNames() {
        setChart()

        assertEquals(emptyList<String>(), marks().first { it.id == "R3" }.tasks)
        assertEquals(1, rimSegmentsOf(tasks, requirements.map { it.id }).first { it.id == "R3" }.slots)
    }

    @Test
    fun ChartRimCentreReadsTheProvenCountOfTheRequirements() {
        setChart(proven = setOf("R1", "R4"))

        composeRule.onNodeWithTag("orrery-centre").assertTextEquals("2 of 5 proven")
    }

    @Test
    fun ChartRimTapOnASegmentSelectsItAndCaptionsItUntilItIsTappedAgain() {
        setChart()

        tap(RIM, middleOf(1))
        composeRule.onNodeWithTag("orrery-caption").assertTextEquals("R2 · failed · R2 is true")
        tap(RIM, middleOf(1))
        assertTrue(captionNodes().isEmpty())
        tap(RIM, middleOf(2))
        composeRule.onNodeWithTag("orrery-caption").assertTextEquals("R3 · held · R3 is true")
    }

    @Test
    fun ChartRimTapOnATaskSelectsItAndCaptionsItUntilItIsTappedAgain() {
        setChart()

        tap(CELLS, middleOf(1))
        composeRule.onNodeWithTag("orrery-caption").assertTextEquals("title of T1 · implement")
        tap(CELLS, middleOf(1))
        assertTrue(captionNodes().isEmpty())
    }

    @Test
    fun ChartRimTapOnTheUnlabelledRimCaptionsNothing() {
        setChart()

        tap(RIM, middleOf(5))

        assertTrue(captionNodes().isEmpty())
    }

    @Test
    fun ChartRimCaptionsOnlyOnTapAndNotOnHover() {
        setChart()
        val at = touchAt(RIM, middleOf(1))

        composeRule.onNodeWithTag("epic-orrery").performMouseInput {
            enter(at)
            moveTo(at)
        }

        assertTrue(captionNodes().isEmpty())
    }

    @Test
    fun ChartRimNamesTheFiveStagesInTheRingsAndThePips() {
        assertEquals(
            listOf("red", "implement", "green", "review", "integrate"),
            ringNameArcs(tasks.size).map { it.place.word },
        )
        composeRule.setContent {
            MaterialTheme { Pips(cellsOf(RingPlace.GREEN, integrated = false, tone = WedgeTone.RUN)) }
        }

        assertEquals(
            listOf("red passed, implement passed, green running, review not reached, integrate not reached"),
            composeRule.onNodeWithTag("pips").fetchSemanticsNode().config.getOrNull(SemanticsProperties.ContentDescription),
        )
    }
}
