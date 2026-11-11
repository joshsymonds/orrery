package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PickerPanelTest {
    private companion object {
        const val TIMEOUT_MS = 5_000L
        const val ASK_DELAY_MS = 800L
        const val ASK_PATH = "/v1/picker/ask"
        const val RUN_PATH = "/v1/picker/run"
    }

    @get:Rule
    val composeRule = createComposeRule()

    private val opened = AtomicReference<String?>(null)
    private val closed = AtomicInteger(0)

    private fun rowsBody(): String {
        val rows = JSONArray()
            .put(
                JSONObject()
                    .put("token", "tok-jump")
                    .put("kind", "jump")
                    .put("dir", "/work/alpha")
                    .put("pane", "%4")
                    .put("label", "alpha window")
                    .put("note", "")
                    .put("prompt", false),
            )
            .put(
                JSONObject()
                    .put("token", "tok-resume")
                    .put("kind", "resume")
                    .put("dir", "/work/beta")
                    .put("pane", "")
                    .put("label", "")
                    .put("note", "fix the flaky test")
                    .put("prompt", true),
            )
        return JSONObject().put("rows", rows).toString()
    }

    private fun setPanel(server: FakeOrreryServer) {
        composeRule.setContent {
            PickerPanel(
                client = OrreryClient(server.baseUrl),
                onOpened = { opened.set(it) },
                onClose = { closed.incrementAndGet() },
            )
        }
    }

    private fun submit(text: String) {
        composeRule.onNodeWithTag("picker-text").performTextInput(text)
        composeRule.onNodeWithTag("picker-ask").performClick()
    }

    private fun rowExists(index: Int): Boolean =
        composeRule.onAllNodesWithTag("picker-row:$index").fetchSemanticsNodes().isNotEmpty()

    private fun rowText(index: Int): String =
        composeRule.onNodeWithTag("picker-row:$index").fetchSemanticsNode()
            .config[SemanticsProperties.Text].joinToString(" ") { it.text }

    private fun postsTo(server: FakeOrreryServer, path: String): List<RecordedRequest> =
        server.requests.filter { it.method == "POST" && it.rawPath == path }

    @Test
    fun submitShowsWorkingThenRowsWithKindFolderLabelNoteAndPromptFlag() {
        FakeOrreryServer().use { server ->
            server.respond("POST", ASK_PATH, 200, rowsBody(), delayMillis = ASK_DELAY_MS)
            setPanel(server)

            submit("fix the test")

            composeRule.onNodeWithTag("picker-working").assertIsDisplayed()
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(0) }
            assertTrue(composeRule.onAllNodesWithTag("picker-working").fetchSemanticsNodes().isEmpty())

            val asks = postsTo(server, ASK_PATH)
            assertEquals(1, asks.size)
            assertEquals("fix the test", JSONObject(asks.single().body).getString("text"))

            assertTrue(rowText(0).contains("jump"))
            assertTrue(rowText(0).contains("/work/alpha"))
            assertTrue(rowText(0).contains("alpha window"))
            assertFalse(rowText(0).contains("first prompt"))

            assertTrue(rowText(1).contains("resume"))
            assertTrue(rowText(1).contains("/work/beta"))
            assertTrue(rowText(1).contains("fix the flaky test"))
            assertTrue(rowText(1).contains("first prompt"))
        }
    }

    @Test
    fun tappingSecondRowRunsItsTokenAndReportsItsPane() {
        FakeOrreryServer().use { server ->
            server.respond("POST", ASK_PATH, 200, rowsBody())
            server.respond("POST", RUN_PATH, 200, JSONObject().put("pane", "%9").toString())
            setPanel(server)

            submit("fix the test")
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(1) }
            composeRule.onNodeWithTag("picker-row:1").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { opened.get() != null }

            assertEquals("%9", opened.get())
            val runs = postsTo(server, RUN_PATH)
            assertEquals(1, runs.size)
            assertEquals("tok-resume", JSONObject(runs.single().body).getString("token"))
        }
    }

    @Test
    fun askFailureShowsItsReasonAndTheTextCanBeResubmitted() {
        FakeOrreryServer().use { server ->
            server.respond("POST", ASK_PATH, 502, "no answer from the host")
            setPanel(server)

            submit("fix the test")
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag("picker-error").assertTextEquals("no answer from the host")
            assertFalse(rowExists(0))

            server.respond("POST", ASK_PATH, 200, rowsBody())
            composeRule.onNodeWithTag("picker-ask").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(0) }

            assertEquals(2, postsTo(server, ASK_PATH).size)
            assertTrue(composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isEmpty())
        }
    }

    @Test
    fun runFailureShowsItsReasonAndTheRowsStay() {
        FakeOrreryServer().use { server ->
            server.respond("POST", ASK_PATH, 200, rowsBody())
            server.respond("POST", RUN_PATH, 409, "the session is gone")
            setPanel(server)

            submit("fix the test")
            composeRule.waitUntil(TIMEOUT_MS) { rowExists(0) }
            composeRule.onNodeWithTag("picker-row:0").performClick()
            composeRule.waitUntil(TIMEOUT_MS) {
                composeRule.onAllNodesWithTag("picker-error").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("picker-error").assertTextEquals("the session is gone")
            assertTrue(rowExists(0))
            assertTrue(rowExists(1))
            assertEquals(null, opened.get())
        }
    }

    @Test
    fun blankTextPostsNothing() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-text").performTextInput("   ")
            composeRule.onNodeWithTag("picker-ask").performClick()

            assertTrue(server.requests.isEmpty())
        }
    }

    @Test
    fun cancelClosesThePanel() {
        FakeOrreryServer().use { server ->
            setPanel(server)

            composeRule.onNodeWithTag("picker-cancel").performClick()

            assertEquals(1, closed.get())
        }
    }
}
