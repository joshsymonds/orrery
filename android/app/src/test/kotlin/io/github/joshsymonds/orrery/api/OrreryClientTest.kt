package io.github.joshsymonds.orrery.api

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.IOException
import java.net.SocketTimeoutException

class OrreryClientTest {
    private val panesJson = """
        [
          {"id":"%3","host":"example-host","session":"example-session","window":1,"pane":2,
           "cwd":"/home/user/project","command":"claude","state":"waiting",
           "summary":"Example summary.","label":"example-label",
           "changed":4102444800,"seen":4102444900,"question":"Example question?",
           "question_hash":"abc123","newest_reply":4102444950},
          {"id":"%4","host":"example-host","session":"other-session","window":0,"pane":0,
           "cwd":"/home/user/other","command":"zsh","state":"",
           "summary":"","label":"","changed":0,"seen":0,"question":"",
           "question_hash":"","newest_reply":null}
        ]
    """.trimIndent()

    private val panesJsonWithoutNewestReply = """
        [
          {"id":"%3","host":"example-host","session":"example-session","window":1,"pane":2,
           "cwd":"/home/user/project","command":"claude","state":"waiting",
           "summary":"Example summary.","label":"example-label",
           "changed":4102444800,"seen":4102444900,"question":"Example question?",
           "question_hash":"abc123"}
        ]
    """.trimIndent()

    private fun assertWriteHeaders(request: RecordedRequest) {
        assertEquals("application/json", request.headers["content-type"])
        assertEquals("orrery-android", request.headers["x-orrery-client"])
    }

    private fun assertJsonBody(expected: String, actual: String) {
        val want = JSONObject(expected)
        val got = JSONObject(actual)
        assertEquals(want.keys().asSequence().toSet(), got.keys().asSequence().toSet())
        for (key in want.keys().asSequence()) {
            assertEquals("field $key of $actual", want.get(key).toString(), got.get(key).toString())
        }
    }

