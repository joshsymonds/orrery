package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performScrollToNode
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseProof
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseRun
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReviewVerdict
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ReleaseTableTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val SLUG = "example-epic"
        const val NOW = 1_800_000_000L
        const val CHANGED = 4_102_444_800L
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun releaseStep(
        position: Long,
        state: String,
        step: String = "Example release step $position.",
        target: String = "",
        undo: String = "Example undo.",
        reason: String = "",
        evidence: String = "",
    ): EpicReleaseProof =
        EpicReleaseProof.newBuilder()
            .setPosition(position)
            .setStep(step)
            .setTarget(target)
            .setUndo(undo)
            .setState(state)
            .setReason(reason)
            .setEvidence(evidence)
            .build()

    private fun releaseRun(part: String, position: Long, attempt: Long, passed: Boolean, reason: String = ""): EpicReleaseRun =
        EpicReleaseRun.newBuilder()
            .setPart(part)
            .setPosition(position)
            .setAttempt(attempt)
            .setStep("Example release run step.")
            .setPassed(passed)
            .setReason(reason)
            .build()

    private fun reviewer(position: Long, name: String, verdict: String, reason: String): EpicReviewVerdict =
        EpicReviewVerdict.newBuilder()
            .setPosition(position)
            .setReviewer(name)
            .setStep("example-step")
            .setVerdict(verdict)
            .setReason(reason)
            .build()

    private fun body(
        outcome: String,
        steps: List<EpicReleaseProof>,
        releases: List<EpicReleaseRun> = emptyList(),
        releaseThen: String = "",
        reviews: List<EpicReviewVerdict> = emptyList(),
    ): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(EPIC_ID)
                    .setRepository("example-repo")
                    .setSlug(SLUG)
                    .setOutcome(outcome)
                    .setChanged(CHANGED)
                    .setLive(true)
                    .setSource("orreryd"),
            )
            .setRepository("/home/user/example-repo")
            .setContract(EpicContract.newBuilder().setReleaseThen(releaseThen))
            .addAllReleaseSteps(steps)
            .addAllReleases(releases)
            .addAllReviews(reviews)
            .build()

    /** Serves the epic and its empty history, the two calls a load makes. */
    private fun serve(server: FakeOrreryServer, epic: EpicsGetResponse) {
        server.respond(EPICS_GET, epic)
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
    }

    private fun setScreen(server: FakeOrreryServer, listState: LazyListState = LazyListState()) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = {},
                onOpenContract = {},
                onTalk = {},
                listedSteerer = { null },
                listState = listState,
                openAt = null,
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countNodes(matcher and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitTag(tag: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(tag)) > 0 }
    }

    /** Waits until the epic has loaded: the now section is drawn only once it has. */
    private fun awaitLoaded() = awaitTag("epic-section:now")

    private fun scrollTo(tag: String) {
        composeRule.onNode(hasScrollAction()).performScrollToNode(hasTestTag(tag))
    }

    /** The index of the item of section [name] in the list, read once the section is on screen. */
    private fun sectionIndex(listState: LazyListState, name: String): Int {
        scrollTo("epic-section:$name")
        return composeRule.runOnIdle {
            val visible = listState.layoutInfo.visibleItemsInfo.firstOrNull { it.key == "section:$name" }
            checkNotNull(visible) { "section $name is not on screen after it was scrolled to" }.index
        }
    }

    /** Asserts the text of the node tagged [tag], a text node of its own. */
    private fun assertTextAt(tag: String, text: String) {
        scrollTo(tag)
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).assertTextEquals(text)
    }

    /** Asserts that the row tagged [tag] holds a text node reading [text]. */
    private fun assertRowHolds(tag: String, text: String) {
        scrollTo(tag)
        assertEquals(1, countIn(tag, hasText(text)))
    }

    /** Asserts that the state cell tagged [tag] reads [word] and draws in [tone]. */
    private fun assertState(tag: String, word: String, tone: ReleaseTone) {
        scrollTo(tag)
        composeRule.onNodeWithTag(tag, useUnmergedTree = true)
            .assertTextEquals(word)
            .assert(SemanticsMatcher.expectValue(ReleaseToneKey, tone))
    }

    @Test
    fun theReleaseIsDrawnAfterTheReviewWithItsWordsAndItsThenLine() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "stopped",
                    steps = listOf(
                        releaseStep(1, "passed"),
                        releaseStep(2, "failed", undo = "rollback"),
                        releaseStep(3, "waiting"),
                    ),
                    releaseThen = "Example then.",
                    reviews = listOf(reviewer(1, "example-reviewer-a", "holds", "Example reason for a.")),
                ),
            )
            setScreen(server, listState)
            awaitLoaded()

            assertTextAt("release-words", "stopped at step 2 of 3")
            assertTrue(sectionIndex(listState, "release") > sectionIndex(listState, "review"))
            assertTextAt("release-then", "Then: Example then.")
        }
    }

    @Test
    fun aStepRowShowsItsPositionStepTargetAndUndo() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "releasing",
                    steps = listOf(
                        releaseStep(1, "passed", step = "Example release step.", target = "example-target", undo = "Example undo."),
                        releaseStep(2, "waiting", step = "Example release step two.", target = "", undo = ""),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertRowHolds("release-step:1", "1")
            assertRowHolds("release-step:1", "Example release step. → example-target")
            assertRowHolds("release-step:1", "Example undo.")
            assertRowHolds("release-step:2", "2")
            assertRowHolds("release-step:2", "Example release step two.")
            assertRowHolds("release-step:2", "not given")
        }
    }

    @Test
    fun eachStateCellDrawsItsWordAndTone() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "releasing",
                    steps = listOf(
                        releaseStep(1, "passed"),
                        releaseStep(2, "running"),
                        releaseStep(3, "failed", undo = "rollback"),
                        releaseStep(4, "waiting"),
                        releaseStep(5, "queued"),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertState("release-state:1", "passed", ReleaseTone.Ok)
            assertState("release-state:2", "running", ReleaseTone.Live)
            assertState("release-state:3", "failed", ReleaseTone.Bad)
            assertState("release-state:4", "waiting", ReleaseTone.None)
            assertState("release-state:5", "queued", ReleaseTone.None)
        }
    }

    @Test
    fun aFailedStepDrawsItsReasonEvidenceAndItsRunsOfItsOwnReleasePartInOrder() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "stopped",
                    steps = listOf(
                        releaseStep(
                            1,
                            "failed",
                            undo = "rollback",
                            reason = "Example step reason.",
                            evidence = "Example evidence.",
                        ),
                        releaseStep(2, "passed", reason = "Ignored reason."),
                    ),
                    releases = listOf(
                        releaseRun("release", 1, 1, passed = false, reason = "Example run reason."),
                        releaseRun("candidate", 1, 1, passed = true),
                        releaseRun("release", 1, 2, passed = true),
                        releaseRun("release", 2, 1, passed = true),
                        releaseRun("named", 1, 3, passed = false),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertTextAt("release-reason:1", "Example step reason.")
            assertTextAt("release-evidence:1", "Example evidence.")
            assertTextAt("release-run:1:0", "attempt 1 · failed · Example run reason.")
            assertTextAt("release-run:1:1", "attempt 2 · passed")
            assertTextAt("release-run:1:2", "attempt 3 · failed")
            assertEquals(0, countNodes(hasTestTag("release-run:1:3")))
            assertTextAt("release-run:2:0", "attempt 1 · passed")
            assertEquals(0, countNodes(hasTestTag("release-reason:2")))
            assertEquals(0, countNodes(hasTestTag("release-evidence:2")))
        }
    }
}
