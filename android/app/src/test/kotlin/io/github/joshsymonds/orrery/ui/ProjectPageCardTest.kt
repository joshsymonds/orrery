package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val NOW = 1_000_000L

private fun epicRow(
    outcome: String = "accepted",
    live: Boolean = true,
    parked: String = "",
) = EpicRow(
    id = "e1",
    repository = "lighthouse",
    slug = "rota-export",
    outcome = outcome,
    changed = NOW - 60,
    live = live,
    runningSteps = emptyList(),
    endedSteps = 0,
    problem = "",
    phaseAt = NOW - 2940,
    parked = parked,
)

private fun taskRow() = EpicTaskRow(effort = 1, slug = "t1", subject = "Build the feed", status = "")

private fun runningStep() = StepRow(
    id = "s1",
    task = "t1",
    agent = "",
    end = "",
    status = "",
    held = false,
    started = NOW - 30,
    ended = 0L,
    doing = "writing the feed",
    checks = emptyList(),
    reason = "",
)

private fun contractRow(care: String = "severe", openDecisions: Long = 1L) = ContractRow(
    id = "lighthouse/tide-alerts",
    repository = "lighthouse",
    title = "The harbour office hears within a minute",
    care = care,
    state = "proposed",
    version = 2L,
    openDecisions = openDecisions,
    conversation = "c1",
    created = NOW - 900,
    changed = NOW - 900,
)

