package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineText
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest as OpenRequestProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PendingEvent
import io.github.joshsymonds.orrery.gen.orrery.v1.PullQueuedRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PullQueuedResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.QueuedMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.WithdrawRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WithdrawResponse
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.IOException
import java.net.SocketTimeoutException
import java.nio.ByteBuffer

class OrreryClientSessionStreamTest {
    private val exampleSession = HeadlessSessionProto.newBuilder()
        .setId(SESSION_ID)
        .setDir("/home/user/project")
        .setLabel("project · 0123abcd")
        .setState("waiting")
        .setChanged(4102444800L)
        .setSeen(4102444900L)
        .setSummary("Example summary.")
        .setQuestion("Example question?")
        .setNewestReply(4102444950L)
        .setPutOff(true)
        .build()

    private val expectedPane = Pane(
        id = SESSION_ID,
        host = "",
        session = "",
        window = 0,
        pane = 0,
        cwd = "/home/user/project",
        command = "",
        state = "waiting",
        summary = "Example summary.",
        label = "project · 0123abcd",
        changed = 4102444800L,
        seen = 4102444900L,
        question = "Example question?",
        questionHash = "",
        newestReply = 4102444950L,
        headless = true,
        orreryId = SESSION_ID,
        putOff = true,
    )

    private val exampleRequest = OpenRequestProto.newBuilder()
        .setId("example-request")
        .setKind("question")
        .setQuestion("Example question?")
        .addOptions("Yes")
        .addOptions("No")
        .setPutOff(true)
        .build()

    private val expectedRequest = SessionRequest(
        id = "example-request",
        kind = "question",
        question = "Example question?",
        options = listOf("Yes", "No"),
        putOff = true,
    )

