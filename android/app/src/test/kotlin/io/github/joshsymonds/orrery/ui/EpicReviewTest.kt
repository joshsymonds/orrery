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
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicClosure
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicLedgerFinding
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
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
class EpicReviewTest {
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

    private fun requirement(id: String): EpicRequirement =
        EpicRequirement.newBuilder()
            .setId(id)
            .setText("Example requirement text.")
            .setEvidence("example evidence")
            .setKind("test")
            .build()

    private fun reviewer(position: Long, name: String, verdict: String, reason: String): EpicReviewVerdict =
        EpicReviewVerdict.newBuilder()
            .setPosition(position)
            .setReviewer(name)
            .setStep("example-step")
            .setVerdict(verdict)
            .setReason(reason)
            .build()

    private fun closure(round: Long, result: String, reason: String): EpicClosure =
        EpicClosure.newBuilder()
            .setRound(round)
            .setStep("example-step")
            .setResult(result)
            .setReason(reason)
            .build()

    private fun finding(
        id: String,
        claim: String,
        location: String,
        result: String,
        unverified: Boolean = false,
        reason: String = "",
        closures: List<EpicClosure> = emptyList(),
    ): EpicLedgerFinding =
        EpicLedgerFinding.newBuilder()
            .setId(id)
            .setPosition(1)
            .setReviewer("example-reviewer")
            .setClaim(claim)
            .setCitation("example citation")
            .setLocation(location)
            .setEvidence("example evidence")
            .setStep("example-step")
            .setResult(result)
            .setUnverified(unverified)
            .setReason(reason)
            .addAllClosures(closures)
            .build()

    private fun body(
        outcome: String,
        reviews: List<EpicReviewVerdict> = emptyList(),
        ledger: List<EpicLedgerFinding> = emptyList(),
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
            .addRequirements(requirement("R1"))
            .addAllReviews(reviews)
            .addAllLedger(ledger)
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

    /** Asserts that the chip tagged [tag] reads [word] and draws in [tone]. */
    private fun assertChip(tag: String, word: String, tone: ChipTone) {
        scrollTo(tag)
        composeRule.onNodeWithTag(tag, useUnmergedTree = true)
            .assertTextEquals(word)
            .assert(SemanticsMatcher.expectValue(ChipToneKey, tone))
    }

    @Test
    fun theReviewIsDrawnAfterTheRequirementsWithItsCountWordsAndItsReviewers() {
        val listState = LazyListState()
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "reviewing",
                    reviews = listOf(
                        reviewer(1, "example-reviewer-a", "holds", "Example reason for a."),
                        reviewer(2, "example-reviewer-b", "fails", "Example reason for b."),
                    ),
                    ledger = listOf(finding("example-reviewer-a-1", "Example claim.", "", "dropped")),
                ),
            )
            setScreen(server, listState)
            awaitLoaded()

            assertTextAt("review-words", "2 reviewers · 1 finding")
            assertTrue(sectionIndex(listState, "review") > sectionIndex(listState, "requirements"))
            assertRowHolds("review-reviewer:example-reviewer-a", "example-reviewer-a")
            assertRowHolds("review-reviewer:example-reviewer-a", "Example reason for a.")
            assertRowHolds("review-reviewer:example-reviewer-b", "example-reviewer-b")
            assertRowHolds("review-reviewer:example-reviewer-b", "Example reason for b.")
            assertChip("review-verdict:example-reviewer-a", "holds", ChipTone.Ok)
            assertChip("review-verdict:example-reviewer-b", "fails", ChipTone.Bad)
        }
    }

    @Test
    fun aGapAndAnyOtherVerdictDrawAttentionAndQuietChips() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "reviewing",
                    reviews = listOf(
                        reviewer(1, "example-reviewer-c", "gap", "Example reason for c."),
                        reviewer(2, "example-reviewer-d", "unknown", "Example reason for d."),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertChip("review-verdict:example-reviewer-c", "gap", ChipTone.Attention)
            assertChip("review-verdict:example-reviewer-d", "unknown", ChipTone.Neutral)
        }
    }

    @Test
    fun aFindingShowsItsIdClaimAndLocationWithItsReasonAndClosuresInRoundOrder() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "reviewing",
                    ledger = listOf(
                        finding(
                            id = "example-reviewer-1",
                            claim = "Example claim.",
                            location = "example-location",
                            result = "confirmed",
                            reason = "Example verify reason",
                            closures = listOf(
                                closure(1, "closed", "Example first closure"),
                                closure(2, "open", ""),
                            ),
                        ),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertRowHolds("review-finding:example-reviewer-1", "example-reviewer-1")
            assertRowHolds("review-finding:example-reviewer-1", "Example claim. · example-location")
            assertChip("review-result:example-reviewer-1", "confirmed", ChipTone.Bad)
            assertTextAt("review-reason:example-reviewer-1", "Example verify reason")
            assertChip("review-closure:example-reviewer-1:0", "round 1 · closed · Example first closure", ChipTone.Ok)
            assertChip("review-closure:example-reviewer-1:1", "round 2 · open", ChipTone.Attention)
        }
    }

    @Test
    fun aFindingsResultDrawsItsOwnChipTone() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "reviewing",
                    ledger = listOf(
                        finding("example-reviewer-2", "Example claim.", "", "dropped"),
                        finding("example-reviewer-3", "Example claim.", "", "undetermined"),
                        finding("example-reviewer-4", "Example claim.", "", "", unverified = true),
                        finding("example-reviewer-5", "Example claim.", "", ""),
                    ),
                ),
            )
            setScreen(server)
            awaitLoaded()

            assertChip("review-result:example-reviewer-2", "dropped", ChipTone.Neutral)
            assertChip("review-result:example-reviewer-3", "undetermined", ChipTone.Attention)
            assertChip("review-result:example-reviewer-4", "left unverified", ChipTone.Attention)
            assertChip("review-result:example-reviewer-5", "being verified", ChipTone.Live)
        }
    }

    @Test
    fun theReviewIsNotDrawnForAnEpicThatIsStillBuilding() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                body(
                    outcome = "building",
                    reviews = listOf(reviewer(1, "example-reviewer-e", "holds", "Example reason for e.")),
                    ledger = listOf(finding("example-reviewer-e-1", "Example claim.", "", "dropped")),
                ),
            )
            setScreen(server)
            awaitLoaded()
            awaitTag("epic-section:requirements")

            assertEquals(0, countNodes(hasTestTag("epic-section:review")))
        }
    }
}
