package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicReleaseStepRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicProofTest {
    private companion object {
        const val REVISION = "0123456789abcdef0123456789abcdef01234567"
        const val CHANGED = 4_102_444_800L
        const val DIM = "dim"
        const val REACHED = "reached"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun run(
        id: String,
        phase: String,
        verdict: String,
        passed: Boolean = true,
    ): EvidenceRunRow =
        EvidenceRunRow(
            id = id,
            phase = phase,
            source = "captured",
            state = "ended",
            exit = 0,
            timedOut = false,
            notCaptured = "",
            verdict = verdict,
            judgeReason = "",
            passed = passed,
            revision = REVISION,
            created = CHANGED - 600,
            ended = CHANGED - 60,
            logBytes = 0L,
            stderrBytes = 0L,
            artifacts = emptyList(),
        )

    private fun requirement(
        id: String,
        text: String,
        kind: String = "check",
        conclusion: String = "",
        runs: List<EvidenceRunRow> = emptyList(),
    ): EpicRequirementRow =
        EpicRequirementRow(
            id = id,
            text = text,
            evidence = "example check",
            kind = kind,
            conclusion = conclusion,
            runs = runs,
        )

    private fun contract(steps: List<EpicReleaseStepRow>, then: String = "example then line"): EpicContractRow =
        EpicContractRow(
            title = "example title",
            approved = "",
            intent = "",
            failures = emptyList(),
            care = "",
            additions = emptyList(),
            premises = emptyList(),
            exclusions = emptyList(),
            approach = "",
            alternatives = emptyList(),
            releaseSteps = steps,
            releaseThen = then,
            checks = "",
            base = "",
            accepted = CHANGED,
        )

    private fun wedge(task: String, requirements: List<String>): Wedge =
        Wedge(
            task = task,
            title = "example title $task",
            requirements = requirements,
            place = WedgePlace.IMPLEMENTING,
            tone = WedgeTone.RUN,
            slices = emptyList(),
        )

    private fun setSections(
        requirements: List<EpicRequirementRow>,
        contract: EpicContractRow? = null,
        released: Boolean = false,
        wedges: List<Wedge> = emptyList(),
        onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
    ) {
        composeRule.setContent {
            Column {
                RedProof(
                    requirements = requirements,
                    loadArtifact = { _, _, _ -> null },
                    wedges = wedges,
                    onOpenPhase = onOpenPhase,
                )
                GreenProof(
                    requirements = requirements,
                    loadArtifact = { _, _, _ -> null },
                    wedges = wedges,
                    onOpenPhase = onOpenPhase,
                )
                ReleaseProof(contract = contract, released = released)
                Report()
            }
        }
    }

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true)
            .fetchSemanticsNodes().size

    private fun stateOf(tag: String): String? =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode()
            .config.getOrNull(SemanticsProperties.StateDescription)

    private fun hasClickAction(tag: String): Boolean =
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode()
            .config.contains(SemanticsActions.OnClick)

    @Test
    fun redCardsHoldOnlyRedRunsInRequirementOrder() {
        setSections(
            listOf(
                requirement(
                    "R1",
                    "example requirement one",
                    runs = listOf(
                        run("example-run-1", "red", "example red one", passed = false),
                        run("example-run-2", "green", "example green one"),
                    ),
                ),
                requirement(
                    "R2",
                    "example requirement two",
                    runs = listOf(run("example-run-3", "green", "example green two")),
                ),
            ),
        )

        assertEquals(1, countIn("proof-card:red:0", hasText("example red one")))
        assertEquals(0, countIn("proof-card:red:0", hasText("example green one")))
        assertEquals(1, countIn("proof-card:red:1", hasText("R2")))
        assertEquals(0, countIn("proof-card:red:1", hasText("example green two")))

        val first = composeRule.onNodeWithTag("proof-card:red:0", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val second = composeRule.onNodeWithTag("proof-card:red:1", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        assertTrue(first.bottom <= second.top)
    }

    @Test
    fun greenCardsHoldOnlyGreenRuns() {
        setSections(
            listOf(
                requirement(
                    "R1",
                    "example requirement one",
                    runs = listOf(
                        run("example-run-1", "red", "example red one", passed = false),
                        run("example-run-2", "green", "example green one"),
                    ),
                ),
                requirement(
                    "R2",
                    "example requirement two",
                    runs = listOf(run("example-run-3", "green", "example green two")),
                ),
            ),
        )

        assertEquals(1, countIn("proof-card:green:0", hasText("example green one")))
        assertEquals(0, countIn("proof-card:green:0", hasText("example red one")))
        assertEquals(1, countIn("proof-card:green:1", hasText("R2")))
        assertEquals(1, countIn("proof-card:green:1", hasText("example green two")))
    }

    @Test
    fun aCardShowsItsIdTextKindAndConclusionAndOmitsAnEmptyKindOrConclusion() {
        setSections(
            listOf(
                requirement("R1", "example requirement one", kind = "check", conclusion = "example conclusion one"),
                requirement("R2", "example requirement two", kind = "", conclusion = ""),
            ),
        )

        assertEquals(1, countIn("proof-card:red:0", hasText("R1")))
        assertEquals(1, countIn("proof-card:red:0", hasText("example requirement one")))
        assertEquals(1, countIn("proof-card:red:0", hasText("check")))
        assertEquals(1, countIn("proof-card:red:0", hasText("example conclusion one")))
        assertEquals(0, countIn("proof-card:red:1", hasText("")))
    }

    @Test
    fun redIsDimWithoutARedRun() {
        setSections(listOf(requirement("R1", "example requirement one", runs = listOf(run("example-run-1", "green", "example green one")))))
        assertEquals(DIM, stateOf("proof:red"))
    }

    @Test
    fun redIsReachedByAnUnpassedRedRun() {
        setSections(
            listOf(requirement("R1", "example requirement one", runs = listOf(run("example-run-1", "red", "example red one", passed = false)))),
        )
        assertEquals(REACHED, stateOf("proof:red"))
    }

    @Test
    fun greenIsDimWhileNoGreenRunPassedAndIgnoresPassedRedRuns() {
        setSections(
            listOf(
                requirement(
                    "R1",
                    "example requirement one",
                    runs = listOf(
                        run("example-run-1", "red", "example red one", passed = true),
                        run("example-run-2", "green", "example green one", passed = false),
                    ),
                ),
            ),
        )
        assertEquals(DIM, stateOf("proof:green"))
    }

    @Test
    fun greenIsReachedByAPassedGreenRun() {
        setSections(
            listOf(requirement("R1", "example requirement one", runs = listOf(run("example-run-1", "green", "example green one")))),
        )
        assertEquals(REACHED, stateOf("proof:green"))
    }

    @Test
    fun releaseIsDimUntilReleasedButStillDrawsItsSteps() {
        setSections(
            listOf(requirement("R1", "example requirement one")),
            contract = contract(listOf(EpicReleaseStepRow("example step one", "example target one", "example undo one"))),
            released = false,
        )

        assertEquals(DIM, stateOf("proof:release"))
        assertEquals(1, countIn("release-step:0", hasText("example step one")))
    }

    @Test
    fun releaseIsReachedOnlyWhenReleased() {
        setSections(listOf(requirement("R1", "example requirement one")), contract = contract(emptyList()), released = true)
        assertEquals(REACHED, stateOf("proof:release"))
    }

    @Test
    fun aReleaseStepShowsItsTargetAndUndoAndTheThenLineFollowsTheSteps() {
        setSections(
            listOf(requirement("R1", "example requirement one")),
            contract = contract(
                listOf(
                    EpicReleaseStepRow("example step one", "example target one", "example undo one"),
                    EpicReleaseStepRow("example step two", "example target two", "example undo two"),
                ),
                then = "example then line",
            ),
        )

        assertEquals(1, countIn("release-step:0", hasText("example target one")))
        assertEquals(1, countIn("release-step:0", hasText("example undo one")))
        assertEquals(1, countIn("release-step:1", hasText("example target two")))
        assertEquals(1, countIn("release-step:1", hasText("example undo two")))
        assertEquals(1, countIn("release-step:1", hasText("example step two")))
        assertEquals(0, composeRule.onAllNodesWithTag("release-step:2", useUnmergedTree = true).fetchSemanticsNodes().size)

        val lastStep = composeRule.onNodeWithTag("release-step:1", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val thenLine = composeRule.onNodeWithText("example then line", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        assertTrue(lastStep.bottom <= thenLine.top)
    }

    @Test
    fun noContractShowsNoReleaseSteps() {
        setSections(listOf(requirement("R1", "example requirement one")), contract = null)
        assertEquals(0, composeRule.onAllNodesWithTag("release-step:0", useUnmergedTree = true).fetchSemanticsNodes().size)
        assertEquals(0, countIn("proof:release", hasText("example then line")))
    }

    @Test
    fun theReportIsAlwaysDimAndCarriesOnlyItsHeading() {
        setSections(
            listOf(requirement("R1", "example requirement one", runs = listOf(run("example-run-1", "red", "example red one")))),
            contract = contract(listOf(EpicReleaseStepRow("example step one", "example target one", "example undo one"))),
            released = true,
        )

        assertEquals(DIM, stateOf("proof:report"))
        assertEquals(1, countIn("proof:report", SemanticsMatcher("any text") { it.config.contains(SemanticsProperties.Text) }))
    }

    @Test
    fun eachSectionIsHeadedByItsName() {
        setSections(listOf(requirement("R1", "example requirement one")), contract = contract(emptyList()))

        assertEquals(1, countIn("proof:red", hasText("Red proof")))
        assertEquals(1, countIn("proof:green", hasText("Green proof")))
        assertEquals(1, countIn("proof:release", hasText("Release proof")))
        assertEquals(1, countIn("proof:report", hasText("Report")))
    }

    @Test
    fun aRedCardTapOpensTheFirstWedgeNamingItsRequirementAtImplementing() {
        val opened = mutableListOf<Pair<String, Phase>>()
        setSections(
            requirements = listOf(requirement("R1", "example requirement one")),
            wedges = listOf(wedge("example-task-1", listOf("R1")), wedge("example-task-2", listOf("R1"))),
            onOpenPhase = { task, phase -> opened += task to phase },
        )

        composeRule.onNodeWithTag("proof-card:red:0").performClick()

        assertEquals(listOf("example-task-1" to Phase.IMPLEMENTING), opened)
    }

    @Test
    fun aGreenCardTapOpensTheFirstWedgeNamingItsRequirementAtChecking() {
        val opened = mutableListOf<Pair<String, Phase>>()
        setSections(
            requirements = listOf(requirement("R1", "example requirement one")),
            wedges = listOf(wedge("example-task-1", listOf("R1")), wedge("example-task-2", listOf("R1"))),
            onOpenPhase = { task, phase -> opened += task to phase },
        )

        composeRule.onNodeWithTag("proof-card:green:0").performClick()

        assertEquals(listOf("example-task-1" to Phase.CHECKING), opened)
    }

    @Test
    fun aCardNoWedgeNamesHasNoClickAction() {
        setSections(
            requirements = listOf(
                requirement("R1", "example requirement one"),
                requirement("R2", "example requirement two"),
            ),
            wedges = listOf(wedge("example-task-1", listOf("R1"))),
            onOpenPhase = { _, _ -> },
        )

        assertTrue(hasClickAction("proof-card:red:0"))
        assertTrue(hasClickAction("proof-card:green:0"))
        assertFalse(hasClickAction("proof-card:red:1"))
        assertFalse(hasClickAction("proof-card:green:1"))
    }

    @Test
    fun withoutTheCallbackNoCardHasAClickAction() {
        setSections(
            requirements = listOf(requirement("R1", "example requirement one")),
            wedges = listOf(wedge("example-task-1", listOf("R1"))),
        )

        assertFalse(hasClickAction("proof-card:red:0"))
        assertFalse(hasClickAction("proof-card:green:0"))
    }
}
