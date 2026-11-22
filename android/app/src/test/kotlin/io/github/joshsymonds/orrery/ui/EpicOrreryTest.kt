package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.click
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.IntSize
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
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

// How far from the centre, in units, a tap lands on a wedge's cells.
private const val WEDGE_RADIUS = 130f

// The first [done] tasks are integrated, the next is accepted but not yet integrated, and the rest are planned.
private fun chart(count: Int, done: Int): List<RecordTask> = List(count) { index ->
    val task = "b${index + 1}"
    when {
        index < done -> RecordTask(
            task = task,
            title = task,
            requirements = emptyList(),
            place = RingPlace.INTEGRATE,
            integrated = true,
            tone = WedgeTone.OK,
            cells = cellsOf(RingPlace.INTEGRATE, integrated = true, tone = WedgeTone.OK),
            words = "done",
            since = 0L,
        )
        index == done -> RecordTask(
            task = task,
            title = task,
            requirements = emptyList(),
            place = RingPlace.INTEGRATE,
            integrated = false,
            tone = WedgeTone.ACC,
            cells = cellsOf(RingPlace.INTEGRATE, integrated = false, tone = WedgeTone.ACC),
            words = "accepted · waits to integrate",
            since = 0L,
        )
        else -> RecordTask(
            task = task,
            title = task,
            requirements = emptyList(),
            place = null,
            integrated = false,
            tone = WedgeTone.PLAN,
            cells = cellsOf(null, integrated = false, tone = WedgeTone.PLAN),
            words = "not started",
            since = 0L,
        )
    }
}

// The point in touch pixels at the middle of wedge [index] of [count], [WEDGE_RADIUS] units out, for a chart drawn at [size].
private fun wedgeTouch(size: IntSize, index: Int, count: Int): Offset {
    val unit = min(size.width, size.height) / BOX
    val angle = (sliceStart(index, count) + sliceSpan(count) / 2) * PI.toFloat() / 180f
    val offset = point(WEDGE_RADIUS, angle) - point(0f, 0f)
    return Offset(size.width / 2f + offset.x * unit, size.height / 2f + offset.y * unit)
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicOrreryTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun ringNameArcsRunOutsideInOnTheirOwnRingsAndPassTwelveOClock() {
        val arcs = ringNameArcs(7)

        assertEquals(RingPlace.entries, arcs.map { it.place })
        arcs.forEach { arc ->
            assertEquals(ringRadius(arc.place.ordinal), arc.radius, 0f)
            assertEquals(sliceStart(0, 7), arc.endAngle, 0f)
            assertTrue(arc.startAngle < 0f)
            assertTrue(arc.endAngle > 0f)
        }
        assertTrue(arcs.zipWithNext().all { (outer, inner) -> outer.radius > inner.radius })
    }

    @Test
    fun contentDescriptionCountsTheIntegratedTasksOfSevenTasks() {
        composeRule.setContent {
            MaterialTheme { EpicOrrery(tasks = chart(7, done = 2), proven = emptySet()) }
        }

        assertEquals(listOf("2 of 7 tasks done"), contentDescription())
    }

    @Test
    fun contentDescriptionCountsTheIntegratedTasksOfAHundredAndTwentyTasks() {
        composeRule.setContent {
            MaterialTheme { EpicOrrery(tasks = chart(120, done = 3), proven = emptySet()) }
        }

        assertEquals(listOf("3 of 120 tasks done"), contentDescription())
    }

    @Test
    fun tappingAWedgeCaptionsItAndTappingItAgainClearsIt() {
        composeRule.setContent {
            MaterialTheme { EpicOrrery(tasks = chart(7, done = 2), proven = emptySet()) }
        }
        assertTrue(composeRule.onAllNodesWithTag("orrery-caption").fetchSemanticsNodes().isEmpty())

        composeRule.onNodeWithTag("epic-orrery").performTouchInput {
            click(wedgeTouch(visibleSize, index = 0, count = 7))
        }
        composeRule.onNodeWithTag("orrery-caption").assertTextEquals("b1 · done")

        composeRule.onNodeWithTag("epic-orrery").performTouchInput {
            click(wedgeTouch(visibleSize, index = 0, count = 7))
        }
        assertTrue(composeRule.onAllNodesWithTag("orrery-caption").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun noTasksDrawsNoChart() {
        composeRule.setContent {
            MaterialTheme { EpicOrrery(tasks = emptyList(), proven = emptySet()) }
        }

        assertTrue(composeRule.onAllNodesWithTag("epic-orrery").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun wedgeOnlyIdJoinsProvenSetOnceEveryWedgeNamingItIsIntegrated() {
        val pending = listOf(
            wedge("b1", WedgePlace.INTEGRATED, listOf("R9")),
            wedge("b2", WedgePlace.IMPLEMENTING, listOf("R9")),
        )
        assertEquals(emptySet<String>(), provenIdsOf(emptyList(), pending))

        val integrated = pending.map { if (it.task == "b2") it.copy(place = WedgePlace.INTEGRATED) else it }
        assertEquals(setOf("R9"), provenIdsOf(emptyList(), integrated))
    }

    private fun wedge(task: String, place: WedgePlace, requirements: List<String>): Wedge = Wedge(
        task = task,
        title = task,
        requirements = requirements,
        place = place,
        tone = WedgeTone.PLAN,
        slices = emptyList(),
    )

    private fun contentDescription(): List<String>? =
        composeRule.onNodeWithTag("epic-orrery").fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.ContentDescription)
}
