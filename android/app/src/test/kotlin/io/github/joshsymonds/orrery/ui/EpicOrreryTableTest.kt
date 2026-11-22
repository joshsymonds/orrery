package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The time the table and the step rows measure from, in unix seconds.
private const val NOW = 10_000L

private fun task(
    name: String,
    place: RingPlace?,
    words: String,
    since: Long = 0L,
    requirements: List<String> = listOf("R1"),
    integrated: Boolean = false,
): RecordTask = RecordTask(
    task = name,
    title = "title of $name",
    requirements = requirements,
    labels = setOf(name),
    place = place,
    integrated = integrated,
    tone = WedgeTone.RUN,
    cells = cellsOf(place, integrated, WedgeTone.RUN),
    words = words,
    since = since,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicOrreryTableTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun node(tag: String) = composeRule.onNodeWithTag(tag, useUnmergedTree = true)

    private fun nodes(tag: String) = composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes()

    private fun stateOf(tag: String): String? =
        node(tag).fetchSemanticsNode().config.getOrNull(SemanticsProperties.StateDescription)

    private fun colourSpansOf(tag: String): List<Color> =
        node(tag).fetchSemanticsNode().config[SemanticsProperties.Text]
            .single()
            .spanStyles
            .map { it.item.color }

    private fun hasClickAction(tag: String): Boolean =
        node(tag).fetchSemanticsNode().config.contains(SemanticsActions.OnClick)

    @Test
    fun aTaskRowShowsItsNameProvesPipsWordsAndClock() {
        val tasks = listOf(
            task(
                "csv-writer",
                RingPlace.IMPLEMENT,
                words = "implement · writing tests",
                since = NOW - 125,
                requirements = listOf("R1", "R2"),
            ),
        )
        composeRule.setContent {
            MaterialTheme { TaskTable(tasks = tasks, now = NOW) }
        }

        node("task-row:csv-writer").assert(hasAnyDescendant(hasText("title of csv-writer")))
        node("task-row:csv-writer").assert(hasAnyDescendant(hasText("R1 R2")))
        node("pips").assert(
            hasContentDescription("red passed, implement running, green not reached, review not reached, integrate not reached"),
        )
        node("task-words:csv-writer").assertTextEquals("implement · writing tests")
        node("task-clock:csv-writer").assertTextEquals("2:05")
    }

    @Test
    fun aTaskRowTapOpensItsPhaseOnceAndAPlannedRowDoesNot() {
        val opened = mutableListOf<Pair<String, Phase>>()
        val tasks = listOf(
            task("csv-writer", RingPlace.IMPLEMENT, words = "implement", since = NOW - 30),
            task("rota-endpoint", null, words = "not started"),
        )
        composeRule.setContent {
            MaterialTheme {
                TaskTable(tasks = tasks, now = NOW, onOpenPhase = { name, phase -> opened.add(name to phase) })
            }
        }

        node("task-row:csv-writer").performClick()

        assertEquals(listOf("csv-writer" to Phase.IMPLEMENTING), opened)
        assertFalse(hasClickAction("task-row:rota-endpoint"))
        assertTrue(nodes("task-clock:rota-endpoint").isEmpty())
    }

    @Test
    fun theTableShowsTwelveTasksThenFoldsTheRestUntilTapped() {
        val tasks = (1..15).map { task("T$it", null, words = "not started") }
        composeRule.setContent {
            MaterialTheme {
                Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                    TaskTable(tasks = tasks, now = NOW)
                }
            }
        }

        for (index in 1..12) {
            assertEquals("row T$index", 1, nodes("task-row:T$index").size)
        }
        for (index in 13..15) {
            assertTrue("row T$index is folded", nodes("task-row:T$index").isEmpty())
        }
        composeRule.onNodeWithTag("task-fold").assertTextEquals("3 more tasks")

        composeRule.onNodeWithTag("task-fold").performScrollTo().performClick()

        for (index in 1..15) {
            assertEquals("row T$index after the fold", 1, nodes("task-row:T$index").size)
        }
        assertTrue(nodes("task-fold").isEmpty())
    }

    @Test
    fun aTableOfTwelveTasksHasNoFold() {
        val tasks = (1..12).map { task("T$it", null, words = "not started") }
        composeRule.setContent {
            MaterialTheme { TaskTable(tasks = tasks, now = NOW) }
        }

        assertEquals(1, nodes("task-row:T12").size)
        assertTrue(nodes("task-fold").isEmpty())
    }

    @Test
    fun aLimitedStepIsGreyAndAHeldStepIsPeachAndEachShowsItsClock() {
        val lines = listOf(
            StepLine("s1", StepGlyph.WORKING, "csv-writer", "csv-writer", "writing tests", NOW - 65),
            StepLine("s2", StepGlyph.LIMITED, "rota-endpoint", "rota-endpoint", "waits on a usage limit", NOW - 30),
            StepLine("s3", StepGlyph.HELD, "export-cli", "export-cli", "held · needs a decision", NOW - 5),
        )
        val tasks = listOf(task("csv-writer", RingPlace.IMPLEMENT, words = "implement · writing tests"))
        composeRule.setContent {
            MaterialTheme { StepRows(lines = lines, tasks = tasks, now = NOW) }
        }

        assertEquals("working", stateOf("step-glyph:s1"))
        assertEquals("limited", stateOf("step-glyph:s2"))
        assertEquals("held", stateOf("step-glyph:s3"))
        assertEquals(listOf(OrreryTokens.Role.live), colourSpansOf("step-glyph:s1"))
        assertEquals(listOf(OrreryTokens.Ink.three), colourSpansOf("step-glyph:s2"))
        assertEquals(listOf(OrreryTokens.Tone.mine), colourSpansOf("step-glyph:s3"))
        assertEquals(listOf(OrreryTokens.Ink.three), colourSpansOf("step-doing:s2"))
        assertEquals(listOf(OrreryTokens.Role.attention), colourSpansOf("step-doing:s3"))
        node("step-doing:s2").assertTextEquals("waits on a usage limit")
        node("step-clock:s1").assertTextEquals("1:05")
        node("step-clock:s2").assertTextEquals("0:30")
        node("step-clock:s3").assertTextEquals("0:05")
    }

    @Test
    fun aStepRowShowsItsTaskPipsAndFiveNoneWhenTheTaskIsNotFound() {
        val lines = listOf(
            StepLine("s1", StepGlyph.WORKING, "csv-writer", "csv-writer", "writing tests", NOW - 65),
            StepLine("s2", StepGlyph.LIMITED, "rota-endpoint", "rota-endpoint", "waits on a usage limit", NOW - 30),
        )
        val tasks = listOf(task("csv-writer", RingPlace.IMPLEMENT, words = "implement · writing tests"))
        composeRule.setContent {
            MaterialTheme { StepRows(lines = lines, tasks = tasks, now = NOW) }
        }

        node("step-row:s1").assert(
            hasAnyDescendant(
                hasContentDescription("red passed, implement running, green not reached, review not reached, integrate not reached"),
            ),
        )
        node("step-row:s2").assert(
            hasAnyDescendant(
                hasContentDescription(
                    "red not reached, implement not reached, green not reached, review not reached, integrate not reached",
                ),
            ),
        )
        node("step-row:s1").assert(hasAnyDescendant(hasText("csv-writer")))
    }

    @Test
    fun anEvidenceJudgeStepRowShowsThePipsOfTheTaskWhoseRunItJudges() {
        val lines = listOf(StepLine("s1", StepGlyph.WORKING, "evidence-run-9", "evidence-run-9", "judging", NOW - 10))
        val tasks = listOf(
            task("T1", RingPlace.GREEN, words = "green · judging").copy(labels = setOf("T1", "evidence-run-9")),
        )
        composeRule.setContent {
            MaterialTheme { StepRows(lines = lines, tasks = tasks, now = NOW) }
        }

        node("step-row:s1").assert(
            hasAnyDescendant(
                hasContentDescription("red passed, implement passed, green running, review not reached, integrate not reached"),
            ),
        )
    }

    @Test
    fun elapsedClockIsMinutesAndSecondsThenHoursFromAnHourAndNeverNegative() {
        assertEquals("0:00", elapsedClock(0))
        assertEquals("0:59", elapsedClock(59))
        assertEquals("1:00", elapsedClock(60))
        assertEquals("59:59", elapsedClock(3_599))
        assertEquals("1:00:00", elapsedClock(3_600))
        assertEquals("2:05:09", elapsedClock(7_509))
        assertEquals("0:00", elapsedClock(-5))
    }
}
