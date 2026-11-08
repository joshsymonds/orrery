package io.github.joshsymonds.orrery.api

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class OrreryClientTest {
    private val panesJson = """
        [
          {"id":"%3","host":"example-host","session":"example-session","window":1,"pane":2,
           "cwd":"/home/user/project","command":"claude","state":"waiting",
           "summary":"Example summary.","label":"example-label",
           "changed":4102444800,"seen":4102444900,"question":"Example question?",
           "question_hash":"abc123"},
          {"id":"%4","host":"example-host","session":"other-session","window":0,"pane":0,
           "cwd":"/home/user/other","command":"zsh","state":"",
           "summary":"","label":"","changed":0,"seen":0,"question":"",
           "question_hash":""}
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
                    ),
                ),
                panes,
            )
            assertEquals("GET", server.requests.single().method)
        }
    }

    @Test
    fun screenReturnsBodyVerbatimAndEncodesPaneId() {
        val screen = "line one  \nline two\n\tindent ✓\n"
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/screen", 200, screen)

            assertEquals(screen, OrreryClient(server.baseUrl).screen("%3"))
            assertEquals("/v1/panes/%253/screen", server.requests.single().rawPath)
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
    fun markSeenPostsEmptyBodyWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes/%253/seen", 200)

            OrreryClient(server.baseUrl).markSeen("%3")

            val request = server.requests.single()
            assertEquals("/v1/panes/%253/seen", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("{}", request.body)
        }
    }

    @Test
    fun openWindowPostsPaneAndKindWithBothHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes", 200)

            OrreryClient(server.baseUrl).openWindow("%3", WindowKind.CLAUDE)

            val request = server.requests.single()
            assertEquals("POST", request.method)
            assertEquals("/v1/panes", request.rawPath)
            assertWriteHeaders(request)
            assertJsonBody("""{"pane":"%3","kind":"claude"}""", request.body)
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
}
