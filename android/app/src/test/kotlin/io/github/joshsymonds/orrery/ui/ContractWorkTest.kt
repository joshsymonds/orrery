package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.ContractRequirementTaskRow
import io.github.joshsymonds.orrery.api.ContractRequirementWorkRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "Example epic"

private fun work(
    id: String,
    state: String,
    tasks: List<ContractRequirementTaskRow> = emptyList(),
): ContractRequirementWorkRow = ContractRequirementWorkRow(
    id = id,
    text = "text of $id",
    state = state,
    tasks = tasks,
)

private fun task(id: String, title: String, status: String): ContractRequirementTaskRow =
    ContractRequirementTaskRow(id = id, title = title, status = status)

/**
 * A run of [phase] that passed or not, created at [created]. A red run passes when its judge finds the conclusion fails, and
 * a green run when it finds the conclusion holds, so the verdict follows the phase and the result.
 */
private fun runOf(
    phase: String,
    passed: Boolean,
    created: Long = 1L,
    state: String = "ended",
): EvidenceRunRow {
    val verdict = if ((phase == "red") != passed) "holds" else "fails"
    return EvidenceRunRow(
        id = "run-$phase-$created",
        phase = phase,
        source = "captured",
        state = state,
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = verdict,
        judgeReason = "",
        passed = passed,
        revision = "",
        created = created,
        ended = if (state == "ended") created else 0L,
        logBytes = 0L,
        stderrBytes = 0L,
        artifacts = emptyList(),
    )
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractWorkTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var opened = 0

    private fun setWork(
        work: List<ContractRequirementWorkRow>,
        runs: Map<String, List<EvidenceRunRow>> = emptyMap(),
    ) {
        composeRule.setContent {
            MaterialTheme {
                ContractWork(work = work, runs = runs, epic = EPIC, onOpenEpic = { opened++ })
            }
        }
    }

    @Test
    fun headCountsTheProvenRowsAndNamesTheEpicWithOpenTheEpic() {
        setWork(
            listOf(
                work("R1", "proven"),
                work("R2", "proven"),
                work("R3", "being worked"),
                work("R4", "failed"),
            ),
        )

        assertOne(hasTestTag(CONTRACT_WORK_TAG))
        assertOne(hasText("THE WORK"))
        assertOne(hasText("2 of 4 requirements proven"))
        assertOne(hasText(EPIC))
        assertOne(hasText("open the epic"))
    }

    @Test
    fun headCountsZeroWhenNoRequirementIsProven() {
        setWork(listOf(work("R1", "not started"), work("R2", "failed")))

        assertOne(hasText("0 of 2 requirements proven"))
    }

    @Test
    fun openingTheEpicCallsOnOpenEpicOnce() {
        setWork(listOf(work("R1", "proven")))

        composeRule.onNodeWithText("open the epic").performClick()

        assertEquals(1, opened)
    }

    @Test
    fun eachRowShowsItsIdTextAndItsStateInWordsWithItsTone() {
        setWork(
            listOf(
                work("R1", "not started"),
                work("R2", "being worked"),
                work("R3", "proven"),
                work("R4", "failed"),
            ),
        )

        assertInRow("R1", "R1")
        assertInRow("R1", "text of R1")
        assertState("R1", "not started", ChipTone.Neutral)
        assertState("R2", "being worked", ChipTone.Live)
        assertState("R3", "proven", ChipTone.Ok)
        assertState("R4", "failed", ChipTone.Bad)
    }

    @Test
    fun aRowShowsEachTaskWithItsTitleAndStatus() {
        setWork(
            listOf(
                work(
                    "R1",
                    "being worked",
                    listOf(task("B1", "Build the parser", "done"), task("B2", "Write the tests", "planned")),
                ),
            ),
        )

        assertInRow("R1", "Build the parser")
        assertInRow("R1", "done")
        assertInRow("R1", "Write the tests")
        assertInRow("R1", "planned")
    }

    @Test
    fun aRequirementWithNoTaskReadsNoTaskYetAndOtherRowsDoNot() {
        setWork(
            listOf(
                work("R1", "not started"),
                work("R2", "being worked", listOf(task("B1", "Build the parser", "planned"))),
            ),
        )

        assertInRow("R1", "no task yet")
        assertAbsentFromRow("R2", "no task yet")
    }

    @Test
    fun eachRedWordReadsFromTheRedRunsOfTheRequirement() {
        setWork(
            listOf(work("R1", "not started"), work("R2", "not started"), work("R3", "not started"), work("R4", "not started")),
            runs = mapOf(
                "R1" to listOf(runOf("red", passed = true)),
                "R2" to listOf(runOf("red", passed = false)),
                "R3" to listOf(runOf("red", passed = false, state = "capturing")),
                "R4" to listOf(runOf("green", passed = true)),
            ),
        )

        assertProof("work-red:R1", "fails at base", ChipTone.Ok)
        assertProof("work-red:R2", "passes at base", ChipTone.Bad)
        assertProof("work-red:R3", "running", ChipTone.Live)
        assertProof("work-red:R4", "no test yet", ChipTone.Neutral)
    }

    @Test
    fun eachGreenWordReadsFromTheGreenRunsOfTheRequirement() {
        setWork(
            listOf(work("G1", "proven"), work("G2", "failed"), work("G3", "being worked"), work("G4", "not started")),
            runs = mapOf(
                "G1" to listOf(runOf("green", passed = true)),
                "G2" to listOf(runOf("green", passed = false)),
                "G3" to listOf(runOf("green", passed = false, state = "judging")),
                "G4" to listOf(runOf("red", passed = true)),
            ),
        )

        assertProof("work-green:G1", "passes", ChipTone.Ok)
        assertProof("work-green:G2", "fails", ChipTone.Bad)
        assertProof("work-green:G3", "running", ChipTone.Live)
        assertProof("work-green:G4", "not yet", ChipTone.Neutral)
    }

    @Test
    fun eachPhaseReadsItsNewestRunByCreationTime() {
        setWork(
            listOf(work("R1", "being worked"), work("R2", "being worked")),
            runs = mapOf(
                "R1" to listOf(
                    runOf("red", passed = true, created = 1L),
                    runOf("red", passed = false, created = 2L),
                    runOf("green", passed = false, created = 1L),
                    runOf("green", passed = true, created = 3L),
                ),
                "R2" to listOf(
                    runOf("red", passed = true, created = 1L),
                    runOf("red", passed = true, created = 2L, state = "capturing"),
                ),
            ),
        )

        assertProof("work-red:R1", "passes at base", ChipTone.Bad)
        assertProof("work-green:R1", "passes", ChipTone.Ok)
        assertProof("work-red:R2", "running", ChipTone.Live)
        assertProof("work-green:R2", "not yet", ChipTone.Neutral)
    }

    private fun assertState(id: String, word: String, tone: ChipTone) {
        composeRule.onNodeWithTag("work-state:$id", useUnmergedTree = true)
            .assertTextEquals(word)
            .assert(SemanticsMatcher.expectValue(ChipToneKey, tone))
    }

    private fun assertProof(tag: String, word: String, tone: ChipTone) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true)
            .assertTextEquals(word)
            .assert(SemanticsMatcher.expectValue(ChipToneKey, tone))
    }

    private fun assertOne(matcher: SemanticsMatcher) {
        composeRule.onAllNodes(matcher).assertCountEquals(1)
    }

    private fun assertInRow(rowId: String, text: String) {
        composeRule.onAllNodes(hasText(text) and hasAnyAncestor(hasTestTag(WORK_ROW_TAG_PREFIX + rowId)), useUnmergedTree = true)
            .assertCountEquals(1)
    }

    private fun assertAbsentFromRow(rowId: String, text: String) {
        composeRule.onAllNodes(hasText(text) and hasAnyAncestor(hasTestTag(WORK_ROW_TAG_PREFIX + rowId)), useUnmergedTree = true)
            .assertCountEquals(0)
    }
}