    @Test
    fun panesParsesEveryField() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesJson)

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
            assertEquals("GET", server.requests.single().method)
        }
    }

    @Test
    fun panesFromPreNewestReplyServerReadZero() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesJsonWithoutNewestReply)

            val panes = OrreryClient(server.baseUrl).panes()

            assertEquals(1, panes.size)
            assertEquals("%3", panes.single().id)
            assertEquals(0L, panes.single().newestReply)
        }
    }

    @Test
    fun screenAsksForColouredHistoryAndReturnsBodyVerbatim() {
        val screen = "\u001B[38;2;166;227;161mline one\u001B[0m  \nline two\n\tindent ✓\n"
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/screen", 200, screen)

            assertEquals(screen, OrreryClient(server.baseUrl).screen("%3"))

            val request = server.requests.single()
            assertEquals("GET", request.method)
            assertEquals("/v1/panes/%253/screen", request.rawPath)
            assertEquals("ansi=1&history=500", request.rawQuery)
        }
    }

    @Test
    fun screenFromPreV060ServerReturnsPlainTextUnchanged() {
        val screen = "plain line\nsecond line\n"
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/screen", 200, screen)

            assertEquals(screen, OrreryClient(server.baseUrl).screen("%3"))
            assertEquals("ansi=1&history=500", server.requests.single().rawQuery)
        }
    }

    @Test
    fun trailingSlashOnBaseUrlIsIgnored() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, "[]")

            assertEquals(emptyList<Pane>(), OrreryClient("${server.baseUrl}/").panes())
            assertEquals("/v1/panes", server.requests.single().rawPath)
        }
    }

    @Test
    fun sendTextPostsExactBodyWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/text", 200)

            OrreryClient(server.baseUrl).sendText("%3", "echo hi")

            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals("/v1/panes/%253/text", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"text":"echo hi"}""", request.body)
        }
    }

    @Test
    fun sendComposedPostsWhatSendTextPosts() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/text", 200)
            val client = OrreryClient(server.baseUrl)

            client.sendText("%3", "echo hi")
            client.sendComposed("%3", "echo hi")

            val (plain, composed) = server.requests
            assertEquals("POST", composed.method)
            assertEquals(plain.rawPath, composed.rawPath)
            assertEquals(plain.rawQuery, composed.rawQuery)
            assertWriteHeaders(composed)
            assertEquals(plain.body, composed.body)
            assertJsonBody("""{"text":"echo hi"}""", composed.body)
        }
    }

    @Test
    fun sendComposedAnsweredAfterTenSecondsSucceeds() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/text", 200, delayMillis = 11_000)

            OrreryClient(server.baseUrl).sendComposed("%3", "echo hi")

            assertEquals(1, server.requests.size)
        }
    }

    @Test
    fun sendTextAnsweredAfterTenSecondsTimesOut() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/text", 200, delayMillis = 11_000)

            assertThrows(SocketTimeoutException::class.java) {
                OrreryClient(server.baseUrl).sendText("%3", "echo hi")
            }
        }
    }

    @Test
    fun getAnsweredAfterTenSecondsTimesOut() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, "[]", delayMillis = 11_000)

            assertThrows(SocketTimeoutException::class.java) {
                OrreryClient(server.baseUrl).panes()
            }
        }
    }

    @Test
    fun sendComposedWithNoAnswerInTimeThrowsNoAnswerAndSendsOnce() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/text", 200, delayMillis = 1_500)

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
    fun sendComposedServerErrorThrowsHttpExceptionCarryingStatus() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/text", 500, "boom")

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).sendComposed("%3", "echo hi")
            }

            assertEquals(500, error.status)
        }
    }

    @Test
    fun sendKeysOmitsExpectQuestionWhenUnguarded() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/keys", 200)

            val result = OrreryClient(server.baseUrl).sendKeys("%3", listOf("Enter", "Down"), null)

            assertEquals(KeysResult.Sent, result)
            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals("/v1/panes/%253/keys", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"keys":["Enter","Down"]}""", request.body)
        }
    }

    @Test
    fun sendKeysSendsExpectQuestionWhenGiven() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/keys", 200)

            val result = OrreryClient(server.baseUrl).sendKeys("%3", listOf("1"), "abc123")

            assertEquals(KeysResult.Sent, result)
            val request = server.requests.single()
            assertWriteHeaders(request)
            assertJsonBody("""{"keys":["1"],"expect_question":"abc123"}""", request.body)
        }
    }

    @Test
    fun sendKeysReturnsPromptChangedOn409() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/keys", 409)

            val result = OrreryClient(server.baseUrl).sendKeys("%3", listOf("1"), "stale")

            assertEquals(KeysResult.PromptChanged, result)
        }
    }

    @Test
    fun markSeenPostsReadUpToWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/seen", 200)

            OrreryClient(server.baseUrl).markSeen("%3", 1_700_000_500L)

            val request = server.requests.single()
            assertEquals("/v1/panes/%253/seen", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"read_up_to":1700000500}""", request.body)
        }
    }

    @Test
    fun registerPushPostsSubscriptionWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/push/register", 200)

            OrreryClient(server.baseUrl).registerPush(
                endpoint = "https://push.example.com/send/abc",
                p256dh = "example-p256dh",
                auth = "example-auth",
            )

            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals("/v1/push/register", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody(
                """{"endpoint":"https://push.example.com/send/abc","p256dh":"example-p256dh","auth":"example-auth"}""",
                request.body,
            )
        }
    }

    @Test
    fun unregisterPushDeletesEndpointWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("DELETE", "/v1/push/register", 200)

            OrreryClient(server.baseUrl).unregisterPush("https://push.example.com/send/abc")

            val request = server.requests.single()
            assertEquals("DELETE", request.method)
            assertEquals("/v1/push/register", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"endpoint":"https://push.example.com/send/abc"}""", request.body)
        }
    }

    @Test
    fun serverErrorThrowsHttpExceptionCarryingStatus() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 500, "boom")

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).panes()
            }

            assertEquals(500, error.status)
        }
    }

    private fun paneJsonWithTool(tool: String): String =
        """[{"id":"%3","host":"example-host","session":"example-session","window":1,"pane":2,
           "cwd":"/home/user/project","command":"claude","state":"working",
           "summary":"","label":"","changed":0,"seen":0,"question":"",
           "question_hash":"","tool":${JSONObject.quote(tool)}}]"""

    @Test
    fun panesReadsToolAndCurrentToolDecodesEveryField() {
        val tool = """{"id":"example-tool-use","name":"Bash","input":"example command","start":1700000250}"""
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, paneJsonWithTool(tool))

            val pane = OrreryClient(server.baseUrl).panes().single()

            assertEquals(tool, pane.tool)
            assertEquals(
                PaneTool("example-tool-use", "Bash", "example command", 1700000250L),
                pane.currentTool(),
            )
        }
    }

    @Test
    fun panesWithoutToolKeyHaveEmptyToolAndNoCurrentTool() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesJson)

            val panes = OrreryClient(server.baseUrl).panes()

            assertEquals(listOf("", ""), panes.map { it.tool })
            assertNull(panes[0].currentTool())
            assertNull(panes[1].currentTool())
        }
    }

    @Test
    fun malformedOrNonObjectToolTextHasNoCurrentTool() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, paneJsonWithTool("{not json"))
            val malformed = OrreryClient(server.baseUrl).panes().single()
            assertEquals("{not json", malformed.tool)
            assertNull(malformed.currentTool())

            server.respond("GET", "/v1/panes", 200, paneJsonWithTool("[1]"))
            val notObject = OrreryClient(server.baseUrl).panes().single()
            assertEquals("[1]", notObject.tool)
            assertNull(notObject.currentTool())
        }
    }

    @Test
    fun conversationParsesOriginAndEveryItemFieldAndAsksForFiftyItems() {
        val body = """
            {"origin":"Example opening prompt.","items":[
              {"kind":"prompt","time":1700000000,"text":"Example prompt.","tool":"","input":"","id":"","error":false},
              {"kind":"tool","time":1700000200,"text":"","tool":"Bash","input":"example command","id":"example-tool-use","error":false},
              {"kind":"result","time":1700000210,"text":"Example output.","tool":"","input":"","id":"example-tool-use","error":false},
              {"kind":"result","time":1700000220,"text":"Example failure.","tool":"","input":"","id":"example-tool-use-2","error":true}
            ]}
        """.trimIndent()
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 200, body)

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
            assertEquals("GET", request.method)
            assertEquals("/v1/panes/%253/conversation", request.rawPath)
            assertEquals("limit=50", request.rawQuery)
        }
    }

    @Test
    fun conversationNoticeItemsKeepTheirSortAndErrorFlag() {
        val body = """
            {"origin":"","items":[
              {"kind":"notice","time":1700000010,"text":"","tool":"","input":"","id":"","error":false,"notice":"compaction"},
              {"kind":"notice","time":1700000020,"text":"Example task ran.","tool":"","input":"","id":"","error":false,"notice":"task"},
              {"kind":"notice","time":1700000030,"text":"Example task broke.","tool":"","input":"","id":"","error":true,"notice":"task"},
              {"kind":"notice","time":1700000040,"text":"Example summary one.\nExample summary two.","tool":"","input":"","id":"","error":false,"notice":"teammate"},
              {"kind":"notice","time":1700000050,"text":"","tool":"","input":"","id":"","error":false,"notice":"interrupted"},
              {"kind":"reply","time":1700000060,"text":"Example reply.","tool":"","input":"","id":"","error":false,"notice":""}
            ],"goal":""}
        """.trimIndent()
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 200, body)

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

    private fun conversationGoal(goalJson: String): String {
        val body = """{"origin":"","items":[]$goalJson}"""
        return FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 200, body)
            requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).goal
        }
    }

    @Test
    fun conversationReadsAPresentGoal() {
        assertEquals("Example goal text.", conversationGoal(""","goal":"Example goal text.""""))
    }

    @Test
    fun conversationEmptyGoalIsNone() {
        assertEquals("", conversationGoal(""","goal":"""""))
    }

    @Test
    fun conversationWithoutGoalKeyParsesWithNoGoal() {
        assertEquals("", conversationGoal(""))
    }

    @Test
    fun conversationNullGoalIsNone() {
        assertEquals("", conversationGoal(""","goal":null"""))
    }

    private fun conversationWaiting(waitingJson: String): List<WaitingItem> {
        val body = """{"origin":"","items":[]$waitingJson}"""
        return FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 200, body)
            requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).waiting
        }
    }

    @Test
    fun conversationReadsPresentWaitingRowsInOrder() {
        val waiting = conversationWaiting(
            ""","waiting":[""" +
                """{"text":"Example queued message.","time":1700000250,"canceled":false},""" +
                """{"text":"Example cleared message.","time":1700000260,"canceled":true}]""",
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
    fun conversationWithoutWaitingKeyHasNoWaitingRows() {
        assertEquals(emptyList<WaitingItem>(), conversationWaiting(""))
    }

    @Test
    fun conversationNullWaitingIsNoWaitingRows() {
        assertEquals(emptyList<WaitingItem>(), conversationWaiting(""","waiting":null"""))
    }

    @Test
    fun conversationEmptyWaitingArrayIsNoWaitingRows() {
        assertEquals(emptyList<WaitingItem>(), conversationWaiting(""","waiting":[]"""))
    }

    @Test
    fun waitingRowWithoutCanceledKeyParsesAsNotCanceled() {
        val waiting = conversationWaiting(
            ""","waiting":[{"text":"Example queued message.","time":1700000250}]""",
        )

        assertEquals(listOf(WaitingItem("Example queued message.", 1700000250L, false)), waiting)
    }

    @Test
    fun waitingRowWithoutTimeKeyParsesAsTimeZero() {
        val waiting = conversationWaiting(
            ""","waiting":[{"text":"Example queued message.","canceled":true}]""",
        )

        assertEquals(listOf(WaitingItem("Example queued message.", 0L, true)), waiting)
    }

    @Test
    fun conversationItemWithoutNoticeKeyParsesWithEmptyNotice() {
        val body = """{"origin":"","items":[{"kind":"reply","time":1,"text":"Example reply.","tool":"","input":"","id":"","error":false}]}"""
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 200, body)

            val item = requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).items.single()

            assertEquals("", item.notice)
        }
    }

    @Test
    fun conversation404ReturnsNull() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 404)

            assertNull(OrreryClient(server.baseUrl).conversation("%3"))
        }
    }

    @Test
    fun conversationServerErrorThrowsHttpExceptionCarryingStatus() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 500, "boom")

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).conversation("%3")
            }

            assertEquals(500, error.status)
        }
    }

    @Test
    fun hostParsesStatsWithAndWithoutPercent() {
        val body = """
            {"host":"example-host","stats":[
              {"label":"example-cpu","value":"12%","percent":12,"level":"ok"},
              {"label":"example-disk","value":"example-value","level":"warn"}
            ]}
        """.trimIndent()
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/host", 200, body)

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
            assertEquals("GET", server.requests.single().method)
        }
    }

    @Test
    fun host404ReturnsNull() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/host", 404)

            assertNull(OrreryClient(server.baseUrl).host())
        }
    }

    @Test
    fun hostBadGatewayThrowsHttpExceptionCarryingStatus() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/host", 502, "bad status")

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).host()
            }

            assertEquals(502, error.status)
        }
    }

    @Test
    fun askPostsExactBodyWithBothHeadersAndParsesEveryRowField() {
        val body = """
            {"rows":[
              {"token":"example-token-1","kind":"jump","dir":"/home/user/project","pane":"%7","label":"example-label","note":"example-note","prompt":false},
              {"token":"example-token-2","kind":"fresh","dir":"/home/user/other","pane":"","label":"","note":"","prompt":true}
            ]}
        """.trimIndent()
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/picker/ask", 200, body)

            val rows = OrreryClient(server.baseUrl).ask("example request")

            assertEquals(
                listOf(
                    PickRow("example-token-1", "jump", "/home/user/project", "%7", "example-label", "example-note", false),
                    PickRow("example-token-2", "fresh", "/home/user/other", "", "", "", true),
                ),
                rows,
            )
            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals("/v1/picker/ask", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"text":"example request"}""", request.body)
        }
    }

    @Test
    fun askAnsweredAfterTenSecondsSucceeds() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/picker/ask", 200, """{"rows":[]}""", delayMillis = 11_000)

            assertEquals(emptyList<PickRow>(), OrreryClient(server.baseUrl).ask("example request"))
        }
    }

    @Test
    fun runPostsExactBodyWithBothHeadersAndReturnsPane() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/picker/run", 200, """{"pane":"%7"}""")

            assertEquals("%7", OrreryClient(server.baseUrl).run("example-token-1"))

            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals("/v1/picker/run", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"token":"example-token-1"}""", request.body)
        }
    }

    @Test
    fun askServerErrorThrowsHttpExceptionCarryingStatusAndReason() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/picker/ask", 502, "model failed: example reason\n")

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).ask("example request")
            }

            assertEquals(502, error.status)
            assertEquals("model failed: example reason", error.message)
        }
    }

    @Test
    fun runExpiredTokenThrowsHttpExceptionCarryingStatusAndReason() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/picker/run", 410, "token expired")

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).run("example-token-1")
            }

            assertEquals(410, error.status)
            assertEquals("token expired", error.message)
        }
    }

    @Test
    fun pickerErrorWithEmptyBodyFallsBackToStatusMessage() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/picker/run", 404)

            val error = assertThrows(OrreryHttpException::class.java) {
                OrreryClient(server.baseUrl).run("example-token-1")
            }

            assertEquals(404, error.status)
            assertEquals("HTTP 404", error.message)
        }
    }
}
