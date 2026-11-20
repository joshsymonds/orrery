package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.atomic.AtomicInteger
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val REQUEST_ID = "synthetic-request"
private const val QUESTION = "Pick a synthetic option?"
private const val OPTION_ONE = "Synthetic one"
private const val ANSWER_PATH = "/orrery.v1.Control/Answer"
private const val NOW = 1_700_000_000L
private const val UNANSWERABLE = "This question cannot be answered from here. Reply in words below."
private const val REFUSED_NOTE =
    "Not answered: the question changed before your answer arrived. This is what it asks now."

// A headless session's Ask in the AskHost that answers it: a request with no options is not answerable from the phone,
// and an answer the server refuses as invalid is refused with the Ask's note and no refresh.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SessionControlsTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun emptyOptionsShowUnanswerableAndPostNothing() {
        setAsk(emptyList()) {}

        composeRule.onNodeWithTag("ask-unanswerable").assertTextEquals(UNANSWERABLE)
        composeRule.onNodeWithTag("ask-question").assertTextEquals(QUESTION)
        composeRule.waitForIdle()
        assertTrue(composeRule.onAllNodesWithTag("ask-option:1").fetchSemanticsNodes().isEmpty())
        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun invalidArgumentAnswerLeavesTheAskWithItsNoteAndDoesNotRefresh() {
        server.fail(ANSWER_PATH, Code.INVALID_ARGUMENT, "synthetic refusal")
        val refreshes = AtomicInteger()
        setAsk(listOf(OPTION_ONE)) { refreshes.incrementAndGet() }

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("ask-note").assertTextEquals(REFUSED_NOTE)
        composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
        assertEquals(1, server.requests.size)
        assertEquals(0, refreshes.get())
    }

    private fun setAsk(options: List<String>, onRefresh: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        val model = sessionAsk(pane(), SessionRequest(id = REQUEST_ID, kind = "question", question = QUESTION, options = options))
        composeRule.setContent {
            MaterialTheme {
                AskHost(model = model, client = client, now = NOW, onRefresh = onRefresh)
            }
        }
    }

    private fun pane(): Pane = Pane(
        id = SESSION_ID,
        host = "",
        session = "",
        window = 0,
        pane = 0,
        cwd = "/home/user/project",
        command = "",
        state = "waiting",
        summary = "",
        label = "example-label",
        changed = NOW - 60,
        seen = 0L,
        question = "",
        questionHash = "",
        headless = true,
    )
}
