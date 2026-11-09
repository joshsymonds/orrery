package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.lifecycle.Lifecycle
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val SCREEN_PATH = "/v1/panes/%253/screen"
private const val SEEN_PATH = "/v1/panes/%253/seen"
private const val PANES_PATH = "/v1/panes"
private const val KEYS_PATH = "/v1/panes/%253/keys"
private const val TIMEOUT_MILLIS = 5_000L

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class PaneScreenTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private val toolQuestion = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""

    private fun panesJson(question: String): String = """
        [{"id":"%3","host":"example-host","session":"example-session","window":1,"pane":2,
          "cwd":"/home/user/project","command":"claude","state":"waiting",
          "summary":"Example summary.","label":"example-label",
          "changed":4102444800,"seen":4102444900,"question":${JSONObject.quote(question)},
          "question_hash":"abc123"}]
    """.trimIndent()

    private fun serve(server: FakeOrreryServer, body: String, question: String = "") {
        server.respond("GET", SCREEN_PATH, 200, body)
        server.respond("GET", PANES_PATH, 200, panesJson(question))
        server.respond("POST", SEEN_PATH, 200)
    }

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }
    }

    private fun screenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "GET" && it.rawPath == SCREEN_PATH }

    private fun seenRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == SEEN_PATH }

    @Test
    fun openingShowsScreenBodyAndMarksSeenOnce() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic screen body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic screen body"), TIMEOUT_MILLIS)
            composeRule.waitForIdle()

            composeRule.onNodeWithText("synthetic screen body").assertIsDisplayed()
            assertEquals(1, seenRequests(server))
        }
    }

    @Test
    fun screenRefreshesEverySecondWhileVisible() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic first body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic first body"), TIMEOUT_MILLIS)
            assertEquals(1, screenRequests(server))

            server.respond("GET", SCREEN_PATH, 200, "synthetic second body")
            composeRule.mainClock.advanceTimeBy(999)
            composeRule.waitForIdle()
            assertEquals(1, screenRequests(server))

            composeRule.mainClock.advanceTimeBy(1)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic second body"), TIMEOUT_MILLIS)
            assertEquals(2, screenRequests(server))
        }
    }

    @Test
    fun noRequestsBelowStartedAndPollingResumesOnReturn() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic visible body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic visible body"), TIMEOUT_MILLIS)

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.CREATED)
            composeRule.waitForIdle()
            val stoppedAt = screenRequests(server)
            composeRule.mainClock.advanceTimeBy(3_000)
            composeRule.waitForIdle()
            assertEquals(stoppedAt, screenRequests(server))

            composeRule.activityRule.scenario.moveToState(Lifecycle.State.RESUMED)
            composeRule.waitUntil(TIMEOUT_MILLIS) { screenRequests(server) > stoppedAt }
        }
    }

    @Test
    fun failedTickKeepsTextAndNextGoodTickClearsMessage() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic kept body")
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic kept body"), TIMEOUT_MILLIS)

            server.respond("GET", SCREEN_PATH, 500)
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("could not refresh"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("synthetic kept body").assertIsDisplayed()

            server.respond("GET", SCREEN_PATH, 200, "synthetic recovered body")
            composeRule.mainClock.advanceTimeBy(1_000)
            composeRule.waitUntilExactlyOneExists(hasText("synthetic recovered body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("could not refresh").assertDoesNotExist()
        }
    }

    @Test
    fun paneWithQuestionShowsCardButtons() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body", question = toolQuestion)
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("1 Yes"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("Bash: echo synthetic").assertIsDisplayed()
            composeRule.onNodeWithText("2 Yes, don't ask again").assertIsDisplayed()
            composeRule.onNodeWithText("3 No").assertIsDisplayed()
        }
    }

    @Test
    fun paneWithoutQuestionShowsNoCard() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("1 Yes").assertDoesNotExist()
            composeRule.onNodeWithText("3 No").assertDoesNotExist()
        }
    }

    @Test
    fun cardRefreshFetchesScreenImmediately() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body", question = toolQuestion)
            server.respond("POST", KEYS_PATH, 200)
            setScreen(server)
            composeRule.waitUntilExactlyOneExists(hasText("1 Yes"), TIMEOUT_MILLIS)
            assertEquals(1, screenRequests(server))

            composeRule.mainClock.autoAdvance = false
            composeRule.onNodeWithText("1 Yes").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { screenRequests(server) == 2 }
        }
    }

    @Test
    fun sendAndQuickKeysAreShown() {
        FakeOrreryServer().use { server ->
            serve(server, "synthetic body")
            setScreen(server)

            composeRule.waitUntilExactlyOneExists(hasText("synthetic body"), TIMEOUT_MILLIS)
            composeRule.onNodeWithText("Send").assertIsDisplayed()
            composeRule.onNodeWithText("Esc").assertExists()
        }
    }
}
