package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AskResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UnregisterResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerRow
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UnregisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.IOException
import java.net.SocketTimeoutException

class OrreryClientTest {
    private val panesProto = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId("%3")
                .setHost("example-host")
                .setSession("example-session")
                .setWindow(1)
                .setPane(2)
                .setCwd("/home/user/project")
                .setCommand("claude")
                .setState("waiting")
                .setSummary("Example summary.")
                .setLabel("example-label")
                .setChanged(4102444800L)
                .setSeen(4102444900L)
                .setQuestion("Example question?")
                .setQuestionHash("abc123")
                .setNewestReply(4102444950L),
        )
        .addPanes(
            PaneProto.newBuilder()
                .setId("%4")
                .setHost("example-host")
                .setSession("other-session")
                .setWindow(0)
                .setPane(0)
                .setCwd("/home/user/other")
                .setCommand("zsh"),
        )
        .build()

    private fun paneWithTool(tool: String): PanesListResponse =
        PanesListResponse.newBuilder()
            .addPanes(
                PaneProto.newBuilder()
                    .setId("%3")
                    .setHost("example-host")
                    .setSession("example-session")
                    .setWindow(1)
                    .setPane(2)
                    .setCwd("/home/user/project")
                    .setCommand("claude")
                    .setState("working")
                    .setTool(tool),
            )
            .build()

    private fun assertConnectRequest(procedure: String, request: RecordedRequest) {
        assertEquals("POST", request.method)
        assertEquals(procedure, request.rawPath)
        assertEquals("application/proto", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    /**
     * Runs [call] after a read has left a keep-alive connection in the pool, so the write travels on
     * that pooled connection, the one a retry would resend on. The server reads the write and closes
     * the connection unanswered.
     */
    private fun assertDroppedWriteIsNoAnswerAndSentOnce(procedure: String, call: (OrreryClient) -> Unit) {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            server.drop(procedure)
            val client = OrreryClient(server.baseUrl)
            client.panes()

            assertThrows(NoAnswerException::class.java) {
                call(client)
            }

            assertEquals(1, server.requests.count { it.rawPath == procedure })
        }
    }

    @Test
    fun panesParsesEveryField() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesProto)

            val panes = OrreryClient(server.baseUrl).panes()

            assertEquals(
                listOf(
                    Pane(
                        id = "%3",
                        host = "example-host",
                        session = "example-session",
                        window = 1,
                        pane = 2,
                        cwd = "/home/user/project",
                        command = "claude",
                        state = "waiting",
                        summary = "Example summary.",
                        label = "example-label",
                        changed = 4102444800L,
                        seen = 4102444900L,
                        question = "Example question?",
                        questionHash = "abc123",
                        newestReply = 4102444950L,
                    ),
                    Pane(
                        id = "%4",
                        host = "example-host",
                        session = "other-session",
                        window = 0,
                        pane = 0,
                        cwd = "/home/user/other",
                        command = "zsh",
                        state = "",
                        summary = "",
                        label = "",
                        changed = 0L,
                        seen = 0L,
                        question = "",
                        questionHash = "",
                        newestReply = 0L,
                    ),
                ),
                panes,
            )
            assertConnectRequest(PANES_LIST, server.requests.single())
        }
    }

    @Test
    fun panesUnsetNewestReplyReadsZero() {
        FakeOrreryServer().use { server ->
            server.respond(
                PANES_LIST,
                PanesListResponse.newBuilder().addPanes(PaneProto.newBuilder().setId("%3").setPane(2)).build(),
            )

            val panes = OrreryClient(server.baseUrl).panes()

            assertEquals(1, panes.size)
            assertEquals("%3", panes.single().id)
            assertEquals(0L, panes.single().newestReply)
        }
    }

    @Test
    fun trailingSlashOnBaseUrlIsIgnored() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())

            assertEquals(emptyList<Pane>(), OrreryClient("${server.baseUrl}/").panes())
            assertEquals(PANES_LIST, server.requests.single().rawPath)
        }
    }

    @Test
    fun sendTextPostsExactMessageWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond(SEND_TEXT, SendTextResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).sendText("%3", "echo hi")

            val request = server.requests.single()
            assertConnectRequest(SEND_TEXT, request)
            assertEquals(
                SendTextRequest.newBuilder().setPane("%3").setText("echo hi").build(),
                SendTextRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun sendComposedPostsWhatSendTextPosts() {
        FakeOrreryServer().use { server ->
            server.respond(SEND_TEXT, SendTextResponse.getDefaultInstance())
            val client = OrreryClient(server.baseUrl)

            client.sendText("%3", "echo hi")
            client.sendComposed("%3", "echo hi")

            val (plain, composed) = server.requests
            assertConnectRequest(SEND_TEXT, composed)
            assertEquals(plain.rawPath, composed.rawPath)

            assertEquals(plain.body.toList(), composed.body.toList())
        }
    }

    @Test
    fun sendComposedAnsweredAfterTenSecondsSucceeds() {
        FakeOrreryServer().use { server ->
            server.respond(
                SEND_TEXT,
                SendTextResponse.getDefaultInstance(),
                delayMillis = 11_000,
            )

            OrreryClient(server.baseUrl).sendComposed("%3", "echo hi")

            assertEquals(1, server.requests.size)
        }
    }

    @Test
    fun sendTextAnsweredAfterTenSecondsIsNoAnswer() {
        FakeOrreryServer().use { server ->
            server.respond(
                SEND_TEXT,
                SendTextResponse.getDefaultInstance(),
                delayMillis = 11_000,
            )

            assertThrows(NoAnswerException::class.java) {
                OrreryClient(server.baseUrl).sendText("%3", "echo hi")
            }
        }
    }

    @Test
    fun panesAnsweredAfterTenSecondsTimesOut() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance(), delayMillis = 11_000)

            assertThrows(SocketTimeoutException::class.java) {
                OrreryClient(server.baseUrl).panes()
            }
        }
    }

    @Test
    fun sendComposedWithNoAnswerInTimeThrowsNoAnswerAndSendsOnce() {
        FakeOrreryServer().use { server ->
            server.respond(
                SEND_TEXT,
                SendTextResponse.getDefaultInstance(),
                delayMillis = 1_500,
            )

            assertThrows(NoAnswerException::class.java) {
                OrreryClient(server.baseUrl, composeReadTimeoutMillis = 300).sendComposed("%3", "echo hi")
            }

            assertEquals(1, server.requests.size)
        }
    }

    @Test
    fun sendComposedRefusedConnectionThrowsIOExceptionThatIsNotNoAnswer() {
        val server = FakeOrreryServer()
        val baseUrl = server.baseUrl
        server.close()

        val error = assertThrows(IOException::class.java) {
            OrreryClient(baseUrl).sendComposed("%3", "echo hi")
        }

        assertFalse(error is NoAnswerException)
    }

    @Test
    fun sendComposedServerErrorThrowsCallExceptionCarryingCode() {
        FakeOrreryServer().use { server ->
            server.fail(SEND_TEXT, Code.INTERNAL_ERROR, "boom")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).sendComposed("%3", "echo hi")
            }

            assertEquals(Code.INTERNAL_ERROR, error.code)
            assertEquals("boom", error.message)
        }
    }

    @Test
    fun sendKeysOmitsExpectQuestionWhenUnguarded() {
        FakeOrreryServer().use { server ->
            server.respond(SEND_KEYS, SendKeysResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).sendKeys("%3", listOf("Enter", "Down"), null)

            assertEquals(KeysResult.Sent, result)
            val request = server.requests.single()
            assertConnectRequest(SEND_KEYS, request)
            assertEquals(
                SendKeysRequest.newBuilder().setPane("%3").addKeys("Enter").addKeys("Down").build(),
                SendKeysRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun sendKeysSendsExpectQuestionWhenGiven() {
        FakeOrreryServer().use { server ->
            server.respond(SEND_KEYS, SendKeysResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).sendKeys("%3", listOf("1"), "abc123")

            assertEquals(KeysResult.Sent, result)
            assertEquals(
                SendKeysRequest.newBuilder().setPane("%3").addKeys("1").setExpectQuestion("abc123").build(),
                SendKeysRequest.parseFrom(server.requests.single().body),
            )
        }
    }

    @Test
    fun sendKeysReturnsPromptChangedOnAborted() {
        FakeOrreryServer().use { server ->
            server.fail(SEND_KEYS, Code.ABORTED, "question refused")

            val result = OrreryClient(server.baseUrl).sendKeys("%3", listOf("1"), "stale")

            assertEquals(KeysResult.PromptChanged, result)
        }
    }

    @Test
    fun markSeenPostsReadUpToWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond(MARK_SEEN, MarkSeenResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).markSeen("%3", 1_700_000_500L)

            val request = server.requests.single()
            assertConnectRequest(MARK_SEEN, request)
            assertEquals(
                MarkSeenRequest.newBuilder().setPane("%3").setReadUpTo(1_700_000_500L).build(),
                MarkSeenRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun registerPushPostsSubscriptionWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond(PUSH_REGISTER, RegisterResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).registerPush(
                endpoint = "https://push.example.com/send/abc",
                p256dh = "example-p256dh",
                auth = "example-auth",
            )

            val request = server.requests.single()
            assertConnectRequest(PUSH_REGISTER, request)
            assertEquals(
                RegisterRequest.newBuilder()
                    .setEndpoint("https://push.example.com/send/abc")
                    .setP256Dh("example-p256dh")
                    .setAuth("example-auth")
                    .build(),
                RegisterRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun unregisterPushDeletesEndpointWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond(PUSH_UNREGISTER, UnregisterResponse.getDefaultInstance())

            OrreryClient(server.baseUrl).unregisterPush("https://push.example.com/send/abc")

            val request = server.requests.single()
            assertConnectRequest(PUSH_UNREGISTER, request)
            assertEquals(
                UnregisterRequest.newBuilder().setEndpoint("https://push.example.com/send/abc").build(),
                UnregisterRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsSendSendText() {
        assertDroppedWriteIsNoAnswerAndSentOnce(SEND_TEXT) { it.sendText("%3", "echo hi") }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsSendComposed() {
        assertDroppedWriteIsNoAnswerAndSentOnce(SEND_TEXT) { it.sendComposed("%3", "echo hi") }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsSendKeys() {
        assertDroppedWriteIsNoAnswerAndSentOnce(SEND_KEYS) { it.sendKeys("%3", listOf("1"), null) }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsMarkSeen() {
        assertDroppedWriteIsNoAnswerAndSentOnce(MARK_SEEN) { it.markSeen("%3", 1_700_000_500L) }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsRegisterPush() {
        assertDroppedWriteIsNoAnswerAndSentOnce(PUSH_REGISTER) { it.registerPush("https://push.example.com/send/abc", "p", "a") }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsUnregisterPush() {
        assertDroppedWriteIsNoAnswerAndSentOnce(PUSH_UNREGISTER) { it.unregisterPush("https://push.example.com/send/abc") }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsAsk() {
        assertDroppedWriteIsNoAnswerAndSentOnce(PICKER_ASK) { it.ask("example request") }
    }

    @Test
    fun writeCallsNeverRetryAfterAConnectionDropsRun() {
        assertDroppedWriteIsNoAnswerAndSentOnce(PICKER_RUN) { it.run("example-token-1") }
    }

    @Test
    fun panesServerErrorThrowsCallExceptionCarryingCode() {
        FakeOrreryServer().use { server ->
            server.fail(PANES_LIST, Code.INTERNAL_ERROR, "boom")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).panes()
            }

            assertEquals(Code.INTERNAL_ERROR, error.code)
        }
    }

    @Test
    fun panesReadsToolAndCurrentToolDecodesEveryField() {
        val tool = """{"id":"example-tool-use","name":"Bash","input":"example command","start":1700000250}"""
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, paneWithTool(tool))

            val pane = OrreryClient(server.baseUrl).panes().single()

            assertEquals(tool, pane.tool)
            assertEquals(
                PaneTool("example-tool-use", "Bash", "example command", 1700000250L),
                pane.currentTool(),
            )
        }
    }

    @Test
    fun panesWithoutToolHaveEmptyToolAndNoCurrentTool() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesProto)

            val panes = OrreryClient(server.baseUrl).panes()

            assertEquals(listOf("", ""), panes.map { it.tool })
            assertNull(panes[0].currentTool())
            assertNull(panes[1].currentTool())
        }
    }

    @Test
    fun malformedOrNonObjectToolTextHasNoCurrentTool() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, paneWithTool("{not json"))
            val malformed = OrreryClient(server.baseUrl).panes().single()
            assertEquals("{not json", malformed.tool)
            assertNull(malformed.currentTool())

            server.respond(PANES_LIST, paneWithTool("[1]"))
            val notObject = OrreryClient(server.baseUrl).panes().single()
            assertEquals("[1]", notObject.tool)
            assertNull(notObject.currentTool())
        }
    }

    @Test
    fun conversationParsesOriginAndEveryItemFieldAndAsksForFiftyItems() {
        val response = ConversationResponse.newBuilder()
            .setOrigin("Example opening prompt.")
            .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(1700000000L).setText("Example prompt."))
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("tool")
                    .setTime(1700000200L)
                    .setTool("Bash")
                    .setInput("example command")
                    .setId("example-tool-use"),
            )
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("result")
                    .setTime(1700000210L)
                    .setText("Example output.")
                    .setId("example-tool-use"),
            )
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("result")
                    .setTime(1700000220L)
                    .setText("Example failure.")
                    .setId("example-tool-use-2")
                    .setError(true),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val conversation = OrreryClient(server.baseUrl).conversation("%3")

            assertEquals(
                Conversation(
                    origin = "Example opening prompt.",
                    items = listOf(
                        ConversationItem("prompt", 1700000000L, "Example prompt.", "", "", "", false),
                        ConversationItem("tool", 1700000200L, "", "Bash", "example command", "example-tool-use", false),
                        ConversationItem("result", 1700000210L, "Example output.", "", "", "example-tool-use", false),
                        ConversationItem("result", 1700000220L, "Example failure.", "", "", "example-tool-use-2", true),
                    ),
                ),
                conversation,
            )
            val request = server.requests.single()
            assertConnectRequest(CONVERSATION, request)
            assertEquals(
                ConversationRequest.newBuilder().setPane("%3").setLimit(50).build(),
                ConversationRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun conversationNoticeItemsKeepTheirSortAndErrorFlag() {
        val response = ConversationResponse.newBuilder()
            .addItems(ConversationItemProto.newBuilder().setKind("notice").setTime(1700000010L).setNotice("compaction"))
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("notice")
                    .setTime(1700000020L)
                    .setText("Example task ran.")
                    .setNotice("task"),
            )
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("notice")
                    .setTime(1700000030L)
                    .setText("Example task broke.")
                    .setError(true)
                    .setNotice("task"),
            )
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("notice")
                    .setTime(1700000040L)
                    .setText("Example summary one.\nExample summary two.")
                    .setNotice("teammate"),
            )
            .addItems(ConversationItemProto.newBuilder().setKind("notice").setTime(1700000050L).setNotice("interrupted"))
            .addItems(ConversationItemProto.newBuilder().setKind("reply").setTime(1700000060L).setText("Example reply."))
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val items = requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).items

            assertEquals(
                listOf(
                    ConversationItem("notice", 1700000010L, "", "", "", "", false, "compaction"),
                    ConversationItem("notice", 1700000020L, "Example task ran.", "", "", "", false, "task"),
                    ConversationItem("notice", 1700000030L, "Example task broke.", "", "", "", true, "task"),
                    ConversationItem("notice", 1700000040L, "Example summary one.\nExample summary two.", "", "", "", false, "teammate"),
                    ConversationItem("notice", 1700000050L, "", "", "", "", false, "interrupted"),
                    ConversationItem("reply", 1700000060L, "Example reply.", "", "", "", false, ""),
                ),
                items,
            )
        }
    }

    @Test
    fun conversationReadsCompactionTriggerTokensAndContext() {
        val response = ConversationResponse.newBuilder()
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("compaction")
                    .setTime(1700000070L)
                    .setText("Example summary.")
                    .setTrigger("auto")
                    .setTokens(4200L),
            )
            .setContext(ConversationContextProto.newBuilder().setTokens(61000L).setWindow(200000L))
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val conversation = requireNotNull(OrreryClient(server.baseUrl).conversation("%3"))

            assertEquals(
                listOf(ConversationItem("compaction", 1700000070L, "Example summary.", "", "", "", false, "", "auto", 4200L)),
                conversation.items,
            )
            assertEquals(ConversationContext(tokens = 61000L, window = 200000L), conversation.context)
        }
    }

    @Test
    fun conversationWithoutTriggerTokensOrContextParsesWithDefaults() {
        val response = ConversationResponse.newBuilder()
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("notice")
                    .setTime(1700000080L)
                    .setText("Example summary.")
                    .setNotice("compaction"),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val conversation = requireNotNull(OrreryClient(server.baseUrl).conversation("%3"))

            assertEquals(
                listOf(ConversationItem("notice", 1700000080L, "Example summary.", "", "", "", false, "compaction", "", 0L)),
                conversation.items,
            )
            assertNull(conversation.context)
        }
    }

    private fun conversationGoal(goal: String): String =
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, ConversationResponse.newBuilder().setGoal(goal).build())
            requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).goal
        }

    @Test
    fun conversationReadsAPresentGoal() {
        assertEquals("Example goal text.", conversationGoal("Example goal text."))
    }

    @Test
    fun conversationEmptyGoalIsNone() {
        assertEquals("", conversationGoal(""))
    }

    private fun conversationWaiting(vararg rows: WaitingMessage): List<WaitingItem> =
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, ConversationResponse.newBuilder().addAllWaiting(rows.toList()).build())
            requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).waiting
        }

    @Test
    fun conversationReadsPresentWaitingRowsInOrder() {
        val waiting = conversationWaiting(
            WaitingMessage.newBuilder().setText("Example queued message.").setTime(1700000250L).setCanceled(false).build(),
            WaitingMessage.newBuilder().setText("Example cleared message.").setTime(1700000260L).setCanceled(true).build(),
        )

        assertEquals(
            listOf(
                WaitingItem("Example queued message.", 1700000250L, false),
                WaitingItem("Example cleared message.", 1700000260L, true),
            ),
            waiting,
        )
    }

    @Test
    fun conversationWithoutWaitingRowsHasNoWaitingRows() {
        assertEquals(emptyList<WaitingItem>(), conversationWaiting())
    }

    @Test
    fun waitingRowWithoutCanceledParsesAsNotCanceled() {
        val waiting = conversationWaiting(
            WaitingMessage.newBuilder().setText("Example queued message.").setTime(1700000250L).build(),
        )

        assertEquals(listOf(WaitingItem("Example queued message.", 1700000250L, false)), waiting)
    }

    @Test
    fun waitingRowWithoutTimeParsesAsTimeZero() {
        val waiting = conversationWaiting(
            WaitingMessage.newBuilder().setText("Example queued message.").setCanceled(true).build(),
        )

        assertEquals(listOf(WaitingItem("Example queued message.", 0L, true)), waiting)
    }

    @Test
    fun conversationItemWithoutNoticeParsesWithEmptyNotice() {
        val response = ConversationResponse.newBuilder()
            .addItems(ConversationItemProto.newBuilder().setKind("reply").setTime(1).setText("Example reply."))
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val item = requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).items.single()

            assertEquals("", item.notice)
        }
    }

    @Test
    fun conversationNotFoundReturnsNull() {
        FakeOrreryServer().use { server ->
            server.fail(CONVERSATION, Code.NOT_FOUND)

            assertNull(OrreryClient(server.baseUrl).conversation("%3"))
        }
    }

    @Test
    fun conversationServerErrorThrowsCallExceptionCarryingCode() {
        FakeOrreryServer().use { server ->
            server.fail(CONVERSATION, Code.INTERNAL_ERROR, "boom")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).conversation("%3")
            }

            assertEquals(Code.INTERNAL_ERROR, error.code)
        }
    }

    @Test
    fun hostParsesStatsWithAndWithoutPercent() {
        val response = HostStatusResponse.newBuilder()
            .setHost("example-host")
            .addStats(HostStatProto.newBuilder().setLabel("example-cpu").setValue("12%").setPercent(12.0).setLevel("ok"))
            .addStats(HostStatProto.newBuilder().setLabel("example-disk").setValue("example-value").setLevel("warn"))
            .build()
        FakeOrreryServer().use { server ->
            server.respond(HOST_STATUS, response)

            val status = OrreryClient(server.baseUrl).host()

            assertEquals(
                HostStatus(
                    host = "example-host",
                    stats = listOf(
                        HostStat("example-cpu", "12%", 12.0, "ok"),
                        HostStat("example-disk", "example-value", null, "warn"),
                    ),
                ),
                status,
            )
            assertConnectRequest(HOST_STATUS, server.requests.single())
        }
    }

    @Test
    fun hostNotFoundReturnsNull() {
        FakeOrreryServer().use { server ->
            server.fail(HOST_STATUS, Code.NOT_FOUND)

            assertNull(OrreryClient(server.baseUrl).host())
        }
    }

    @Test
    fun hostUnavailableThrowsCallExceptionCarryingCodeAndMessage() {
        FakeOrreryServer().use { server ->
            server.fail(HOST_STATUS, Code.UNAVAILABLE, "bad status")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).host()
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals("bad status", error.message)
        }
    }

    @Test
    fun askPostsExactMessageWithBothHeadersAndParsesEveryRowField() {
        val response = AskResponse.newBuilder()
            .addRows(
                PickerRow.newBuilder()
                    .setToken("example-token-1")
                    .setKind("jump")
                    .setDir("/home/user/project")
                    .setPane("%7")
                    .setLabel("example-label")
                    .setNote("example-note")
                    .setPrompt(false),
            )
            .addRows(
                PickerRow.newBuilder()
                    .setToken("example-token-2")
                    .setKind("fresh")
                    .setDir("/home/user/other")
                    .setPrompt(true),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.respond(PICKER_ASK, response)

            val rows = OrreryClient(server.baseUrl).ask("example request")

            assertEquals(
                listOf(
                    PickRow("example-token-1", "jump", "/home/user/project", "%7", "example-label", "example-note", false),
                    PickRow("example-token-2", "fresh", "/home/user/other", "", "", "", true),
                ),
                rows,
            )
            val request = server.requests.single()
            assertConnectRequest(PICKER_ASK, request)
            assertEquals(
                AskRequest.newBuilder().setText("example request").build(),
                AskRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun askAnsweredAfterTenSecondsSucceeds() {
        FakeOrreryServer().use { server ->
            server.respond(
                PICKER_ASK,
                AskResponse.getDefaultInstance(),
                delayMillis = 11_000,
            )

            assertEquals(emptyList<PickRow>(), OrreryClient(server.baseUrl).ask("example request"))
        }
    }

    @Test
    fun runPostsExactMessageWithBothHeadersAndReturnsPane() {
        FakeOrreryServer().use { server ->
            server.respond(PICKER_RUN, RunResponse.newBuilder().setPane("%7").build())

            assertEquals("%7", OrreryClient(server.baseUrl).run("example-token-1"))

            val request = server.requests.single()
            assertConnectRequest(PICKER_RUN, request)
            assertEquals(
                RunRequest.newBuilder().setToken("example-token-1").build(),
                RunRequest.parseFrom(request.body),
            )
        }
    }

    @Test
    fun askServerErrorThrowsCallExceptionCarryingReason() {
        FakeOrreryServer().use { server ->
            server.fail(PICKER_ASK, Code.UNAVAILABLE, "model failed: example reason")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).ask("example request")
            }

            assertEquals(Code.UNAVAILABLE, error.code)
            assertEquals("model failed: example reason", error.message)
        }
    }

    @Test
    fun runExpiredTokenThrowsCallExceptionCarryingReason() {
        FakeOrreryServer().use { server ->
            server.fail(PICKER_RUN, Code.FAILED_PRECONDITION, "token expired")

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).run("example-token-1")
            }

            assertEquals(Code.FAILED_PRECONDITION, error.code)
            assertEquals("token expired", error.message)
        }
    }

    @Test
    fun pickerErrorWithEmptyMessageFallsBackToCodeName() {
        FakeOrreryServer().use { server ->
            server.fail(PICKER_RUN, Code.NOT_FOUND)

            val error = assertThrows(OrreryCallException::class.java) {
                OrreryClient(server.baseUrl).run("example-token-1")
            }

            assertEquals(Code.NOT_FOUND, error.code)
            assertEquals("not_found", error.message)
        }
    }

    /**
     * A saved address the client cannot use must not crash its construction. Every call on it raises
     * an IOException, so the screens' IOException handling applies.
     */
    private fun assertUnusableAddressConstructsAndEveryCallRaisesIOException(address: String) {
        val client = OrreryClient(address)

        assertThrows(IOException::class.java) { client.panes() }
        assertThrows(IOException::class.java) { client.sendText("%3", "example") }
    }

    @Test
    fun ftpSchemeConstructsAndEveryCallRaisesIOException() {
        assertUnusableAddressConstructsAndEveryCallRaisesIOException("ftp://example")
    }

    @Test
    fun schemeLessAddressConstructsAndEveryCallRaisesIOException() {
        assertUnusableAddressConstructsAndEveryCallRaisesIOException("vermissian.example:8490")
    }

    @Test
    fun notAUrlConstructsAndEveryCallRaisesIOException() {
        assertUnusableAddressConstructsAndEveryCallRaisesIOException("not a url")
    }

    @Test
    fun nonNumericPortConstructsAndEveryCallRaisesIOException() {
        assertUnusableAddressConstructsAndEveryCallRaisesIOException("http://example-host:abc")
    }

    @Test
    fun spaceInHostConstructsAndEveryCallRaisesIOException() {
        assertUnusableAddressConstructsAndEveryCallRaisesIOException("https://example host:8490")
    }

    private companion object {
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val CONVERSATION = "/orrery.v1.Panes/Conversation"
        const val SEND_TEXT = "/orrery.v1.Panes/SendText"
        const val SEND_KEYS = "/orrery.v1.Panes/SendKeys"
        const val MARK_SEEN = "/orrery.v1.Panes/MarkSeen"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PUSH_REGISTER = "/orrery.v1.Push/Register"
        const val PUSH_UNREGISTER = "/orrery.v1.Push/Unregister"
        const val PICKER_ASK = "/orrery.v1.Picker/Ask"
        const val PICKER_RUN = "/orrery.v1.Picker/Run"
    }
}
