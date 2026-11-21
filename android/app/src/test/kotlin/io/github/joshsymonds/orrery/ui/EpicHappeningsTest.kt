package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicDecisionRow
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicTellRow
import io.github.joshsymonds.orrery.api.EpicTriggerRow
import io.github.joshsymonds.orrery.api.EpicTurnRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

// 1_700_000_000 is 22:13:20 in UTC, the zone these tests run in. at(n) is n minutes after it, so its clock text is
// 22:13 plus n minutes.
private const val BASE = 1_700_000_000L

private fun at(minute: Int): Long = BASE + 60L * minute

private fun stepRow(
    id: String,
    task: String = "",
    agent: String = "",
    end: String = "",
    checks: List<StepCheckRow> = emptyList(),
): StepRow = StepRow(
    id = id,
    task = task,
    agent = agent,
    end = end,
    status = "",
    held = false,
    started = 0L,
    ended = 0L,
    doing = "",
    checks = checks,
    reason = "",
)

private fun turn(seq: Long, state: String, outcome: String = ""): EpicTurnRow = EpicTurnRow(
    seq = seq,
    step = "",
    state = state,
    reason = "",
    outline = "",
    outcome = outcome,
    decisions = emptyList(),
    started = 0L,
    ended = 0L,
)

private fun evidence(item: String, phase: String, state: String, passed: Boolean): EvidenceRunRow = EvidenceRunRow(
    id = "",
    phase = phase,
    source = "captured",
    state = state,
    exit = 0,
    timedOut = false,
    notCaptured = "",
    verdict = "",
    judgeReason = "",
    passed = passed,
    revision = "",
    created = 0L,
    ended = 0L,
    logBytes = 0L,
    stderrBytes = 0L,
    artifacts = emptyList(),
    item = item,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicHappeningsTest {
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
    fun justHappenedShowsTheNewestThreeAndMoreOpensTheRestInPlace() {
        val entries = listOf(
            EpicHistoryRow(time = at(0), cursor = "c1", kind = "turn", turn = turn(seq = 1L, state = "succeeded")),
            EpicHistoryRow(
                time = at(1),
                cursor = "c2",
                kind = "trigger",
                trigger = EpicTriggerRow(id = "", cause = "owner asked", state = "", turn = 0L, created = 0L),
            ),
            EpicHistoryRow(time = at(2), cursor = "c3", kind = "step", step = stepRow("s1", task = "b1", end = "done")),
            EpicHistoryRow(
                time = at(3),
                cursor = "c4",
                kind = "decision",
                decision = EpicDecisionRow(seq = 0L, id = "", text = "use the table", time = 0L, question = ""),
            ),
            EpicHistoryRow(
                time = at(4),
                cursor = "c5",
                kind = "tell",
                tell = EpicTellRow(seq = 0L, text = "build one is in", at = 0L, told = false),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        composeRule.onNodeWithText("JUST HAPPENED").assertIsDisplayed()
        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:17", "build one is in")
        composeRule.onNodeWithTag("happened:1").assertTextEquals("22:16", "decision by unknown · use the table")
        composeRule.onNodeWithTag("happened:2").assertTextEquals("22:15", "b1 · done")
        assertTrue(composeRule.onAllNodesWithTag("happened:3").fetchSemanticsNodes().isEmpty())
        composeRule.onNodeWithTag("happened-more").assertTextEquals("+2 earlier")

        composeRule.onNodeWithTag("happened-more").performClick()

        assertEquals(1, composeRule.onAllNodesWithTag("happened:4").fetchSemanticsNodes().size)
        assertTrue(composeRule.onAllNodesWithTag("happened:5").fetchSemanticsNodes().isEmpty())
        assertTrue(composeRule.onAllNodesWithTag("happened-more").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun anEntryAtTimeZeroShowsNoClock() {
        val entries = listOf(
            EpicHistoryRow(
                time = 0L,
                cursor = "z1",
                kind = "tell",
                tell = EpicTellRow(seq = 0L, text = "build one is in", at = 0L, told = false),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        composeRule.onNodeWithTag("happened:0").assertTextEquals("", "build one is in")
    }

    @Test
    fun eachKindOfEntryReadsItsOwnTextAndTheRestOpenInPlace() {
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "k1",
                kind = "turn",
                turn = turn(3L, "succeeded", outcome = "shipped"),
            ),
            EpicHistoryRow(time = at(1), cursor = "k2", kind = "turn", turn = turn(4L, "running")),
            EpicHistoryRow(
                time = at(2),
                cursor = "k3",
                kind = "trigger",
                trigger = EpicTriggerRow(id = "", cause = "owner asked", state = "", turn = 0L, created = 0L),
            ),
            EpicHistoryRow(time = at(3), cursor = "k4", kind = "step", step = stepRow("s1", task = "b1")),
            EpicHistoryRow(
                time = at(4),
                cursor = "k5",
                kind = "step",
                step = stepRow("s2", agent = "builder", end = "done"),
            ),
            EpicHistoryRow(
                time = at(5),
                cursor = "k6",
                kind = "evidence",
                evidence = evidence("R1", "red", state = "capturing", passed = false),
            ),
            EpicHistoryRow(
                time = at(6),
                cursor = "k7",
                kind = "evidence",
                evidence = evidence("R1", "green", state = "ended", passed = true),
            ),
            EpicHistoryRow(
                time = at(7),
                cursor = "k8",
                kind = "evidence",
                evidence = evidence("R2", "green", state = "ended", passed = false),
            ),
            EpicHistoryRow(
                time = at(8),
                cursor = "k9",
                kind = "decision",
                decision = EpicDecisionRow(seq = 0L, id = "", text = "use the table", time = 0L, question = ""),
            ),
            EpicHistoryRow(
                time = at(9),
                cursor = "k10",
                kind = "tell",
                tell = EpicTellRow(seq = 0L, text = "build one is in", at = 0L, told = false),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        composeRule.onNodeWithTag("happened-more").assertTextEquals("+7 earlier")
        composeRule.onNodeWithTag("happened-more").performClick()

        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:22", "build one is in")
        composeRule.onNodeWithTag("happened:1").assertTextEquals("22:21", "decision by unknown · use the table")
        composeRule.onNodeWithTag("happened:2").assertTextEquals("22:20", "R2 green run · failed")
        composeRule.onNodeWithTag("happened:3").assertTextEquals("22:19", "R1 green run · passed")
        composeRule.onNodeWithTag("happened:4").assertTextEquals("22:18", "R1 red run · running")
        composeRule.onNodeWithTag("happened:5").assertTextEquals("22:17", "builder · done")
        composeRule.onNodeWithTag("happened:6").assertTextEquals("22:16", "b1 · started")
        composeRule.onNodeWithTag("happened:7").assertTextEquals("22:15", "trigger · owner asked")
        composeRule.onNodeWithTag("happened:8").assertTextEquals("22:14", "turn 4 · running")
        composeRule.onNodeWithTag("happened:9").assertTextEquals("22:13", "turn 3 · succeeded · shipped")
    }

    @Test
    fun aHistoryOfThreeOrFewerHasNoMoreButton() {
        val entries = listOf(
            EpicHistoryRow(time = at(0), cursor = "f1", kind = "turn", turn = turn(1L, "succeeded")),
            EpicHistoryRow(time = at(1), cursor = "f2", kind = "turn", turn = turn(2L, "succeeded")),
            EpicHistoryRow(time = at(2), cursor = "f3", kind = "turn", turn = turn(3L, "succeeded")),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        composeRule.onNodeWithTag("happened:2").assertTextEquals("22:13", "turn 1 · succeeded")
        assertTrue(composeRule.onAllNodesWithTag("happened-more").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun aStepEntryTapOpensItsStepWhenTheEpicHoldsIt() {
        val opened = mutableListOf<StepRow>()
        val held = stepRow("s1", task = "b1", agent = "builder", end = "done")
        val entries = listOf(
            EpicHistoryRow(time = at(0), cursor = "s1", kind = "step", step = stepRow("s1", task = "b1", end = "done")),
            EpicHistoryRow(time = at(1), cursor = "s9", kind = "step", step = stepRow("s9", task = "b9", end = "done")),
        )
        composeRule.setContent {
            MaterialTheme {
                JustHappened(
                    entries = entries,
                    steps = listOf(held),
                    onOpenStep = { step -> opened.add(step) },
                )
            }
        }

        assertFalse(hasClickAction("happened:0"))
        assertTrue(hasClickAction("happened:1"))
        composeRule.onNodeWithTag("happened:1").performClick()

        assertEquals(listOf(held), opened)
    }

    @Test
    fun failedCheckTailsShowUnderTheirStepEntryAndPassedChecksShowNone() {
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "t1",
                kind = "step",
                step = stepRow("s1", task = "b1", end = "checks_failed"),
            ),
        )
        val steps = listOf(
            stepRow(
                "s2",
                task = "b2",
                end = "checks_failed",
                checks = listOf(
                    StepCheckRow(round = 1, name = "lint", ok = false, stdout = "other step out", stderr = ""),
                ),
            ),
            stepRow(
                "s1",
                task = "b1",
                end = "checks_failed",
                checks = listOf(
                    StepCheckRow(round = 1, name = "lint", ok = false, stdout = "lint out", stderr = "lint err"),
                    StepCheckRow(round = 1, name = "unit", ok = true, stdout = "unit out", stderr = ""),
                    StepCheckRow(round = 1, name = "vet", ok = false, stdout = "", stderr = ""),
                ),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = steps, onOpenStep = {}) }
        }

        composeRule.onNodeWithTag("happened-tail:0:lint", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("lint out")))
        composeRule.onNodeWithTag("happened-tail:0:lint", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("lint err")))
        assertTrue(
            composeRule.onAllNodesWithTag("happened-tail:0:unit", useUnmergedTree = true)
                .fetchSemanticsNodes().isEmpty(),
        )
        assertTrue(
            composeRule.onAllNodesWithTag("happened-tail:0:vet", useUnmergedTree = true)
                .fetchSemanticsNodes().isEmpty(),
        )
        assertTrue(
            composeRule.onAllNodesWithText("other step out", useUnmergedTree = true)
                .fetchSemanticsNodes().isEmpty(),
        )
        assertTrue(
            composeRule.onAllNodesWithText("unit out", useUnmergedTree = true)
                .fetchSemanticsNodes().isEmpty(),
        )
    }

    private fun hasClickAction(tag: String): Boolean =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.contains(SemanticsActions.OnClick)
}