    private val firstDeltaProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(10L)
        .setReset(true)
        .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(1700000000L).setText("Example prompt."))
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("compaction")
                .setTime(1700000050L)
                .setText("Example summary.")
                .setTrigger("auto")
                .setTokens(611000L),
        )
        .addItems(ConversationItemProto.newBuilder().setKind("interrupted").setTime(1700000400L))
        .setState(
            ConversationStateProto.newBuilder()
                .setOrigin("Example opening prompt.")
                .setGoal("Example goal.")
                .addWaiting(WaitingMessage.newBuilder().setText("Example queued.").setTime(1700000300L).setCanceled(false))
                .addWaiting(WaitingMessage.newBuilder().setText("Example cancelled.").setTime(1700000310L).setCanceled(true))
                .setContext(ConversationContextProto.newBuilder().setTokens(1200L).setWindow(200000L))
                .setSession(exampleSession)
                .setRequest(exampleRequest)
                .setInterrupting(true)
                .setInterruptingSince(1700000400L)
                .setTurnSince(1700000390L)
                .addPending(PendingEvent.newBuilder().setId("event-1").setKind("step_end").setWaits("speak"))
                .addPending(PendingEvent.newBuilder().setId("event-2").setKind("question").setWaits("request")),
        )
        .addQueued(
            QueuedMessage.newBuilder()
                .setPromptId("prompt-1")
                .setText("Example queued prompt.")
                .setState("queued")
                .setWaits("turn"),
        )
        .addQueued(
            QueuedMessage.newBuilder()
                .setPromptId("prompt-2")
                .setText("Example canceled prompt.")
                .setState("canceled")
                .setWaits("start"),
        )
        .addUnfinished(
            EngineText.newBuilder()
                .setMessageId("msg_example")
                .setIndex(0)
                .setParentToolUseId("")
                .setKind("text")
                .setText("Example partial reply."),
        )
        .addUnfinished(
            EngineText.newBuilder()
                .setMessageId("msg_example")
                .setIndex(1)
                .setParentToolUseId("example-tool-use")
                .setKind("thinking")
                .setText("Example partial thought."),
        )
        .build()

    private val expectedFirstDelta = ConversationDelta(
        transcript = SESSION_ID,
        seq = 10L,
        reset = true,
        items = listOf(
            ConversationItem(kind = "prompt", time = 1700000000L, text = "Example prompt.", tool = "", input = "", id = "", error = false),
            ConversationItem(
                kind = "compaction",
                time = 1700000050L,
                text = "Example summary.",
                tool = "",
                input = "",
                id = "",
                error = false,
                trigger = "auto",
                tokens = 611000L,
            ),
            ConversationItem(
                kind = "interrupted",
                time = 1700000400L,
                text = "",
                tool = "",
                input = "",
                id = "",
                error = false,
            ),
        ),
        state = ConversationState(
            origin = "Example opening prompt.",
            goal = "Example goal.",
            waiting = listOf(
                WaitingItem(text = "Example queued.", time = 1700000300L, canceled = false),
                WaitingItem(text = "Example cancelled.", time = 1700000310L, canceled = true),
            ),
            context = ConversationContext(tokens = 1200L, window = 200000L),
            pane = expectedPane,
            request = expectedRequest,
            interrupting = true,
            interruptingSince = 1700000400L,
            turnSince = 1700000390L,
            pending = listOf(
                PendingItem(id = "event-1", kind = "step_end", waits = "speak"),
                PendingItem(id = "event-2", kind = "question", waits = "request"),
            ),
        ),
        queued = listOf(
            QueuedItem(promptId = "prompt-1", text = "Example queued prompt.", state = "queued", waits = "turn"),
            QueuedItem(promptId = "prompt-2", text = "Example canceled prompt.", state = "canceled", waits = "start"),
        ),
        unfinished = listOf(
            UnfinishedText(messageId = "msg_example", index = 0, parentToolUseId = "", kind = "text", text = "Example partial reply."),
            UnfinishedText(
                messageId = "msg_example",
                index = 1,
                parentToolUseId = "example-tool-use",
                kind = "thinking",
                text = "Example partial thought.",
            ),
        ),
    )

    private val secondDeltaProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(11L)
        .addItems(ConversationItemProto.newBuilder().setKind("reply").setTime(1700000100L).setText("Example reply."))
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("result")
                .setTime(1700000210L)
                .setText("Example failure.")
                .setId("example-tool-use")
                .setError(true),
        )
        .addUnfinished(
            EngineText.newBuilder()
                .setMessageId("msg_example_two")
                .setIndex(0)
                .setParentToolUseId("")
                .setKind("text")
                .setText("Example later partial."),
        )
        .build()

    private val expectedSecondDelta = ConversationDelta(
        transcript = SESSION_ID,
        seq = 11L,
        reset = false,
        items = listOf(
            ConversationItem(kind = "reply", time = 1700000100L, text = "Example reply.", tool = "", input = "", id = "", error = false),
            ConversationItem(
                kind = "result",
                time = 1700000210L,
                text = "Example failure.",
                tool = "",
                input = "",
                id = "example-tool-use",
                error = true,
            ),
        ),
        state = null,
        unfinished = listOf(
            UnfinishedText(messageId = "msg_example_two", index = 0, parentToolUseId = "", kind = "text", text = "Example later partial."),
        ),
    )

    private val promptProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(14L)
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("prompt")
                .setTime(1700000500L)
                .setText("Example brief.")
                .setSender("brief"),
        )
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("prompt")
                .setTime(1700000600L)
                .setText("Example send-back.")
                .setSender("engine")
                .setRound(2),
        )
        .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(1700000700L).setText("Example owner prompt."))
        .build()

    private val expectedPromptDelta = ConversationDelta(
        transcript = SESSION_ID,
        seq = 14L,
        reset = false,
        items = listOf(
            ConversationItem(
                kind = "prompt",
                time = 1700000500L,
                text = "Example brief.",
                tool = "",
                input = "",
                id = "",
                error = false,
                sender = "brief",
                round = 0,
            ),
            ConversationItem(
                kind = "prompt",
                time = 1700000600L,
                text = "Example send-back.",
                tool = "",
                input = "",
                id = "",
                error = false,
                sender = "engine",
                round = 2,
            ),
            ConversationItem(
                kind = "prompt",
                time = 1700000700L,
                text = "Example owner prompt.",
                tool = "",
                input = "",
                id = "",
                error = false,
            ),
        ),
        state = null,
    )

    private val thirdDeltaProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(12L)
        .setState(ConversationStateProto.newBuilder().setOrigin("Example opening prompt."))
        .build()

    private val expectedThirdDelta = ConversationDelta(
        transcript = SESSION_ID,
        seq = 12L,
        reset = false,
        items = emptyList(),
        state = ConversationState(
            origin = "Example opening prompt.",
            goal = "",
            waiting = emptyList(),
            context = null,
            pane = null,
            request = null,
        ),
    )

    private val keepaliveProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(13L)
        .build()

    private val expectedKeepalive = ConversationDelta(
        transcript = SESSION_ID,
        seq = 13L,
        reset = false,
        items = emptyList(),
        state = null,
    )

    @Test
    fun watchSessionSendsTheProcedureHeadersAndTheRequestFields() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(emptyList(), StreamEnd.Clean) }

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 42L) { }
            }

            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals(SESSION_WATCH_CONVERSATION, request.rawPath)
            assertEquals("application/connect+proto", request.headers["content-type"])
            assertEquals("orrery-android", request.headers["x-orrery-client"])

            val sent = SessionsWatchConversationRequest.parseFrom(decodeEnvelope(request.body))
            assertEquals(SESSION_ID, sent.id)
            assertEquals(50, sent.limit)
            assertEquals(42L, sent.after)
        }
    }

    @Test
    fun watchSessionDeliversEveryDeltaInOrderWithEveryFieldMapped() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) {
                Reply.Stream(listOf(firstDeltaProto, secondDeltaProto, thirdDeltaProto, keepaliveProto), StreamEnd.Clean)
            }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.add(it) }
            }

            assertEquals(
                listOf(expectedFirstDelta, expectedSecondDelta, expectedThirdDelta, expectedKeepalive),
                received,
            )
        }
    }

    @Test
    fun watchSessionKeepsEachPromptItemsSenderAndRound() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(listOf(promptProto), StreamEnd.Clean) }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.add(it) }
            }

            assertEquals(listOf(expectedPromptDelta), received)
        }
    }

    @Test
    fun watchSessionCleanEndWithNoMessagesReturnsWithoutDeltas() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(emptyList(), StreamEnd.Clean) }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.add(it) }
            }

            assertEquals(emptyList<ConversationDelta>(), received)
        }
    }

    @Test
    fun watchSessionErrorEndThrowsCallExceptionAfterTheDeltasBeforeIt() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) {
                Reply.Stream(listOf(firstDeltaProto), StreamEnd.Error(Code.NOT_FOUND, "example session is gone"))
            }
            val received = mutableListOf<ConversationDelta>()

            val error = assertThrows(OrreryCallException::class.java) {
                runBlocking {
                    OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.add(it) }
                }
            }

            assertEquals(Code.NOT_FOUND, error.code)
            assertEquals(listOf(expectedFirstDelta), received)
        }
    }

    @Test
    fun watchSessionSilenceLongerThanTheStreamReadTimeoutThrowsSocketTimeoutException() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(listOf(firstDeltaProto), StreamEnd.Silent) }
            val client = OrreryClient(server.baseUrl, streamReadTimeoutMillis = 300)
            val received = mutableListOf<ConversationDelta>()

            assertThrows(SocketTimeoutException::class.java) {
                runBlocking {
                    client.watchSession(SESSION_ID, 0L) { received.add(it) }
                }
            }

            assertEquals(listOf(expectedFirstDelta), received)
        }
    }

    @Test
    fun cancellingTheCallerClosesTheSessionStreamWithinTwoSeconds() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(emptyList(), StreamEnd.Held) }
            val client = OrreryClient(server.baseUrl)

            runBlocking {
                val firstDelta = CompletableDeferred<Unit>()
                val job = launch(Dispatchers.IO) {
                    client.watchSession(SESSION_ID, 0L) { firstDelta.complete(Unit) }
                }
                Quiescence.awaitUntil("the first delta") { firstDelta.isCompleted }

                job.cancelAndJoin()
            }

            server.awaitStreamClosedByClient()
        }
    }

    @Test
    fun watchSessionRefusedConnectionThrowsIOExceptionThatIsNotCallException() {
        val server = FakeOrreryServer()
        val baseUrl = server.baseUrl
        server.close()

        val error = assertThrows(IOException::class.java) {
            runBlocking {
                OrreryClient(baseUrl).watchSession(SESSION_ID, 0L) { }
            }
        }

        assertFalse(error is OrreryCallException)
    }

    @Test
    fun interruptReturnsTheTextsTheServerAnswersInSendOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                CONTROL_INTERRUPT,
                InterruptResponse.newBuilder().addTexts("Example first text.").addTexts("Example second text.").build(),
            )

            val texts = OrreryClient(server.baseUrl).interrupt(SESSION_ID)

            assertEquals(listOf("Example first text.", "Example second text."), texts)
        }
    }

    @Test
    fun withdrawSendsTheSessionAndPromptIdAndReturnsTheTexts() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_WITHDRAW, WithdrawResponse.newBuilder().addTexts("Example withdrawn text.").build())

            val texts = OrreryClient(server.baseUrl).withdraw(SESSION_ID, "prompt-1")

            val sent = WithdrawRequest.parseFrom(server.requests.single().body)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("prompt-1", sent.promptId)
            assertEquals(listOf("Example withdrawn text."), texts)
        }
    }

    @Test
    fun pullQueuedSendsThePaneAndReturnsTheText() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_PULL_QUEUED, PullQueuedResponse.newBuilder().setText("Example pulled text.").build())

            val text = OrreryClient(server.baseUrl).pullQueued("pane-1")

            assertEquals("pane-1", PullQueuedRequest.parseFrom(server.requests.single().body).pane)
            assertEquals("Example pulled text.", text)
        }
    }

    /** The payload of one Connect envelope: a zero flag byte, the big-endian length, then the message. */
    private fun decodeEnvelope(body: ByteArray): ByteArray {
        assertEquals(0, body[0].toInt())
        val length = ByteBuffer.wrap(body, 1, 4).int
        assertEquals(5 + length, body.size)
        return body.copyOfRange(5, body.size)
    }

    private companion object {
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val SESSION_WATCH_CONVERSATION = "/orrery.v1.Sessions/WatchConversation"
        const val CONTROL_INTERRUPT = "/orrery.v1.Control/Interrupt"
        const val CONTROL_WITHDRAW = "/orrery.v1.Control/Withdraw"
        const val PANES_PULL_QUEUED = "/orrery.v1.Panes/PullQueued"
    }
}
