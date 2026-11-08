package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.RecordedRequest
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class QuestionCardTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val keysPath = "/v1/panes/%253/keys"
    private val toolQuestion = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""
    private val askQuestion = """
        {"tool":"AskUserQuestion","questions":[{"question":"Pick a synthetic option?",
         "header":"Pick","options":[{"label":"Synthetic one","description":"d"},
         {"label":"Synthetic two","description":"d"}],"multiSelect":false}]}
    """.trimIndent()

    private fun pane(question: String): Pane = Pane(
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
        question = question,
        questionHash = "abc123",
    )

    private fun setCard(server: FakeOrreryServer, question: String, onRefresh: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { QuestionCard(pane(question), client, onRefresh) }
        }
    }

    private fun assertKeysRequest(request: RecordedRequest, keys: String) {
        assertEquals("POST", request.method)
        assertEquals(keysPath, request.rawPath)
        val body = JSONObject(request.body)
        assertEquals(keys, body.getJSONArray("keys").toString())
        assertEquals("abc123", body.getString("expect_question"))
    }

    @Test
    fun toolQuestionRendersTitleAndThreeButtons() {
        FakeOrreryServer().use { server ->
            setCard(server, toolQuestion) {}

            composeRule.onNodeWithText("Bash: echo synthetic").assertIsDisplayed()
            composeRule.onNodeWithText("1 Yes").assertIsDisplayed()
            composeRule.onNodeWithText("2 Yes, don't ask again").assertIsDisplayed()
            composeRule.onNodeWithText("3 No").assertIsDisplayed()
        }
    }

    @Test
    fun tappingToolOptionPostsKeyWithHashAndRefreshes() {
        FakeOrreryServer().use { server ->
            server.respond("POST", keysPath, 200)
            var refreshes = 0
            setCard(server, toolQuestion) { refreshes++ }

            composeRule.onNodeWithText("1 Yes").performClick()
            composeRule.waitUntil(timeoutMillis = 5_000) { refreshes == 1 }

            assertEquals(1, server.requests.size)
            assertKeysRequest(server.requests.single(), """["1"]""")
        }
    }

    @Test
    fun askUserQuestionRendersLabelsAndPostsTappedNumber() {
        FakeOrreryServer().use { server ->
            server.respond("POST", keysPath, 200)
            var refreshes = 0
            setCard(server, askQuestion) { refreshes++ }

            composeRule.onNodeWithText("Pick a synthetic option?").assertIsDisplayed()
            composeRule.onNodeWithText("Synthetic one").assertIsDisplayed()
            composeRule.onNodeWithText("Synthetic two").performClick()
            composeRule.waitUntil(timeoutMillis = 5_000) { refreshes == 1 }

            assertEquals(1, server.requests.size)
            assertKeysRequest(server.requests.single(), """["2"]""")
        }
    }

    @Test
    fun promptChangedShowsMessageAndRefreshes() {
        FakeOrreryServer().use { server ->
            server.respond("POST", keysPath, 409)
            var refreshes = 0
            setCard(server, toolQuestion) { refreshes++ }

            composeRule.onNodeWithText("1 Yes").performClick()
            composeRule.waitUntil(timeoutMillis = 5_000) { refreshes == 1 }

            composeRule.onNodeWithText("prompt changed").assertIsDisplayed()
            assertKeysRequest(server.requests.single(), """["1"]""")
        }
    }

    @Test
    fun serverErrorShowsNotSentAndDoesNotRefresh() {
        FakeOrreryServer().use { server ->
            server.respond("POST", keysPath, 500)
            var refreshes = 0
            setCard(server, toolQuestion) { refreshes++ }

            composeRule.onNodeWithText("1 Yes").performClick()
            composeRule.waitUntil(timeoutMillis = 5_000) { server.requests.isNotEmpty() }
            composeRule.onNodeWithText("not sent").assertIsDisplayed()

            assertEquals(0, refreshes)
        }
    }

    @Test
    fun plainTextQuestionIsItsOwnTitle() {
        FakeOrreryServer().use { server ->
            setCard(server, "Plain synthetic question?") {}

            composeRule.onNodeWithText("Plain synthetic question?").assertIsDisplayed()
            composeRule.onNodeWithText("3 No").assertIsDisplayed()
        }
    }

    @Test
    fun truncatedMarkerTitleIsTheTool() {
        FakeOrreryServer().use { server ->
            setCard(server, """{"tool":"Bash","truncated":true,"sha256":"synthetic"}""") {}

            composeRule.onNodeWithText("Bash").assertIsDisplayed()
            composeRule.onNodeWithText("2 Yes, don't ask again").assertIsDisplayed()
        }
    }
}
