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
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.gen.orrery.v1.AskResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerRow
import io.github.joshsymonds.orrery.gen.orrery.v1.RunResponse
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
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val PICKER_ASK = "/orrery.v1.Picker/Ask"
        const val PICKER_RUN = "/orrery.v1.Picker/Run"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val HEADLESS_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        const val HEADLESS_LABEL = "alpha-agent · a1b2c3d4"
        const val HEADLESS_SUMMARY = "Waiting on a synthetic plan."
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
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState(state)
        .setSummary(summary)
        .setLabel(label)
        .setChanged(changed)
        .setSeen(seen)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(newestReply)
        .build()

    /** A waiting headless session in ALPHA, unseen: its newest reply is after it was last seen. */
    private fun headlessBody(): ListHeadlessResponse = ListHeadlessResponse.newBuilder()
        .addSessions(
            HeadlessSession.newBuilder()
                .setId(HEADLESS_ID)
                .setDir(ALPHA)
                .setLabel(HEADLESS_LABEL)
                .setState("waiting")
                .setChanged(NOW - 60)
                .setSeen(0)
                .setSummary(HEADLESS_SUMMARY)
                .setQuestion("Proceed with the synthetic plan?")
                .setNewestReply(NOW - 60),
        )
        .build()

    private fun pickerRowsResponse(): AskResponse = AskResponse.newBuilder()
        .addRows(
            PickerRow.newBuilder()
                .setToken("tok-beta")
                .setKind("jump")
                .setDir("/home/user/beta")
                .setPane("%3")
                .setLabel("beta-tests")
                .setNote("")
                .setPrompt(false),
        )
        .build()

    // %1 changed after it was last seen but its newest reply is older, so it is read: no dot.
    // %2 and %4 have a reply newer than they were seen, so they are unread.
    private fun panesBody(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(pane("%1", ALPHA, "alpha-build", "working", "Compiling the synthetic module.", NOW - 30, NOW - 300, NOW - 400))
        .addPanes(pane("%2", ALPHA, "alpha-review", "waiting", "Waiting on a synthetic answer.", NOW - 300, NOW - 600, NOW - 300))
        .addPanes(pane("%3", BETA, "beta-tests", "idle", "Tests finished.", NOW - 7200, NOW - 7200, NOW - 7200))
        .addPanes(pane("%4", "$BETA/", "beta-deploy", "working", "Deploy running.", NOW - 172_800, NOW - 200_000, NOW - 172_800))
        .addPanes(pane("%5", ALPHA, "alpha-idle", "idle", "", 0, 0, 0))
        .build()

    private fun setScreen(server: FakeOrreryServer) {
        composeRule.setContent {
            PaneListScreen(
                client = OrreryClient(server.baseUrl),
                onOpenPane = { openedPane = it },
                onOpenPaneId = { openedPaneId.set(it) },
                onStartedSession = {},
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
            server.respond(PANES_LIST, panesBody())
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
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%5")

            // NOW seconds since the epoch is 19675 days; a row with changed == 0 must not show it.
            assertEquals(0, countNodes(hasText("19675d")))
        }
    }

    @Test
    fun unseenDotAppearsOnlyOnUnseenRows() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
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
            server.respond(PANES_LIST, panesBody())
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
            server.respond(PANES_LIST, panesBody())
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
            server.respond(PANES_LIST, panesBody())
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
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%3")

            composeRule.onNodeWithTag("pane-row:%3").performClick()

            assertEquals("%3", openedPane?.id)
            assertEquals("beta-tests", openedPane?.label)
        }
    }

    @Test
    fun headlessSessionJoinsTheAllTreeUnderItsDirFolder() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, headlessBody())
            setScreen(server)
            awaitRow(HEADLESS_ID)

            assertRowsShown("%1", "%2", "%3", "%4", "%5")
            assertEquals(1, countNodes(hasTestTag("folder:$ALPHA")))
            assertTrue(nodeOf(hasTestTag("folder:$ALPHA")).boundsInRoot.top < nodeOf(hasTestTag("pane-row:$HEADLESS_ID")).boundsInRoot.top)
            assertEquals(1, countNodes(hasText(HEADLESS_LABEL) and hasAnyAncestor(hasTestTag("pane-row:$HEADLESS_ID"))))
            assertEquals(1, countNodes(hasText(HEADLESS_SUMMARY) and hasAnyAncestor(hasTestTag("pane-row:$HEADLESS_ID"))))
            assertEquals(1, countNodes(hasText("waiting") and hasAnyAncestor(hasTestTag("pane-row:$HEADLESS_ID"))))
            assertEquals(
                1,
                countNodes(hasContentDescription("unseen") and hasAnyAncestor(hasTestTag("pane-row:$HEADLESS_ID"))),
            )
        }
    }

    @Test
    fun waitingFilterListsTheHeadlessWaitingSessionWithThePanes() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, headlessBody())
            setScreen(server)
            awaitRow(HEADLESS_ID)

            composeRule.onNodeWithTag("filter-waiting").performClick()

            assertRowsShown("%2")
            assertEquals(1, countNodes(hasTestTag("pane-row:$HEADLESS_ID")))
            assertNoFolderHeaders()
        }
    }

    @Test
    fun tappingHeadlessRowOpensItsSessionAsAHeadlessPane() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, headlessBody())
            setScreen(server)
            awaitRow(HEADLESS_ID)

            composeRule.onNodeWithTag("pane-row:$HEADLESS_ID").performClick()

            assertEquals(HEADLESS_ID, openedPane?.id)
            assertEquals(true, openedPane?.headless)
            assertNull(openedPaneId.get())
        }
    }

    @Test
    fun unimplementedHeadlessListLeavesThePanesAlone() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.fail(LIST_HEADLESS, Code.UNIMPLEMENTED, "no headless sessions here")
            setScreen(server)

            awaitRow("%1")
            assertRowsShown("%1", "%2", "%3", "%4", "%5")
            assertEquals(0, countNodes(hasText("could not load panes")))
        }
    }

    @Test
    fun unavailableHeadlessListLeavesThePanesAlone() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.fail(LIST_HEADLESS, Code.UNAVAILABLE, "orreryd is not taking session commands")
            setScreen(server)

            awaitRow("%1")
            assertRowsShown("%1", "%2", "%3", "%4", "%5")
            assertEquals(0, countNodes(hasText("could not load panes")))
        }
    }

    @Test
    fun unscriptedHeadlessListNotFoundLeavesThePanesAlone() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)

            awaitRow("%1")
            assertRowsShown("%1", "%2", "%3", "%4", "%5")
            assertEquals(0, countNodes(hasText("could not load panes")))
        }
    }

    @Test
    fun internalHeadlessListErrorShowsCouldNotLoadPanes() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.fail(LIST_HEADLESS, Code.INTERNAL_ERROR, "boom")
            setScreen(server)

            awaitText("could not load panes")
            assertEquals(0, countNodes(hasTestTag("pane-row:%1")))
        }
    }

    @Test
    fun settingsButtonRunsOnOpenSettings() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithText("Settings").performClick()

            assertEquals(1, settingsOpened)
        }
    }

    @Test
    fun pickerNewIsTheOnlyNewButtonInEveryView() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
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
            server.fail(PANES_LIST, Code.INTERNAL_ERROR, "boom")
            setScreen(server)

            awaitText("could not load panes")
            composeRule.onNodeWithText("Retry").assertExists()

            server.respond(PANES_LIST, panesBody())
            composeRule.onNodeWithText("Retry").performClick()

            awaitRow("%1")
            composeRule.onNodeWithText("alpha").assertExists()
        }
    }

    @Test
    fun malformedBodyShowsCouldNotLoadPanes() {
        FakeOrreryServer().use { server ->
            server.route(PANES_LIST) { Reply.Raw("not json".toByteArray(Charsets.UTF_8)) }
            setScreen(server)

            awaitText("could not load panes")
        }
    }

    @Test
    fun newButtonSitsInTheTopHeaderAboveTheFilters() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            val newTop = composeRule.onNodeWithTag("picker-new").fetchSemanticsNode().boundsInRoot.top
            val filterTop = composeRule.onNodeWithTag("filter-all").fetchSemanticsNode().boundsInRoot.top
            assertTrue(newTop < filterTop)
        }
    }

    @Test
    fun titleRowShowsTheIconAsADiscLeftOfPanes() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            val mark = nodeOf(hasTestTag("title-mark")).boundsInRoot
            val title = nodeOf(hasText("Panes")).boundsInRoot
            assertTrue(mark.right <= title.left)
            assertEquals(title.center.y.toDouble(), mark.center.y.toDouble(), 1.0)
            assertEquals(title.height.toDouble(), mark.height.toDouble(), 1.0)
        }
    }

    @Test
    fun newButtonOpensThePickerAndARunOpensItsPane() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(PICKER_ASK, pickerRowsResponse())
            server.respond(PICKER_RUN, RunResponse.newBuilder().setPane("%9").build())
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
            server.respond(PANES_LIST, panesBody())
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
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("picker-new").performClick()
            composeRule.onNodeWithTag("picker-cancel").performClick()

            assertEquals(0, countNodes(hasTestTag("picker-text")))
        }
    }
}