private val question = EpicQuestionRow(id = "q1", text = "Which fix should I make?", turn = 1L, asked = NOW - 60)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ProjectPageCardTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun aContractWaitingReadsItsCareItsTitleItsDecisionsAndItsVersion() {
        val card = workCardOf(ContractNode(contractRow()), NOW)

        assertEquals("contract:lighthouse/tide-alerts", card.key)
        assertEquals("tide-alerts", card.slug)
        assertEquals(null, card.gauge)
        assertEquals(CardTone.ATTENTION, card.tone)
        assertEquals("severe care", card.state)
        assertEquals(listOf(TrackState.NOW, TrackState.AHEAD, TrackState.AHEAD, TrackState.AHEAD, TrackState.AHEAD), card.track)
        assertEquals("The harbour office hears within a minute", card.now)
        assertEquals("1 decision left to answer", card.asks)
        assertEquals("Read and decide", card.act)
        assertEquals("v2", card.count)
    }

    @Test
    fun aContractWithNoDecisionsOpenWaitsForYouToAcceptAndReadsProposedWithNoCare() {
        val card = workCardOf(ContractNode(contractRow(care = "", openDecisions = 0L)), NOW)

        assertEquals("proposed", card.state)
        assertEquals("waits for you to accept", card.asks)
    }

    @Test
    fun anEpicThatAsksReadsItsQuestionAnswerItsNowSentenceAndItsTrack() {
        val epic = epicRow()
        val detail = EpicDetail(
            epic = epic,
            tasks = listOf(taskRow()),
            slices = emptyList(),
            steps = listOf(runningStep()),
            questions = listOf(question),
        )
        val card = workCardOf(EpicNode(epic, detail, contract = null), NOW)

        assertEquals("epic:e1", card.key)
        assertEquals("rota-export", card.slug)
        assertEquals(CardTone.ATTENTION, card.tone)
        assertEquals("building · 49 min", card.state)
        assertEquals(listOf(TrackState.DONE, TrackState.DONE, TrackState.NOW, TrackState.AHEAD, TrackState.AHEAD), card.track)
        assertEquals("t1 · implementing · writing the feed", card.now)
        assertEquals("Which fix should I make?", card.asks)
        assertEquals("Answer", card.act)
        assertEquals("0 of 1 tasks done", card.count)
    }

    @Test
    fun anEpicWithAnotherNeedReadsItsBlockerAndDecide() {
        val epic = epicRow(parked = "held for a decision")
        val detail = EpicDetail(
            epic = epic,
            tasks = listOf(taskRow()),
            slices = emptyList(),
            steps = emptyList(),
            parked = "held for a decision",
        )
        val card = workCardOf(EpicNode(epic, detail, contract = null), NOW)

        assertEquals(CardTone.ATTENTION, card.tone)
        assertEquals("parked · 49 min", card.state)
        assertEquals("held for a decision", card.asks)
        assertEquals("Decide", card.act)
    }

    @Test
    fun anEpicBuildingIsLiveAndAsksNothing() {
        val epic = epicRow()
        val detail = EpicDetail(epic = epic, tasks = listOf(taskRow()), slices = emptyList(), steps = listOf(runningStep()))
        val card = workCardOf(EpicNode(epic, detail, contract = null), NOW)

        assertEquals(CardTone.LIVE, card.tone)
        assertEquals("", card.asks)
        assertEquals("", card.act)
    }

    @Test
    fun anAcceptedContractWithNoListedEpicHasNoToneAndAsksNothing() {
        val card = workCardOf(ContractNode(contractRow(care = "").copy(state = "accepted")), NOW)

        assertEquals(CardTone.NONE, card.tone)
        assertEquals("accepted", card.state)
        assertEquals("", card.asks)
        assertEquals("", card.act)
    }

    @Test
    fun anEpicWithNoDetailHasItsContractDoneAndNoPartNow() {
        val card = workCardOf(EpicNode(epicRow(), detail = null, contract = null), NOW)

        assertEquals(listOf(TrackState.DONE, TrackState.AHEAD, TrackState.AHEAD, TrackState.AHEAD, TrackState.AHEAD), card.track)
    }

    @Test
    fun anArchivedLiveEpicHasNoToneAndAsksNothing() {
        val epic = epicRow().copy(archived = true)
        val detail = EpicDetail(epic = epic, tasks = listOf(taskRow()), slices = emptyList(), steps = emptyList(), questions = listOf(question))
        val card = workCardOf(EpicNode(epic, detail, contract = null), NOW)

        assertEquals(CardTone.NONE, card.tone)
        assertEquals("", card.asks)
        assertEquals("", card.act)
    }

    @Test
    fun anEndedEpicHasNoToneAndAsksNothing() {
        val epic = epicRow(outcome = "released", live = false)
        val detail = EpicDetail(epic = epic, tasks = listOf(taskRow()), slices = emptyList(), steps = emptyList())
        val card = workCardOf(EpicNode(epic, detail, contract = null), NOW)

        assertEquals(CardTone.NONE, card.tone)
        assertEquals("shipped · 49 min ago", card.state)
        assertEquals(listOf(TrackState.DONE, TrackState.DONE, TrackState.DONE, TrackState.DONE, TrackState.NOW), card.track)
        assertEquals("", card.asks)
        assertEquals("", card.act)
    }

    @Test
    fun aWorkCardDrawsItsQuestionAndATapOnItsCardActAndOpenEachCallsOnOpenWithItsKey() {
        val epic = epicRow()
        val detail = EpicDetail(
            epic = epic,
            tasks = listOf(taskRow()),
            slices = emptyList(),
            steps = listOf(runningStep()),
            questions = listOf(question),
        )
        val card = workCardOf(EpicNode(epic, detail, contract = null), NOW)
        val opened = mutableListOf<String>()
        composeRule.setContent { WorkCard(card = card, onOpen = { opened += it }) }

        composeRule.onNodeWithText("Which fix should I make?", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("card:epic:e1").fetchSemanticsNode().config[SemanticsActions.OnClick].action?.invoke()
        composeRule.onNodeWithTag("card-act:epic:e1", useUnmergedTree = true).performClick()
        composeRule.onNodeWithTag("card-open:epic:e1", useUnmergedTree = true).performClick()

        assertEquals(listOf("epic:e1", "epic:e1", "epic:e1"), opened)
    }
}
