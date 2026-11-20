package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performImeAction
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.AskResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerRow
import io.github.joshsymonds.orrery.gen.orrery.v1.RunResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.atomic.AtomicReference
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PaneListScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val HOST = "example-host"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val PICKER_ASK = "/orrery.v1.Picker/Ask"
        const val PICKER_RUN = "/orrery.v1.Picker/Run"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val HEADLESS_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        const val HEADLESS_LABEL = "alpha-agent · a1b2c3d4"
        const val UNSEEN_WORDS = "new since you looked"
        const val SHELL_SESSION = "_orrery-shell-1"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var openedPane: Pane? = null
    private val openedPaneId = AtomicReference<String?>(null)
    private var settingsOpened = 0

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun sessionTag(sessionId: String): String = "convo:session:$sessionId"

    private fun pane(
        id: String,
        cwd: String,
        label: String,
        state: String,
        summary: String,
        changed: Long,
        seen: Long,
        newestReply: Long,
        session: String = "example-session",
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(HOST)
        .setSession(session)
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
                .setSummary("Waiting on a synthetic plan.")
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

    /** Serves the host as the panes' host, so the panes and the headless sessions of one directory share a card. */
    private fun setScreen(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PaneListScreen(
                client = client,
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

    private fun awaitRow(id: String) {
        composeRule.awaitUntil { countNodes(hasTestTag(convoTag(id))) == 1 }
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    private fun awaitText(text: String) {
        composeRule.awaitUntil { countNodes(hasText(text)) > 0 }
    }

    private fun assertRowsShown(vararg ids: String) {
        for (id in listOf("%1", "%2", "%3", "%4", "%5")) {
            val expected = if (id in ids) 1 else 0
            assertEquals("row $id", expected, countNodes(hasTestTag(convoTag(id))))
        }
    }

    @Test
    fun tappingAConversationOpensItsPane() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%3")

            composeRule.onNodeWithTag(convoTag("%3")).performClick()

            assertEquals("%3", openedPane?.id)
            assertEquals("beta-tests", openedPane?.label)
        }
    }

    @Test
    fun headlessSessionDrawsUnderItsDirectoryCard() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, headlessBody())
            setScreen(server)
            awaitNode(hasTestTag(sessionTag(HEADLESS_ID)))

            assertRowsShown("%1", "%2", "%3", "%4", "%5")
            assertEquals(
                1,
                countNodes(hasTestTag(sessionTag(HEADLESS_ID)) and hasAnyAncestor(hasTestTag("project:|$ALPHA"))),
            )
            assertEquals(1, countNodes(hasText(HEADLESS_LABEL) and hasAnyAncestor(hasTestTag(sessionTag(HEADLESS_ID)))))
            assertEquals(1, countNodes(hasText("? Proceed with the synthetic plan?") and hasAnyAncestor(hasTestTag(sessionTag(HEADLESS_ID)))))
            assertEquals(
                1,
                countNodes(hasContentDescription(UNSEEN_WORDS) and hasAnyAncestor(hasTestTag(sessionTag(HEADLESS_ID)))),
            )
        }
    }

    @Test
    fun tappingHeadlessRowOpensItsSessionAsAHeadlessPane() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, headlessBody())
            setScreen(server)
            awaitNode(hasTestTag(sessionTag(HEADLESS_ID)))

            composeRule.onNodeWithTag(sessionTag(HEADLESS_ID)).performClick()

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
            assertEquals(0, countNodes(hasText("The server did not answer.")))
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
            assertEquals(0, countNodes(hasText("The server did not answer.")))
        }
    }

    @Test
    fun unscriptedHeadlessListNotFoundLeavesThePanesAlone() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)

            awaitRow("%1")
            assertRowsShown("%1", "%2", "%3", "%4", "%5")
            assertEquals(0, countNodes(hasText("The server did not answer.")))
        }
    }

    @Test
    fun internalHeadlessListErrorShowsTheServerDidNotAnswer() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.fail(LIST_HEADLESS, Code.INTERNAL_ERROR, "boom")
            setScreen(server)

            awaitText("The server did not answer.")
            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))
        }
    }

    @Test
    fun malformedBodyShowsTheServerDidNotAnswer() {
        FakeOrreryServer().use { server ->
            server.route(PANES_LIST) { Reply.Raw("not json".toByteArray(Charsets.UTF_8)) }
            setScreen(server)

            awaitText("The server did not answer.")
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
    fun theHeadHoldsTheOnlyNewButtonWhileTheTreeIsListed() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            assertEquals(1, countNodes(hasTestTag("picker-new")))
            assertEquals(0, countNodes(hasTestTag("empty-new")))
        }
    }

    @Test
    fun newButtonSitsInTheHeadAboveTheTree() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            val newTop = composeRule.onNodeWithTag("picker-new").fetchSemanticsNode().boundsInRoot.top
            val cardTop = composeRule.onNodeWithTag("project:|$ALPHA").fetchSemanticsNode().boundsInRoot.top
            assertTrue(newTop < cardTop)
        }
    }

    @Test
    fun titleRowShowsTheIconAsADiscLeftOfTheWord() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            val mark = composeRule.onNodeWithTag("title-mark").fetchSemanticsNode().boundsInRoot
            val title = composeRule.onNodeWithText("orrery").fetchSemanticsNode().boundsInRoot
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
            composeRule.onNodeWithTag("picker-text").performImeAction()
            composeRule.awaitUntil { countNodes(hasTestTag("picker-row:0")) == 1 }
            composeRule.onNodeWithTag("picker-row:0").performSemanticsAction(SemanticsActions.OnClick)
            composeRule.awaitUntil { openedPaneId.get() != null }

            assertEquals("%9", openedPaneId.get())
            assertNull(openedPane)
            assertEquals(0, countNodes(hasTestTag("picker-text")))
        }
    }

    @Test
    fun dismissingThePickerSheetHidesIt() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            composeRule.onNodeWithTag("picker-new").performClick()
            composeRule.onNodeWithTag("picker-text").assertExists()
            composeRule.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.Dismiss))
                .performSemanticsAction(SemanticsActions.Dismiss)

            composeRule.awaitUntil { countNodes(hasTestTag("picker-text")) == 0 }
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

            assertEquals(2, countNodes(hasContentDescription(UNSEEN_WORDS)))
            assertEquals(
                1,
                countNodes(hasContentDescription(UNSEEN_WORDS) and hasAnyAncestor(hasTestTag(convoTag("%2")))),
            )
            assertEquals(
                1,
                countNodes(hasContentDescription(UNSEEN_WORDS) and hasAnyAncestor(hasTestTag(convoTag("%4")))),
            )
        }
    }

    @Test
    fun shellSessionPanesAreHiddenFromTheTree() {
        FakeOrreryServer().use { server ->
            server.respond(
                PANES_LIST,
                panesBody().toBuilder()
                    .addPanes(pane("%6", ALPHA, "shell-pane", "waiting", "Internal shell.", NOW - 30, NOW - 300, NOW - 60, session = SHELL_SESSION))
                    .addPanes(pane("%7", ALPHA, "ordinary-pane", "waiting", "Waiting on a reply.", NOW - 30, NOW - 300, NOW - 60))
                    .addPanes(pane("%8", ALPHA, "near-miss-pane", "waiting", "Waiting on a reply.", NOW - 30, NOW - 300, NOW - 60, session = "orrery-shell-2"))
                    .build(),
            )
            setScreen(server)
            awaitRow("%7")

            assertEquals("the shell pane is not drawn", 0, countNodes(hasTestTag(convoTag("%6"))))
            assertEquals("the ordinary pane is drawn", 1, countNodes(hasTestTag(convoTag("%7"))))
            assertEquals("the near-miss pane is drawn", 1, countNodes(hasTestTag(convoTag("%8"))))
        }
    }

    @Test
    fun endedLineIsAbsentWhileNoSessionHasEnded() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            server.respond(LIST_HEADLESS, headlessBody())
            setScreen(server)
            awaitNode(hasTestTag(sessionTag(HEADLESS_ID)))

            assertEquals(0, countNodes(hasTagPrefix("ended:")))
        }
    }

    @Test
    fun aPaneOnTheServingHostDrawsItsCardWithNoHostName() {
        FakeOrreryServer().use { server ->
            server.respond(PANES_LIST, panesBody())
            setScreen(server)
            awaitRow("%1")

            assertEquals(0, countNodes(hasTagPrefix("project:$HOST|")))
            assertEquals(1, countNodes(hasTestTag("project:|$ALPHA")))
        }
    }
}
