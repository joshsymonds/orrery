package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry as HostEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.tapInboxAction
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * R3 end to end, on invented hosts with one unreachable. The inbox head's Hosts action opens the hosts view, which lists
 * each host's load percent, memory percent and reach, and the unreachable host with no numbers. A pane's head and band,
 * and a session's, draw their host's label and no host meter. Every screen runs on the fixed clock [NOW].
 */
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class HostsDroidHostsViewTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val FIXTURE_CHANGED = 4102444800L
        const val FIXTURE_SEEN = 4102444900L
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val PANE_STREAM = "/orrery.v1.Panes/WatchConversation"
        const val PANE_CONVERSATION = "/orrery.v1.Panes/Conversation"
        const val PANE_SEEN = "/orrery.v1.Panes/MarkSeen"
        const val SESSION_STREAM = "/orrery.v1.Sessions/WatchConversation"
        const val HEADLESS_LIST = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val NOTICES_LIST = "/orrery.v1.Notices/List"
        const val PANE_ID = "%3"
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val GOAL = "synthetic goal text"
        const val HEAD = "head"
        const val HEAD_TITLE = "head-title"
        const val BAND = "band"
        const val HOST_LABEL = "host-label"
        const val HOST_METERS = "host-meters"

        // Invented hosts. The serving host answers, the second answers, and the third does not, whatever numbers it holds.
        const val SERVING = "example-serving"
        const val ANSWERING = "example-answering"
        const val SILENT = "example-silent"
    }

    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    /** A host meter's node: the meters' grid, or one meter's own node. */
    private val hostMeter = SemanticsMatcher("a host meter") { node ->
        val tag = node.config.getOrElse(SemanticsProperties.TestTag) { "" }
        tag == HOST_METERS || tag.startsWith("meter")
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    /** The matcher [matcher] within the row of host [name]. */
    private fun inRow(name: String, matcher: SemanticsMatcher): SemanticsMatcher =
        matcher and hasAnyAncestor(hasTestTag("host-row:$name"))

    private fun hostEntry(name: String, load: Double, memory: Double, reachable: Boolean): HostEntryProto =
        HostEntryProto.newBuilder()
            .setName(name)
            .setLoadPercent(load)
            .setMemoryPercent(memory)
            .setReachable(reachable)
            .build()

    /** The Host.Status the server answers: the serving host's own stat, and the three hosts in the server's order. */
    private fun hostStatus(): HostStatusResponse =
        HostStatusResponse.newBuilder()
            .setHost(SERVING)
            .addStats(HostStatProto.newBuilder().setLabel("cpu").setValue("37%").setPercent(37.2).setLevel("ok"))
            .addHosts(hostEntry(SERVING, 37.2, 58.9, true))
            .addHosts(hostEntry(ANSWERING, 84.6, 3.4, true))
            .addHosts(hostEntry(SILENT, 55.0, 60.0, false))
            .build()

    /** Draws the inbox, whose Hosts action opens the hosts view in its place, as MainActivity shows it. */
    private fun setInbox(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, hostStatus())
        server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
        server.respond(HEADLESS_LIST, ListHeadlessResponse.getDefaultInstance())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(NOTICES_LIST, NoticesListResponse.getDefaultInstance())
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            var open by remember { mutableStateOf(false) }
            if (open) {
                HostsView(client = client, onBack = { open = false })
            } else {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    onOpenHosts = { open = true },
                    now = { NOW },
                )
            }
        }
    }

    /** One pane, which names no host, so its head labels the serving host the Host.Status read names. */
    private fun servePane(server: FakeOrreryServer) {
        server.route(PANE_STREAM) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(
                    PaneProto.newBuilder()
                        .setId(PANE_ID)
                        .setSession("example-session")
                        .setWindow(1)
                        .setPane(2)
                        .setCwd("/home/user/tidepool")
                        .setCommand("claude")
                        .setState("done")
                        .setLabel("Tide table cache warm-up")
                        .setChanged(FIXTURE_CHANGED)
                        .setSeen(FIXTURE_SEEN)
                        .build(),
                )
                .build(),
        )
        server.respond(
            PANE_CONVERSATION,
            ConversationResponse.newBuilder()
                .setOrigin("synthetic origin")
                .setGoal(GOAL)
                .addItems(ConversationItemProto.newBuilder().setKind("reply").setText("synthetic reply").build())
                .build(),
        )
        server.respond(PANE_SEEN, MarkSeenResponse.getDefaultInstance())
        server.respond(HOST_STATUS, hostStatus())
    }

    private fun setPane(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                PaneScreen(PANE_ID, client, clock = { NOW })
            }
        }
    }

    /** One session whose first delta holds its state and goal, which names no host, and whose stream stays open. */
    private fun serveSession(server: FakeOrreryServer) {
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setGoal(GOAL)
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/tidepool")
                    .setLabel("Tide table cache warm-up")
                    .setState("done")
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN),
            )
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .addItems(ConversationItemProto.newBuilder().setKind("reply").setText("synthetic reply").build())
            .setState(state)
            .build()
        val requests = AtomicInteger()
        server.route(SESSION_STREAM) {
            if (requests.getAndIncrement() == 0) {
                Reply.Stream(listOf(delta), StreamEnd.Open)
            } else {
                Reply.Stream(emptyList(), StreamEnd.Open)
            }
        }
        server.respond(HOST_STATUS, hostStatus())
    }

    private fun setSession(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionScreen(SESSION_ID, client, clock = { NOW })
            }
        }
    }

    /** The head and its band are drawn, and no host meter is drawn anywhere on the screen. */
    private fun assertHeadDrawsNoHostMeters() {
        composeRule.onNodeWithTag(HEAD).assertIsDisplayed()
        composeRule.onNodeWithTag(BAND).assertIsDisplayed()
        assertEquals("no host meter is drawn", 0, countNodes(hostMeter))
    }

    @Test
    fun theInboxHeadsHostsActionOpensTheHostsViewListingEachHostsLoadMemoryAndReach() {
        FakeOrreryServer().use { server ->
            setInbox(server)
            composeRule.awaitUntil("the inbox head is drawn") { countNodes(hasTestTag("inbox-head")) == 1 }

            composeRule.tapInboxAction("open-hosts")

            composeRule.awaitUntil("the hosts view lists $SILENT") { countNodes(hasTestTag("host-row:$SILENT")) == 1 }
            // A reachable host shows its load and memory percents, rounded, and no unreachable mark.
            assertEquals(1, countNodes(inRow(SERVING, hasText("37%"))))
            assertEquals(1, countNodes(inRow(SERVING, hasText("59%"))))
            assertEquals(0, countNodes(inRow(SERVING, hasText("unreachable", substring = true))))
            assertEquals(1, countNodes(inRow(ANSWERING, hasText("85%"))))
            assertEquals(1, countNodes(inRow(ANSWERING, hasText("3%"))))
            // A host that does not answer shows the word unreachable and no numbers, whatever it reports.
            assertEquals(1, countNodes(inRow(SILENT, hasText("unreachable"))))
            assertEquals(0, countNodes(inRow(SILENT, hasText("%", substring = true))))
            assertEquals(0, countNodes(inRow(SILENT, hasTestTag(HOST_METERS))))
            // The meter matcher the head tests use finds the meters of the reachable hosts, so its zero counts mean something.
            assertTrue("the hosts view draws meters", countNodes(hostMeter) > 0)
        }
    }

    @Test
    fun aPaneHeadDrawsItsHostsLabelAndNoHostMeters() {
        FakeOrreryServer().use { server ->
            servePane(server)
            setPane(server)

            // The label appears once the Host.Status read has named the serving host, so the read has landed.
            composeRule.awaitExactlyOne(hasTestTag(HOST_LABEL) and hasAnyAncestor(hasTestTag(HEAD_TITLE)))
            composeRule.onNode(hasTestTag(HOST_LABEL) and hasAnyAncestor(hasTestTag(HEAD_TITLE))).assertTextEquals(SERVING)
            assertHeadDrawsNoHostMeters()
        }
    }

    @Test
    fun aSessionHeadDrawsItsHostsLabelAndNoHostMeters() {
        FakeOrreryServer().use { server ->
            serveSession(server)
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_LABEL) and hasAnyAncestor(hasTestTag(HEAD_TITLE)))
            composeRule.onNode(hasTestTag(HOST_LABEL) and hasAnyAncestor(hasTestTag(HEAD_TITLE))).assertTextEquals(SERVING)
            assertHeadDrawsNoHostMeters()
        }
    }
}
