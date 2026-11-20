package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.QueuedItem
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.WaitingItem
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PullQueuedRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PullQueuedResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WithdrawRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WithdrawResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CountDownLatch
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "synthetic-session-1"
private const val PANE_ID = "%7"
private const val PROMPT_ID = "synthetic-prompt-1"
private const val TYPED = "text the owner typed"
private const val QUEUED_TEXT = "queued text"
private const val DISCARDED_TEXT = "discarded text"
private const val CANCELED_TEXT = "canceled text"
private const val LIVE_TEXT = "live text"
private const val PULLED_TEXT = "pulled text"
private const val WAITING_TEXT = "waiting text"
private const val WITHDRAW_PATH = "/orrery.v1.Control/Withdraw"
private const val INTERRUPT_PATH = "/orrery.v1.Control/Interrupt"
private const val SAY_PATH = "/orrery.v1.Control/Say"
private const val PULL_PATH = "/orrery.v1.Panes/PullQueued"
private const val REFUSAL_REASON = "synthetic refusal"
private const val TAKE_BACK_TAG = "dock-take-back"
private const val PUT_BACK_TAG = "dock-put-back"
private const val TAKE_ALL_BACK_TAG = "dock-take-all-back"
private const val SEND_NOW_TAG = "dock-send-now"
private const val INTERRUPT_TAG = "dock-interrupt"
private const val ROW_TAG = "dock-tray-row"
private val ALREADY_TOOK = DockLines.alreadyTook
private val TAKE_BACK_FAILED = DockLines.takeBackFailed
private val TAKE_ALL_BACK_FAILED = DockLines.takeAllBackFailed

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockQueueTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val server = FakeOrreryServer()
    private val interruptReply = CountDownLatch(1)

    @After
    fun tearDown() {
        interruptReply.countDown()
        server.close()
    }

    @Test
    fun takeBackCallsWithdrawWithThePromptIdAndPutsItsTextAfterTheDraft() {
        server.respond(WITHDRAW_PATH, WithdrawResponse.newBuilder().addTexts(QUEUED_TEXT).build())
        val draft = typedDraft()
        setDock(draft = draft, queued = listOf(queued(PROMPT_ID, QUEUED_TEXT)))

        composeRule.onNodeWithTag(TAKE_BACK_TAG).performClick()
        composeRule.awaitUntil("the withdrawn text is after the draft") { draft.text == "$TYPED\n\n$QUEUED_TEXT" }

        assertEquals(1, server.requests.size)
        val request = WithdrawRequest.parseFrom(server.requests.single().body)
        assertEquals(SESSION_ID, request.id)
        assertEquals(PROMPT_ID, request.promptId)
    }

    @Test
    fun takeBackWithNoTextSaysClaudeAlreadyTookIt() {
        server.respond(WITHDRAW_PATH, WithdrawResponse.getDefaultInstance())
        val draft = DockDraft()
        setDock(draft = draft, queued = listOf(queued(PROMPT_ID, QUEUED_TEXT)))

        composeRule.onNodeWithTag(TAKE_BACK_TAG).performClick()
        composeRule.awaitUntil("the already-took note shows") {
            composeRule.onAllNodesWithText(ALREADY_TOOK).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals("", draft.text)
        composeRule.onNodeWithText(ALREADY_TOOK).assertIsDisplayed()
    }

    @Test
    fun takeBackFailureSaysCouldNotTakeItBack() {
        server.fail(WITHDRAW_PATH, Code.INTERNAL_ERROR, REFUSAL_REASON)
        val draft = DockDraft()
        setDock(draft = draft, queued = listOf(queued(PROMPT_ID, QUEUED_TEXT)))

        composeRule.onNodeWithTag(TAKE_BACK_TAG).performClick()
        composeRule.awaitUntil("the take-back failure shows") {
            composeRule.onAllNodesWithText(TAKE_BACK_FAILED).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals("", draft.text)
    }

    @Test
    fun putBackPutsTheDiscardedMessageInTheBoxAndHidesTheRow() {
        val draft = DockDraft()
        setDock(draft = draft, queued = listOf(queued(PROMPT_ID, DISCARDED_TEXT, state = "discarded")))

        composeRule.onNodeWithTag(PUT_BACK_TAG).performClick()
        composeRule.awaitUntil("the discarded text is in the box") { draft.text == DISCARDED_TEXT }
        composeRule.waitForIdle()

        composeRule.onAllNodesWithTag(ROW_TAG).assertCountEquals(0)
        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun canceledMessageDrawsNothing() {
        setDock(
            queued = listOf(
                queued("prompt-live", LIVE_TEXT),
                queued("prompt-canceled", CANCELED_TEXT, state = "canceled"),
            ),
        )

        composeRule.onAllNodesWithTag(ROW_TAG).assertCountEquals(1)
        composeRule.onAllNodesWithText(CANCELED_TEXT).assertCountEquals(0)
    }

    @Test
    fun takeAllBackCallsPullQueuedAndPutsItsTextInTheBox() {
        server.respond(PULL_PATH, PullQueuedResponse.newBuilder().setText(PULLED_TEXT).build())
        val draft = DockDraft()
        setDock(draft = draft, facts = paneFacts(), sessionId = null, paneId = PANE_ID, waiting = listOf(waiting()))

        composeRule.onNodeWithTag(TAKE_ALL_BACK_TAG).performClick()
        composeRule.awaitUntil("the pulled text is in the box") { draft.text == PULLED_TEXT }

        assertEquals(1, server.requests.size)
        assertEquals(PANE_ID, PullQueuedRequest.parseFrom(server.requests.single().body).pane)
    }

    @Test
    fun takeAllBackFailureSaysCouldNotTakeThemBack() {
        server.fail(PULL_PATH, Code.INTERNAL_ERROR, REFUSAL_REASON)
        val draft = DockDraft()
        setDock(draft = draft, facts = paneFacts(), sessionId = null, paneId = PANE_ID, waiting = listOf(waiting()))

        composeRule.onNodeWithTag(TAKE_ALL_BACK_TAG).performClick()
        composeRule.awaitUntil("the take-all-back failure shows") {
            composeRule.onAllNodesWithText(TAKE_ALL_BACK_FAILED).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals("", draft.text)
    }

    @Test
    fun sendNowCallsInterruptThenSaysEachTextInOrder() {
        server.respond(INTERRUPT_PATH, InterruptResponse.newBuilder().addAllTexts(LIST_OF_THREE).build())
        server.respond(SAY_PATH, SayResponse.getDefaultInstance())
        setDock(facts = workingSession(), queued = listOf(queued(PROMPT_ID, LIVE_TEXT)))

        composeRule.onNodeWithTag(SEND_NOW_TAG).performClick()
        composeRule.awaitUntil("all three texts are said again") { server.requests.size == 4 }

        val requests = server.requests
        assertEquals(listOf(INTERRUPT_PATH, SAY_PATH, SAY_PATH, SAY_PATH), requests.map { it.rawPath })
        assertEquals(SESSION_ID, InterruptRequest.parseFrom(requests[0].body).id)
        val said = requests.drop(1).map { SayRequest.parseFrom(it.body).text }
        assertEquals(LIST_OF_THREE, said)
    }

    @Test
    fun sendNowIsNotOfferedWhileItsInterruptIsInFlightAndSaysEachTextOnce() {
        server.route(INTERRUPT_PATH) {
            interruptReply.await()
            Reply.Message(InterruptResponse.newBuilder().addAllTexts(LIST_OF_THREE).build(), 0L)
        }
        server.respond(SAY_PATH, SayResponse.getDefaultInstance())
        setDock(facts = workingSession(), queued = listOf(queued(PROMPT_ID, LIVE_TEXT)))

        composeRule.onNodeWithTag(SEND_NOW_TAG).performClick()
        composeRule.awaitUntil("the interrupt is in flight") { server.requests.size == 1 }
        composeRule.waitForIdle()
        composeRule.onAllNodesWithTag(SEND_NOW_TAG).assertCountEquals(0)

        interruptReply.countDown()
        composeRule.awaitUntil("all three texts are said again") { server.requests.size == 4 }
        composeRule.awaitUntil("Send now is offered again") {
            composeRule.onAllNodesWithTag(SEND_NOW_TAG).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(listOf(INTERRUPT_PATH, SAY_PATH, SAY_PATH, SAY_PATH), server.requests.map { it.rawPath })
        assertEquals(LIST_OF_THREE, server.requests.drop(1).map { SayRequest.parseFrom(it.body).text })
    }

    @Test
    fun refusedSecondSayLeavesTheSecondAndThirdInTheBoxInOrder() {
        server.respond(INTERRUPT_PATH, InterruptResponse.newBuilder().addAllTexts(LIST_OF_THREE).build())
        server.route(SAY_PATH) { request ->
            if (SayRequest.parseFrom(request.body).text == "second") {
                Reply.Failure(Code.FAILED_PRECONDITION, REFUSAL_REASON, 0L)
            } else {
                Reply.Message(SayResponse.getDefaultInstance(), 0L)
            }
        }
        val draft = DockDraft()
        setDock(draft = draft, facts = workingSession(), queued = listOf(queued(PROMPT_ID, LIVE_TEXT)))

        composeRule.onNodeWithTag(SEND_NOW_TAG).performClick()
        composeRule.awaitUntil("the held texts are in the box") { draft.text == "second\n\nthird" }
        composeRule.waitForIdle()

        assertEquals(listOf(INTERRUPT_PATH, SAY_PATH, SAY_PATH), server.requests.map { it.rawPath })
        assertEquals(listOf("first", "second"), server.requests.drop(1).map { SayRequest.parseFrom(it.body).text })
        composeRule.onNodeWithText(DockLines.sendNowHeld(2)).assertIsDisplayed()
    }

    @Test
    fun interruptTextsLandInTheBoxInOrder() {
        server.respond(INTERRUPT_PATH, InterruptResponse.newBuilder().addTexts("alpha").addTexts("beta").build())
        val draft = DockDraft()
        setDock(draft = draft, facts = workingSession())

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.awaitUntil("the interrupted texts are in the box") { draft.text == "alpha\n\nbeta" }
        composeRule.waitForIdle()

        composeRule.onNodeWithText(DockLines.backInBox(2)).assertIsDisplayed()
    }

    @Test
    fun aFailedInterruptSaysSoAndKeepsTheDraft() {
        server.fail(INTERRUPT_PATH, Code.INTERNAL_ERROR, REFUSAL_REASON)
        val draft = typedDraft()
        setDock(draft = draft, facts = workingSession())

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.awaitUntil("the interrupt failure shows") {
            composeRule.onAllNodesWithText("could not interrupt").fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(TYPED, draft.text)
    }

    @Test
    fun aRefusedSendNowInterruptSaysNotSentAndSaysNothing() {
        server.fail(INTERRUPT_PATH, Code.FAILED_PRECONDITION, REFUSAL_REASON)
        val draft = typedDraft()
        setDock(draft = draft, facts = workingSession(), queued = listOf(queued(PROMPT_ID, QUEUED_TEXT)))

        composeRule.onNodeWithTag(SEND_NOW_TAG).performClick()
        composeRule.awaitUntil("the refusal shows") {
            composeRule.onAllNodesWithText(DockLines.sendRefused).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(listOf(INTERRUPT_PATH), server.requests.map { it.rawPath })
        assertEquals(TYPED, draft.text)
    }

    private fun typedDraft(): DockDraft = DockDraft().also { it.append(listOf(TYPED)) }

    private fun queued(promptId: String, text: String, state: String = "queued", waits: String = "turn") =
        QueuedItem(promptId = promptId, text = text, state = state, waits = waits)

    private fun waiting() = WaitingItem(text = WAITING_TEXT, time = 1L, canceled = false)

    private fun workingSession() = DockFacts(kind = DockKind.SESSION, name = SESSION_NAME, state = "working")

    private fun paneFacts() = DockFacts(kind = DockKind.PANE, name = PANE_NAME, state = "working")

    private fun setDock(
        draft: DockDraft = DockDraft(),
        facts: DockFacts = workingSession(),
        sessionId: String? = SESSION_ID,
        paneId: String? = null,
        queued: List<QueuedItem> = emptyList(),
        waiting: List<WaitingItem> = emptyList(),
    ) {
        composeRule.setContent {
            MaterialTheme {
                Dock(
                    facts = facts,
                    nowSeconds = NOW,
                    client = OrreryClient(server.baseUrl),
                    draft = draft,
                    send = {},
                    sessionId = sessionId,
                    paneId = paneId,
                    queued = queued,
                    waiting = waiting,
                    newPromptId = { NEW_PROMPT_ID },
                )
            }
        }
        composeRule.waitForIdle()
    }

    private companion object {
        const val SESSION_NAME = "synthetic session"
        const val PANE_NAME = "synthetic pane"
        const val NOW = 1_000_000L
        const val NEW_PROMPT_ID = "synthetic-new-prompt"
        val LIST_OF_THREE = listOf("first", "second", "third")
    }
}
