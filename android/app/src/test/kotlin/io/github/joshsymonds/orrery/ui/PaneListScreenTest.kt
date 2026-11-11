package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PaneListScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val TIMEOUT_MS = 5_000L
    }

    @get:Rule
    val composeRule = createComposeRule()

    private var openedPane: Pane? = null
    private val openedPaneId = AtomicReference<String?>(null)
    private var settingsOpened = 0

    private fun pane(
        id: String,
        cwd: String,
        label: String,
        state: String,
        summary: String,
        changed: Long,
        seen: Long,
    ): JSONObject = JSONObject()
        .put("id", id)
        .put("host", "example-host")
        .put("session", "example-session")
        .put("window", 1)
        .put("pane", 0)
        .put("cwd", cwd)
        .put("command", "claude")
        .put("state", state)
        .put("summary", summary)
        .put("label", label)
        .put("changed", changed)
        .put("seen", seen)
        .put("question", "")
        .put("question_hash", "")

    private fun pickerRowsBody(): String = JSONObject()
        .put(
            "rows",
            JSONArray().put(
                JSONObject()
                    .put("token", "tok-beta")
                    .put("kind", "jump")
                    .put("dir", "/home/user/beta")
                    .put("pane", "%3")
                    .put("label", "beta-tests")
                    .put("note", "")
                    .put("prompt", false),
            ),
        ).toString()

    private fun panesBody(): String = JSONArray(
        listOf(
            pane("%1", "/home/user/alpha", "alpha-build", "working", "Compiling the synthetic module.", NOW - 30, NOW - 10),
            pane("%2", "/home/user/alpha", "alpha-review", "waiting", "Waiting on a synthetic answer.", NOW - 300, NOW - 600),
            pane("%3", "/home/user/beta", "beta-tests", "idle", "Tests finished.", NOW - 7200, NOW - 7200),
            pane("%4", "/home/user/beta/", "beta-deploy", "working", "Deploy running.", NOW - 172_800, NOW - 200_000),
            pane("%5", "/home/user/alpha", "alpha-idle", "idle", "", 0, 0),
        ),
    ).toString()

    private fun setScreen(server: FakeOrreryServer) {
        composeRule.setContent {
            PaneListScreen(
                client = OrreryClient(server.baseUrl),
                onOpenPane = { openedPane = it },
                onOpenPaneId = { openedPaneId.set(it) },
                onOpenSettings = { settingsOpened++ },
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitRow(id: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasTestTag("pane-row:$id")) == 1 }
    }

    private fun awaitText(text: String) {
        composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasText(text)) > 0 }
    }

    private fun assertRowsShown(vararg ids: String) {
        for (id in listOf("%1", "%2", "%3", "%4", "%5")) {
            val expected = if (id in ids) 1 else 0
            assertEquals("row $id", expected, countNodes(hasTestTag("pane-row:$id")))
        }
    }

    private fun assertInGroup(project: String, id: String) {
        assertEquals(
            "row $id under $project",
            1,
            countNodes(hasTestTag("group:$project") and hasAnyDescendant(hasTestTag("pane-row:$id"))),
        )
    }

    @Test
    fun groupsRowsUnderProjectHeadersWithDetails() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithText("alpha").assertExists()
            composeRule.onNodeWithText("beta").assertExists()
            assertInGroup("alpha", "%1")
            assertInGroup("alpha", "%2")
            assertInGroup("beta", "%3")
            assertInGroup("beta", "%4")

            composeRule.onNodeWithText("alpha-build").assertExists()
            assertEquals(2, countNodes(hasText("working")))
            composeRule.onNodeWithText("Compiling the synthetic module.").assertExists()
            composeRule.onNodeWithText("30s").assertExists()
            composeRule.onNodeWithText("5m").assertExists()
            composeRule.onNodeWithText("2h").assertExists()
            composeRule.onNodeWithText("2d").assertExists()
        }
    }

    @Test
    fun rowWithoutChangeTimeShowsNoAge() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%5")

            // NOW seconds since the epoch is 19675 days; a row with changed == 0 must not show it.
            assertEquals(0, countNodes(hasText("19675d")))
        }
    }

    @Test
    fun unseenDotAppearsOnlyOnUnseenRows() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            assertEquals(2, countNodes(hasContentDescription("unseen")))
            assertEquals(
                1,
                countNodes(hasContentDescription("unseen") and hasAnyAncestor(hasTestTag("pane-row:%2"))),
            )
            assertEquals(
                1,
                countNodes(hasContentDescription("unseen") and hasAnyAncestor(hasTestTag("pane-row:%4"))),
            )
        }
    }

    @Test
    fun newFilterShowsOnlyUnseenRows() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("filter-new").performClick()

            assertRowsShown("%2", "%4")
            composeRule.onNodeWithText("alpha").assertExists()
            composeRule.onNodeWithText("beta").assertExists()
        }
    }

    @Test
    fun waitingFilterShowsOnlyWaitingRowsAndHidesEmptyGroups() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("filter-waiting").performClick()

            assertRowsShown("%2")
            composeRule.onNodeWithText("alpha").assertExists()
            assertEquals(0, countNodes(hasText("beta")))
        }
    }

    @Test
    fun allFilterRestoresEveryRow() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("filter-waiting").performClick()
            composeRule.onNodeWithTag("filter-all").performClick()

            assertRowsShown("%1", "%2", "%3", "%4", "%5")
        }
    }

    @Test
    fun tappingRowOpensThatPane() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%3")

            composeRule.onNodeWithTag("pane-row:%3").performClick()

            assertEquals("%3", openedPane?.id)
            assertEquals("beta-tests", openedPane?.label)
        }
    }

    @Test
    fun settingsButtonRunsOnOpenSettings() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithText("Settings").performClick()

            assertEquals(1, settingsOpened)
        }
    }

    @Test
    fun newThenShellOnAlphaPostsFirstVisiblePaneAndReloadsList() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            server.respond("POST", "/v1/panes", 200)
            setScreen(server)
            awaitRow("%1")

            composeRule.onNode(hasText("New") and hasAnyAncestor(hasTestTag("new-window:alpha"))).performClick()
            composeRule.onNodeWithText("Shell").performClick()
            composeRule.waitUntil(TIMEOUT_MS) {
                server.requests.count { it.method == "GET" && it.rawPath == "/v1/panes" } == 2
            }

            val posts = server.requests.filter { it.method == "POST" }
            assertEquals(1, posts.size)
            assertEquals("/v1/panes", posts.single().rawPath)
            val body = JSONObject(posts.single().body)
            assertEquals("%1", body.getString("pane"))
            assertEquals("shell", body.getString("kind"))
        }
    }

    @Test
    fun serverErrorShowsCouldNotLoadPanesAndRetryReloads() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 500)
            setScreen(server)

            awaitText("could not load panes")
            composeRule.onNodeWithText("Retry").assertExists()

            server.respond("GET", "/v1/panes", 200, panesBody())
            composeRule.onNodeWithText("Retry").performClick()

            awaitRow("%1")
            composeRule.onNodeWithText("alpha").assertExists()
        }
    }

    @Test
    fun malformedBodyShowsCouldNotLoadPanes() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, "not json")
            setScreen(server)

            awaitText("could not load panes")
        }
    }

    @Test
    fun newButtonSitsInTheTopHeaderAboveTheFilters() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            val newTop = composeRule.onNodeWithTag("picker-new").fetchSemanticsNode().boundsInRoot.top
            val filterTop = composeRule.onNodeWithTag("filter-all").fetchSemanticsNode().boundsInRoot.top
            assertTrue(newTop < filterTop)
        }
    }

    @Test
    fun newButtonOpensThePickerAndARunOpensItsPane() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            server.respond("POST", "/v1/picker/ask", 200, pickerRowsBody())
            server.respond("POST", "/v1/picker/run", 200, """{"pane":"%9"}""")
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("picker-new").performClick()
            composeRule.onNodeWithTag("picker-text").performTextInput("beta")
            composeRule.onNodeWithTag("picker-ask").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { countNodes(hasTestTag("picker-row:0")) == 1 }
            composeRule.onNodeWithTag("picker-row:0").performClick()
            composeRule.waitUntil(TIMEOUT_MS) { openedPaneId.get() != null }

            assertEquals("%9", openedPaneId.get())
            assertNull(openedPane)
            assertEquals(0, countNodes(hasTestTag("picker-text")))
        }
    }

    @Test
    fun paneRowStillOpensItsPaneWhilePickerIsOpen() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%3")

            composeRule.onNodeWithTag("picker-new").performClick()
            composeRule.onNodeWithTag("picker-text").assertExists()
            composeRule.onNodeWithTag("pane-row:%3").performClick()

            assertEquals("%3", openedPane?.id)
            assertNull(openedPaneId.get())
        }
    }

    @Test
    fun cancelHidesThePicker() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("picker-new").performClick()
            composeRule.onNodeWithTag("picker-cancel").performClick()

            assertEquals(0, countNodes(hasTestTag("picker-text")))
        }
    }
}
