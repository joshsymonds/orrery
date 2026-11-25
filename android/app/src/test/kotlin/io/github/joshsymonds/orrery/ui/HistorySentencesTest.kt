package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.api.EpicDecisionRow
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicTellRow
import io.github.joshsymonds.orrery.api.EpicTurnRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryMono
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
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
    end: String = "",
    checks: List<StepCheckRow> = emptyList(),
): StepRow = StepRow(
        id = id,
        task = task,
        agent = "",
        end = end,
        status = "",
        held = false,
        started = 0L,
        ended = 0L,
        doing = "",
        checks = checks,
        reason = "",
    )

private fun turn(
    seq: Long,
    state: String,
    outcome: String = "",
    outline: String = "",
    decisions: List<String> = emptyList(),
): EpicTurnRow = EpicTurnRow(
    seq = seq,
    step = "",
    state = state,
    reason = "",
    outline = outline,
    outcome = outcome,
    decisions = decisions,
    started = 0L,
    ended = 0L,
)

private fun run(
    id: String,
    item: String,
    phase: String,
    passed: Boolean,
    verdict: String = "",
    judgeReason: String = "",
    judgeStep: String = "",
): EvidenceRunRow = EvidenceRunRow(
    id = id,
    phase = phase,
    source = "captured",
    state = "ended",
    exit = 0,
    timedOut = false,
    notCaptured = "",
    verdict = verdict,
    judgeReason = judgeReason,
    passed = passed,
    revision = "",
    created = 0L,
    ended = 0L,
    logBytes = 0L,
    stderrBytes = 0L,
    artifacts = emptyList(),
    item = item,
    judgeStep = judgeStep,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class HistorySentencesTest {
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
    fun historySentenceLineReadsTheSentenceItsEntryWasGivenNotALabelBuiltFromItsFields() {
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "q1",
                kind = "evidence",
                sentence = "Green evidence: R1 holds",
                evidence = run("r1", "R1", "green", passed = true, verdict = "holds"),
                runs = listOf(run("r1", "R1", "green", passed = true, verdict = "holds")),
            ),
            EpicHistoryRow(
                time = at(1),
                cursor = "q2",
                kind = "step",
                sentence = "B1 ended done: all green",
                step = stepRow("s1", task = "b1", end = "done"),
            ),
            EpicHistoryRow(
                time = at(2),
                cursor = "q3",
                kind = "decision",
                sentence = "Decision by owner: use the table",
                decision = EpicDecisionRow(
                    seq = 1L,
                    id = "d1",
                    text = "use the table",
                    time = 0L,
                    question = "",
                    by = "owner",
                ),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:15", "Decision by owner: use the table")
        composeRule.onNodeWithTag("happened:1").assertTextEquals("22:14", "B1 ended done: all green")
        composeRule.onNodeWithTag("happened:2").assertTextEquals("22:13", "Green evidence: R1 holds")
    }

    @Test
    fun historySentenceLineExpandsInPlaceToItsDetailAndShutsOnASecondTap() {
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "x1",
                kind = "turn",
                sentence = "Planner turn 1 succeeded: outlined the export",
                turn = turn(
                    seq = 1L,
                    state = "succeeded",
                    outcome = "outlined the export",
                    outline = "shift-store, rota-endpoint",
                    decisions = listOf("use the table", "keep the audit log"),
                ),
            ),
            EpicHistoryRow(
                time = at(1),
                cursor = "x2",
                kind = "tell",
                sentence = "To tell the owner: review pending",
                tell = EpicTellRow(seq = 0L, text = "review pending", at = 0L, told = false),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        assertEquals("collapsed", stateOf("happened:1"))
        assertEquals(0, countNodes(hasTestTag("happened-detail:1")))
        assertFalse(hasClickAction("happened:0"))

        composeRule.onNodeWithTag("happened:1").performClick()

        composeRule.onNodeWithTag("happened-outline:1", useUnmergedTree = true)
            .assertTextEquals("shift-store, rota-endpoint")
        composeRule.onNodeWithTag("happened-decision:1:0", useUnmergedTree = true).assertTextEquals("use the table")
        composeRule.onNodeWithTag("happened-decision:1:1", useUnmergedTree = true)
            .assertTextEquals("keep the audit log")
        assertEquals("expanded", stateOf("happened:1"))
        composeRule.onNodeWithTag("happened:0").assertTextEquals("22:14", "To tell the owner: review pending")

        composeRule.onNodeWithTag("happened:1").performClick()

        assertEquals(0, countNodes(hasTestTag("happened-detail:1")))
        assertEquals("collapsed", stateOf("happened:1"))
    }

    @Test
    fun historySentenceStepLineOpensItsStepFromItsDetailAndAnUnheldStepDoesNot() {
        val opened = mutableListOf<StepRow>()
        val lint = StepCheckRow(
            round = 1,
            name = "lint",
            ok = false,
            stdout = "lint out",
            stderr = "",
            detail = "exit 1",
        )
        val held = stepRow("s1", task = "b1", end = "checks_failed", checks = listOf(lint))
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "p1",
                kind = "step",
                sentence = "B1 ended checks_failed",
                step = stepRow("s1", task = "b1", end = "checks_failed"),
            ),
            EpicHistoryRow(
                time = at(1),
                cursor = "p2",
                kind = "step",
                sentence = "B9 ended done",
                step = stepRow("s9", task = "b9", end = "done"),
            ),
        )
        composeRule.setContent {
            MaterialTheme {
                JustHappened(entries = entries, steps = listOf(held), onOpenStep = { step -> opened.add(step) })
            }
        }

        assertFalse(hasClickAction("happened:0"))
        composeRule.onNodeWithTag("happened:1").performClick()

        composeRule.onNodeWithTag("happened-check:1:lint", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("round 1 · lint · failed")))
        composeRule.onNodeWithTag("happened-check:1:lint", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("exit 1")))
        composeRule.onNodeWithTag("happened-tail:1:lint", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("lint out")))
        assertTrue(opened.isEmpty())

        composeRule.onNodeWithTag("happened-open:1", useUnmergedTree = true).performClick()

        assertEquals(listOf(held), opened)
    }

    @Test
    fun historySentenceEvidenceLineOpensEachRunsJudgeStepAndAJudgelessRunHasNoDoor() {
        val opened = mutableListOf<StepRow>()
        val judge = stepRow("st-judge", task = "judge", end = "done")
        val judged = run(
            "r1",
            "R1",
            "green",
            passed = true,
            verdict = "holds",
            judgeReason = "the export writes the file",
            judgeStep = "st-judge",
        )
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "e1",
                kind = "evidence",
                sentence = "Green evidence: R1 holds; R2 not judged",
                evidence = judged,
                runs = listOf(judged, run("r2", "R2", "green", passed = false)),
            ),
        )
        composeRule.setContent {
            MaterialTheme {
                JustHappened(entries = entries, steps = listOf(judge), onOpenStep = { step -> opened.add(step) })
            }
        }

        composeRule.onNodeWithTag("happened:0").performClick()

        composeRule.onNodeWithTag("happened-run:0:0", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("passed · exit 0")))
        composeRule.onNodeWithTag("happened-run:0:0", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("judge: holds · the export writes the file")))
        composeRule.onNodeWithTag("happened-run:0:1", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("judge: no verdict")))
        assertEquals("a run with no judge step has no door", 0, countNodes(hasTestTag("happened-judge:0:1")))

        composeRule.onNodeWithTag("happened-judge:0:0", useUnmergedTree = true).performClick()

        assertEquals(listOf(judge), opened)
    }

    @Test
    fun historySentenceEachLineTimeIsDrawnInOrreryMonoAtTheSmallSize() {
        val entries = listOf(
            EpicHistoryRow(
                time = at(0),
                cursor = "m1",
                kind = "turn",
                sentence = "Planner turn 2 succeeded",
                turn = turn(seq = 2L, state = "succeeded"),
            ),
            EpicHistoryRow(
                time = at(1),
                cursor = "m2",
                kind = "tell",
                sentence = "To tell the owner: build one is in",
                tell = EpicTellRow(seq = 0L, text = "build one is in", at = 0L, told = false),
            ),
        )
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = entries, steps = emptyList(), onOpenStep = {}) }
        }

        for (tag in listOf("happened-at:0", "happened-at:1")) {
            val style = textLayoutOf(tag).layoutInput.style
            assertEquals(OrreryMono, style.fontFamily)
            assertEquals(OrreryTokens.Type.small.fontSize, style.fontSize)
        }
    }

    @Test
    fun historySentenceHeadIsTheSectionHeadWithTheRetryIconInHueOne() {
        composeRule.setContent {
            MaterialTheme { JustHappened(entries = emptyList(), steps = emptyList(), onOpenStep = {}) }
        }

        composeRule.onNodeWithText("JUST HAPPENED").assertIsDisplayed()
        composeRule.onNode(hasTestTag("happened-head"), useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("JUST HAPPENED")))
        val icon = composeRule.onNodeWithTag("happened-head:icon", useUnmergedTree = true).fetchSemanticsNode().config
        assertEquals(Glyph.RETRY, icon.getOrNull(HappenedGlyphKey))
        assertEquals(OrreryTokens.Series.one, icon.getOrNull(HappenedHueKey))
    }

    private fun stateOf(tag: String): String? =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.getOrNull(SemanticsProperties.StateDescription)

    private fun hasClickAction(tag: String): Boolean =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.contains(SemanticsActions.OnClick)

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun textLayoutOf(tag: String): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        val node = composeRule.onNode(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNode()
        node.config[SemanticsActions.GetTextLayoutResult].action!!.invoke(layouts)
        return layouts.single()
    }
}
