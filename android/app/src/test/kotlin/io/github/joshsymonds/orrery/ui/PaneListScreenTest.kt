package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
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
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
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
        newestReply: Long,
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
        .put("newest_reply", newestReply)

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

    // %1 changed after it was last seen but its newest reply is older, so it is read: no dot.
    // %2 and %4 have a reply newer than they were seen, so they are unread.
    private fun panesBody(): String = JSONArray(
        listOf(
            pane("%1", ALPHA, "alpha-build", "working", "Compiling the synthetic module.", NOW - 30, NOW - 300, NOW - 400),
            pane("%2", ALPHA, "alpha-review", "waiting", "Waiting on a synthetic answer.", NOW - 300, NOW - 600, NOW - 300),
            pane("%3", BETA, "beta-tests", "idle", "Tests finished.", NOW - 7200, NOW - 7200, NOW - 7200),
            pane("%4", "$BETA/", "beta-deploy", "working", "Deploy running.", NOW - 172_800, NOW - 200_000, NOW - 172_800),
            pane("%5", ALPHA, "alpha-idle", "idle", "", 0, 0, 0),
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

    private fun hasTagPrefix(prefix: String): SemanticsMatcher =
        SemanticsMatcher("test tag starts with $prefix") {
            it.config.contains(SemanticsProperties.TestTag) &&
                it.config[SemanticsProperties.TestTag].startsWith(prefix)
        }

    private fun nodeOf(matcher: SemanticsMatcher) =
        composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode()

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

    private fun assertNoFolderHeaders() {
        assertEquals(0, countNodes(hasTagPrefix("folder:")))
    }

    @Test
    fun allNestsPanesUnderFolderHeadersAndCollapsesOneOnTap() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            assertEquals(1, countNodes(hasTestTag("folder:$ALPHA")))
            assertEquals(1, countNodes(hasTestTag("folder:$BETA")))
            assertTrue(nodeOf(hasTestTag("folder:$ALPHA")).boundsInRoot.top < nodeOf(hasTestTag("pane-row:%1")).boundsInRoot.top)
            assertTrue(nodeOf(hasTestTag("pane-row:%5")).boundsInRoot.top < nodeOf(hasTestTag("folder:$BETA")).boundsInRoot.top)
            assertTrue(nodeOf(hasTestTag("folder:$BETA")).boundsInRoot.top < nodeOf(hasTestTag("pane-row:%3")).boundsInRoot.top)
            assertTrue(nodeOf(hasText("alpha-build")).boundsInRoot.left > nodeOf(hasText("alpha")).boundsInRoot.left)

            composeRule.onNodeWithTag("folder:$ALPHA").performClick()

            assertRowsShown("%3", "%4")
            assertEquals(1, countNodes(hasTestTag("folder:$ALPHA")))
            composeRule.onNodeWithTag("folder:$ALPHA").performClick()
            assertRowsShown("%1", "%2", "%3", "%4", "%5")
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
    fun newFilterListsUnreadPanesNewestFirstWithProjectAndNoFolderHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("filter-new").performClick()

            assertRowsShown("%2", "%4")
            assertNoFolderHeaders()
            assertTrue(nodeOf(hasTestTag("pane-row:%2")).boundsInRoot.top < nodeOf(hasTestTag("pane-row:%4")).boundsInRoot.top)
            assertEquals(1, countNodes(hasText("alpha") and hasAnyAncestor(hasTestTag("pane-row:%2"))))
            assertEquals(1, countNodes(hasText("beta") and hasAnyAncestor(hasTestTag("pane-row:%4"))))
        }
    }

    @Test
    fun waitingFilterListsWaitingPanesFlatWithProjectAndNoFolderHeaders() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("filter-waiting").performClick()

            assertRowsShown("%2")
            assertNoFolderHeaders()
            assertEquals(1, countNodes(hasText("alpha") and hasAnyAncestor(hasTestTag("pane-row:%2"))))
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
    fun pickerNewIsTheOnlyNewButtonInEveryView() {
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes", 200, panesBody())
            setScreen(server)
            awaitRow("%1")

            for (filter in listOf("filter-all", "filter-new", "filter-waiting")) {
                composeRule.onNodeWithTag(filter).performClick()

                assertEquals(
                    "$filter shows picker-new",
                    1,
                    countNodes(hasText("New") and hasAnyAncestor(hasTestTag("picker-new"))),
                )
                // The filter tab also reads "New"; every other clickable "New" is a New button.
                val newButtons = hasText("New") and hasClickAction() and !hasTestTag("filter-new")
                assertEquals(
                    "$filter has exactly one New button",
                    1,
                    composeRule.onAllNodes(newButtons).fetchSemanticsNodes().size,
                )
            }
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
