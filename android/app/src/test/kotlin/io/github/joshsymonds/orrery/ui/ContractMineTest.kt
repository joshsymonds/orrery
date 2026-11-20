package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone
import java.util.concurrent.atomic.AtomicReference

private const val IRREVERSIBLE = 7
private const val DECISIONS = 8

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ContractMineTest {
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
    fun aSectionWithAnOpenDecisionIsAttention() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION, SETTLED_DECISION))

        composeRule.onNodeWithTag("contract-section:0").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
        composeRule.scrollTo("contract-section:$DECISIONS")
        composeRule.onNodeWithTag("contract-section:$DECISIONS").assert(SemanticsMatcher.expectValue(AttentionKey, true))
    }

    @Test
    fun anOpenDecisionIsAnAttentionRowAndASettledOneIsOnlyANote() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION, SETTLED_DECISION))

        composeRule.scrollTo("contract-row:D1")
        composeRule.onNodeWithTag("contract-row:D1").assert(SemanticsMatcher.expectValue(AttentionKey, true))
        assertEquals(0, composeRule.onAllNodesWithTag("contract-row:D2").fetchSemanticsNodes().size)
        composeRule.assertShows("Settled: D2 Synthetic answer")
    }

    @Test
    fun aRowTheMarksConcernAreAttention() {
        composeRule.setContractBody(marks = marks(concerned = setOf("F1")))

        composeRule.scrollTo("contract-row:F1")
        composeRule.onNodeWithTag("contract-row:F1").assert(SemanticsMatcher.expectValue(AttentionKey, true))
        composeRule.onNodeWithTag("contract-row:F2").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
    }

    @Test
    fun anUnreadIrreversibleSectionIsAttentionAndSaysToReadItBeforeAccept() {
        composeRule.setContractBody(marks = marks(unread = true))

        composeRule.scrollTo("contract-section:$IRREVERSIBLE")
        composeRule.onNodeWithTag("contract-section:$IRREVERSIBLE").assert(SemanticsMatcher.expectValue(AttentionKey, true))
        composeRule.assertShownIn("contract-section:$IRREVERSIBLE", "read this before Accept")
    }

    @Test
    fun aReadIrreversibleSectionIsNotAttention() {
        composeRule.setContractBody(marks = marks(unread = false))

        composeRule.scrollTo("contract-section:$IRREVERSIBLE")
        composeRule.onNodeWithTag("contract-section:$IRREVERSIBLE").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
        assertEquals(0, composeRule.onAllNodesWithText("read this before Accept").fetchSemanticsNodes().size)
    }

    @Test
    fun onIrreversibleShownIsCalledOnceWhenTheSectionIsComposed() {
        var shown = 0
        composeRule.setContractBody(onIrreversibleShown = { shown++ })

        composeRule.scrollTo("contract-section:$IRREVERSIBLE")
        composeRule.waitForIdle()
        assertEquals(1, shown)
    }

    @Test
    fun aRowControlCallsOnAboutWithItsIdAndText() {
        val abouts = mutableListOf<RowAbout>()
        composeRule.setContractBody(onAbout = { abouts += it })

        listOf(
            "F1" to "Synthetic failure text",
            "R1" to "Synthetic requirement text",
            "P1" to "Synthetic premise text",
        ).forEach { (id, text) ->
            composeRule.scrollTo("contract-talk:$id")
            composeRule.onNodeWithTag("contract-talk:$id").performClick()
            assertEquals(RowAbout(id, text), abouts.last())
        }
        assertEquals(3, abouts.size)
    }

    @Test
    fun anOpenDecisionsControlCallsOnAboutWithItsQuestion() {
        val abouts = mutableListOf<RowAbout>()
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION), onAbout = { abouts += it })

        composeRule.scrollTo("contract-talk:D1")
        composeRule.onNodeWithTag("contract-talk:D1").performClick()
        assertEquals(listOf(RowAbout("D1", "Synthetic decision question")), abouts)
    }

    @Test
    fun additionsKeepAndStrikeCallOnAboutWithTheirLocation() {
        val abouts = mutableListOf<RowAbout>()
        composeRule.setContractBody(onAbout = { abouts += it })

        composeRule.scrollTo("contract-keep:0")
        composeRule.onNodeWithTag("contract-keep:0").performClick()
        composeRule.scrollTo("contract-strike:0")
        composeRule.onNodeWithTag("contract-strike:0").performClick()
        assertEquals(
            listOf(
                RowAbout("Synthetic addition location", "Keep: Synthetic addition what"),
                RowAbout("Synthetic addition location", "Strike: Synthetic addition what"),
            ),
            abouts,
        )
    }

    @Test
    fun withoutOnAboutNoRowHasAControl() {
        composeRule.setContractBody(decisions = listOf(OPEN_DECISION))

        listOf("F1", "R1", "P1", "D1").forEach { id -> composeRule.assertNoControlIn("contract-row:$id", "contract-talk:$id") }
        composeRule.assertNoControlIn("contract-addition:0", "contract-keep:0")
        composeRule.assertNoControlIn("contract-addition:0", "contract-strike:0")
    }

    @Test
    fun aChangedRowAndSectionCarryAChangedInVChip() {
        composeRule.setContractBody(marks = marks(changedRows = setOf("F1"), changedSections = setOf("intent"), version = 2))

        composeRule.scrollTo("contract-row:F1")
        composeRule.onNode(
            hasText("changed in v2") and hasAnyAncestor(hasTestTag("contract-row:F1")),
            useUnmergedTree = true,
        ).assert(SemanticsMatcher.expectValue(ChipToneKey, ChipTone.New))
        composeRule.onNode(
            hasText("changed in v2") and hasAnyAncestor(hasTestTag("contract-section:0")),
            useUnmergedTree = true,
        ).assertIsDisplayed()
    }

    @Test
    fun anUnchangedRowHasNoChangedChip() {
        composeRule.setContractBody(marks = marks(changedRows = setOf("F1"), version = 2))

        composeRule.scrollTo("contract-row:F2")
        composeRule.onNode(
            hasText("changed in v2") and hasAnyAncestor(hasTestTag("contract-row:F2")),
            useUnmergedTree = true,
        ).assertDoesNotExist()
    }

    @Test
    fun aSetAsideContractWithAnOpenDecisionSaysNoneAndDrawsNoAttention() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(
                AtomicReference(
                    contractRecordProto(
                        "set_aside",
                        listOf(contractVersionProto(1, decisions = listOf(openDecisionProto()))),
                        states = listOf(contractStateProto("set_aside", 1, words = "Synthetic set aside words")),
                    ),
                ),
            )
            setScreenAndAwaitRecord(server)

            composeRule.assertShows("Decisions needed: none")
            composeRule.scrollTo("contract-row:D1")
            composeRule.onNodeWithTag("contract-row:D1").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
            composeRule.scrollTo("contract-section:$DECISIONS")
            composeRule.onNodeWithTag("contract-section:$DECISIONS").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
        }
    }

    @Test
    fun anOlderVersionOfAProposedContractSaysNoneAndDrawsNoAttention() {
        FakeOrreryServer().use { server ->
            server.serveContractRecord(
                AtomicReference(
                    contractRecordProto(
                        "proposed",
                        listOf(
                            contractVersionProto(1, decisions = listOf(openDecisionProto())),
                            contractVersionProto(2, decisions = listOf(openDecisionProto())),
                        ),
                    ),
                ),
            )
            setScreenAndAwaitRecord(server)

            composeRule.assertShows("Decisions needed: 1")
            composeRule.onNodeWithTag("contract-versions-toggle").performClick()
            composeRule.scrollTo("contract-section:$DECISIONS")
            composeRule.onNodeWithTag("contract-section:$DECISIONS").assert(SemanticsMatcher.expectValue(AttentionKey, true))

            composeRule.scrollTo("contract-version:1")
            composeRule.onNodeWithTag("contract-version:1").performClick()
            composeRule.awaitExactlyOne(hasTestTag("contract-older"))

            composeRule.assertShows("Decisions needed: none")
            assertEquals(0, composeRule.onAllNodesWithText("Decisions needed: 1").fetchSemanticsNodes().size)
            composeRule.scrollTo("contract-section:$DECISIONS")
            composeRule.onNodeWithTag("contract-section:$DECISIONS").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
            composeRule.scrollTo("contract-row:D1")
            composeRule.onNodeWithTag("contract-row:D1").assert(SemanticsMatcher.keyNotDefined(AttentionKey))
        }
    }

    /** Draws the contract screen for [RECORD_ID] from [server] and waits until its record is drawn. */
    private fun setScreenAndAwaitRecord(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                ContractScreen(client = client, epicId = RECORD_ID, onBack = {}, onHome = {})
            }
        }
        composeRule.awaitExactlyOne(hasTestTag("contract-versions-toggle"))
    }

    private fun marks(
        concerned: Set<String> = emptySet(),
        unread: Boolean = false,
        changedRows: Set<String> = emptySet(),
        changedSections: Set<String> = emptySet(),
        version: Long = 1,
    ) = DocMarks(
        concerned = concerned,
        unread = unread,
        changedRows = changedRows,
        changedSections = changedSections,
        version = version,
    )
}
