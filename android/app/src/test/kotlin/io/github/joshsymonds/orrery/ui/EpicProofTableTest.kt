package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private fun evidence(
    phase: String,
    passed: Boolean,
    created: Long,
    state: String = "ended",
): EvidenceRunRow = EvidenceRunRow(
    id = "run-$phase-$created",
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
    created = created,
    ended = created,
    logBytes = 0L,
    stderrBytes = 0L,
    artifacts = emptyList(),
)

private fun requirement(
    id: String,
    kind: String = "check",
    runs: List<EvidenceRunRow> = emptyList(),
): EpicRequirementRow = EpicRequirementRow(
    id = id,
    text = "text of $id",
    evidence = "",
    kind = kind,
    runs = runs,
)

private val noArtifact: ArtifactLoader = { _, _, _ -> null }

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicProofTableTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun rowShowsItsIdAndTextWithNoRunCellsOpen() {
        composeRule.setContent {
            MaterialTheme { RequirementTable(listOf(requirement("R1")), noArtifact) }
        }

        assertInRow("R1", "R1")
        assertInRow("R1", "text of R1")
        assertCell("req-red:R1", "no test yet", OrreryTokens.Ink.four)
        assertCell("req-green:R1", "not yet", OrreryTokens.Ink.four)
        composeRule.onNodeWithTag("req-proven:R1", useUnmergedTree = true).assertTextEquals("open")
    }

    @Test
    fun reviewRequirementWithNoRunReadsByReviewInBothCells() {
        composeRule.setContent {
            MaterialTheme { RequirementTable(listOf(requirement("R1", kind = "review")), noArtifact) }
        }

        assertCell("req-red:R1", "by review", OrreryTokens.Ink.four)
        assertCell("req-green:R1", "by review", OrreryTokens.Ink.four)
        composeRule.onNodeWithTag("req-proven:R1", useUnmergedTree = true).assertTextEquals("open")
    }

    @Test
    fun runningNewestRunReadsRunningInLive() {
        composeRule.setContent {
            MaterialTheme {
                RequirementTable(
                    listOf(
                        requirement("R1", runs = listOf(evidence("red", passed = false, created = 1L, state = "running"))),
                        requirement("R2", runs = listOf(evidence("green", passed = false, created = 1L, state = "running"))),
                    ),
                    noArtifact,
                )
            }
        }

        assertCell("req-red:R1", "running", OrreryTokens.Role.live)
        assertCell("req-green:R1", "not yet", OrreryTokens.Ink.four)
        assertCell("req-red:R2", "no test yet", OrreryTokens.Ink.four)
        assertCell("req-green:R2", "running", OrreryTokens.Role.live)
        composeRule.onNodeWithTag("req-proven:R2", useUnmergedTree = true).assertTextEquals("open")
    }

    @Test
    fun redRunPassedReadsFailsAtBaseAndNotPassedReadsPassesAtBase() {
        composeRule.setContent {
            MaterialTheme {
                RequirementTable(
                    listOf(
                        requirement("R1", runs = listOf(evidence("red", passed = true, created = 1L))),
                        requirement("R2", runs = listOf(evidence("red", passed = false, created = 1L))),
                    ),
                    noArtifact,
                )
            }
        }

        assertCell("req-red:R1", "✓ fails at base", OrreryTokens.Role.ok)
        assertCell("req-red:R2", "✗ passes at base", OrreryTokens.Role.bad)
    }

    @Test
    fun greenRunPassedReadsPassesAndNotPassedReadsFails() {
        composeRule.setContent {
            MaterialTheme {
                RequirementTable(
                    listOf(
                        requirement("R1", runs = listOf(evidence("green", passed = true, created = 1L))),
                        requirement("R2", runs = listOf(evidence("green", passed = false, created = 1L))),
                    ),
                    noArtifact,
                )
            }
        }

        assertCell("req-green:R1", "✓ passes", OrreryTokens.Role.ok)
        assertCell("req-green:R2", "✗ fails", OrreryTokens.Role.bad)
    }

    @Test
    fun newestRunOfEachPhaseDecidesItsCell() {
        composeRule.setContent {
            MaterialTheme {
                RequirementTable(
                    listOf(
                        requirement(
                            "R1",
                            runs = listOf(
                                evidence("red", passed = false, created = 2L),
                                evidence("red", passed = true, created = 1L),
                                evidence("green", passed = false, created = 1L),
                                evidence("green", passed = true, created = 3L),
                            ),
                        ),
                    ),
                    noArtifact,
                )
            }
        }

        assertCell("req-red:R1", "✗ passes at base", OrreryTokens.Role.bad)
        assertCell("req-green:R1", "✓ passes", OrreryTokens.Role.ok)
    }

    @Test
    fun rowIsProvenOnlyWhenAGreenRunPassed() {
        composeRule.setContent {
            MaterialTheme {
                RequirementTable(
                    listOf(
                        requirement("R1", runs = listOf(evidence("green", passed = true, created = 1L))),
                        requirement("R2", runs = listOf(evidence("red", passed = true, created = 1L))),
                        requirement("R3", runs = listOf(evidence("green", passed = false, created = 1L))),
                    ),
                    noArtifact,
                )
            }
        }

        composeRule.onNodeWithTag("req-proven:R1", useUnmergedTree = true).assertTextEquals("proven")
        composeRule.onNodeWithTag("req-proven:R2", useUnmergedTree = true).assertTextEquals("open")
        composeRule.onNodeWithTag("req-proven:R3", useUnmergedTree = true).assertTextEquals("open")
    }

    @Test
    fun tapOpensRunsRedFirstThenGreenAndSecondTapCloses() {
        composeRule.setContent {
            MaterialTheme {
                RequirementTable(
                    listOf(
                        requirement(
                            "R1",
                            runs = listOf(
                                evidence("green", passed = true, created = 5L),
                                evidence("red", passed = false, created = 3L),
                                evidence("red", passed = true, created = 1L),
                            ),
                        ),
                    ),
                    noArtifact,
                )
            }
        }

        composeRule.onAllNodesWithTag("req-runs:R1", useUnmergedTree = true).assertCountEquals(0)

        composeRule.onNodeWithTag("req-row:R1").performClick()

        composeRule.onAllNodesWithTag("req-runs:R1", useUnmergedTree = true).assertCountEquals(1)
        composeRule.onNodeWithTag("evidence-run:0:0", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("RED")) and hasAnyDescendant(hasText("passed")))
        composeRule.onNodeWithTag("evidence-run:0:1", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("RED")) and hasAnyDescendant(hasText("not passed")))
        composeRule.onNodeWithTag("evidence-run:0:2", useUnmergedTree = true)
            .assert(hasAnyDescendant(hasText("GREEN")))

        composeRule.onNodeWithTag("req-row:R1").performClick()

        composeRule.onAllNodesWithTag("req-runs:R1", useUnmergedTree = true).assertCountEquals(0)
    }

    private fun assertInRow(rowId: String, text: String) {
        composeRule.onAllNodes(hasText(text) and hasAnyAncestor(hasTestTag("req-row:$rowId")), useUnmergedTree = true)
            .assertCountEquals(1)
    }

    private fun assertCell(tag: String, text: String, colour: Color) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).assertTextEquals(text)
        assertEquals(listOf(colour), colourSpansOf(tag))
    }

    private fun colourSpansOf(tag: String): List<Color> =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().config[SemanticsProperties.Text]
            .single()
            .spanStyles
            .map { it.item.color }
}
