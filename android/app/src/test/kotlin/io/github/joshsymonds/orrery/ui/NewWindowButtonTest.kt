package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class NewWindowButtonTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val opened = AtomicInteger(0)

    private fun setButton(server: FakeOrreryServer) {
        composeRule.setContent {
            NewWindowButton(
                paneId = "%3",
                client = OrreryClient(server.baseUrl),
                onOpened = { opened.incrementAndGet() },
            )
        }
    }

    private fun assertOpenRequest(server: FakeOrreryServer, kind: String) {
        val posts: List<RecordedRequest> = server.requests.filter { it.method == "POST" }
        assertEquals(1, posts.size)
        assertEquals("/v1/panes", posts.single().rawPath)
        val body = JSONObject(posts.single().body)
        assertEquals(setOf("pane", "kind"), body.keys().asSequence().toSet())
        assertEquals("%3", body.getString("pane"))
        assertEquals(kind, body.getString("kind"))
    }

    @Test
    fun claudePanePostsClaudeKindAndReportsOpened() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes", 200)
            setButton(server)

            composeRule.onNodeWithText("New").performClick()
            composeRule.onNodeWithText("Claude pane").performClick()
            composeRule.waitUntil(5_000) { opened.get() == 1 }

            assertOpenRequest(server, "claude")
        }
    }

    @Test
    fun shellPostsShellKindAndReportsOpened() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes", 200)
            setButton(server)

            composeRule.onNodeWithText("New").performClick()
            composeRule.onNodeWithText("Shell").performClick()
            composeRule.waitUntil(5_000) { opened.get() == 1 }

            assertOpenRequest(server, "shell")
        }
    }

    @Test
    fun serverFailureShowsCouldNotOpenAndDoesNotReportOpened() {
        FakeOrreryServer().use { server ->
            server.respond("POST", "/v1/panes", 500)
            setButton(server)

            composeRule.onNodeWithText("New").performClick()
            composeRule.onNodeWithText("Shell").performClick()
            composeRule.waitUntil(5_000) {
                composeRule.onAllNodesWithText("could not open").fetchSemanticsNodes().isNotEmpty()
            }

            assertEquals(0, opened.get())
        }
    }
}
