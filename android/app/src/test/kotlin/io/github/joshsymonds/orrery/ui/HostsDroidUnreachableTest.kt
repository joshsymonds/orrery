package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry as HostEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// R4 end to end, on invented hosts with one that does not answer: a pane flagged unreachable, a headless session on that
// host, and a step on it. Each title row draws the unreachable mark, the grey dashed ring and the word, and never a
// stopped glyph. A pane's dock on such a host is its one line, `unreachable: <host> does not answer`, with no box, queue
// or control. A pane on a host that answers draws no mark and keeps its box. Checks read semantics only, and every screen
// runs on the fixed clock NOW.
@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class HostsDroidUnreachableTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val PANE_STREAM = "/orrery.v1.Panes/WatchConversation"
        const val PANE_CONVERSATION = "/orrery.v1.Panes/Conversation"
        const val PANE_SEEN = "/orrery.v1.Panes/MarkSeen"
        const val SESSION_STREAM = "/orrery.v1.Sessions/WatchConversation"
        const val PANE_ID = "%7"
        const val SESSION_ID = "fedcba9876543210fedcba9876543210"
        const val STEP_LABEL = "synthetic step"
        const val GOAL = "synthetic goal text"
        const val HEAD_TITLE = "head-title"
        const val HOST_LABEL = "host-label"
        const val HOST_UNREACHABLE = "host-unreachable"
        const val DOCK = "dock"
        const val DOCK_WORD = "dock-word"
        const val UNREACHABLE = "unreachable"

        // Invented hosts. The serving host answers, and the second does not.
        const val SERVING = "example-serving"
        const val SILENT = "example-silent"
    }

    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    /** The controls a dock offers an input, a queue or a control with: none of them on a dock that says unreachable. */
    private val dockControls = listOf("composer", "dock-tray", "dock-interrupt", "dock-resume", "dock-more")

    /** The labels of a pane's quick keys, which post keys to the pane, so are not offered on a host that does not answer. */
    private val quickKeyLabels = listOf("Esc", "Ctrl-C", "Enter", "←", "↑", "↓", "→")

    /** Any node that carries text, so a dock's lines can be counted. */
    private val textNode = SemanticsMatcher("has text") { it.config.contains(SemanticsProperties.Text) }

    /** The nodes under the node tagged [tag] that match [matcher], read from the unmerged tree. */
    private fun under(tag: String, matcher: SemanticsMatcher) =
        composeRule.onAllNodes(matcher and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true)

    /** The colour of each unreachable mark's ring under the nodes that match [scope], on its colour key. */
    private fun ringColours(scope: SemanticsMatcher): List<Color> =
        composeRule.onAllNodes(hasAnyAncestor(scope), useUnmergedTree = true)
            .fetchSemanticsNodes()
            .mapNotNull { it.config.getOrNull(HostRingColourKey) }

    private fun countTag(tag: String): Int =
        composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countText(text: String): Int =
        composeRule.onAllNodes(hasText(text), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun hostEntry(name: String, reachable: Boolean): HostEntryProto =
        HostEntryProto.newBuilder()
            .setName(name)
            .setLoadPercent(12.0)
            .setMemoryPercent(40.0)
            .setReachable(reachable)
            .build()

    /** The Host.Status the server answers: the serving host, and the two hosts, one of which does not answer. */
    private fun hostStatus(): HostStatusResponse =
        HostStatusResponse.newBuilder()
            .setHost(SERVING)
            .addHosts(hostEntry(SERVING, reachable = true))
            .addHosts(hostEntry(SILENT, reachable = false))
            .build()

    /**
     * One pane on [host], flagged unreachable or not, whose stream gives up so the screen polls it. A pane with an empty
     * [host] reads as the serving host, which its label shows once the Host.Status read has named it.
     */
    private fun servePane(server: FakeOrreryServer, host: String, unreachable: Boolean) {
        server.route(PANE_STREAM) {
            Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream"))
        }
        server.respond(
            PANES_LIST,
            PanesListResponse.newBuilder()
                .addPanes(
                    PaneProto.newBuilder()
                        .setId(PANE_ID)
                        .setHost(host)
                        .setSession("example-session")
                        .setWindow(1)
                        .setPane(2)
                        .setCwd("/home/user/lighthouse")
                        .setCommand("claude")
                        .setState("done")
                        .setLabel("Lamp dimmer")
                        .setChanged(NOW - 120)
                        .setSeen(NOW - 60)
                        .setUnreachable(unreachable)
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

    /** One headless session on [host], whose first delta holds its state and goal, and whose stream stays open. */
    private fun serveSession(server: FakeOrreryServer, host: String) {
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setGoal(GOAL)
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/lighthouse")
                    .setLabel("Lamp dimmer")
                    .setState("done")
                    .setChanged(NOW - 120)
                    .setSeen(NOW - 60)
                    .setHost(host),
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

    /** A step's session whose stream opens with no delta, and the Host.Status read the step's title row waits for. */
    private fun serveStep(server: FakeOrreryServer) {
        server.route(SESSION_STREAM) { Reply.Stream(emptyList(), StreamEnd.Open) }
        server.respond(HOST_STATUS, hostStatus())
    }

    /** The step screen of a step on [SILENT], which names its host itself, so the title row draws that host. */
    private fun setStep(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionScreen(
                    SESSION_ID,
                    client,
                    step = StepMode(held = false, label = STEP_LABEL, host = SILENT),
                    clock = { NOW },
                )
            }
        }
    }

    @Test
    fun aPaneFlaggedUnreachableDrawsTheMarkInItsTitleRowAndItsDockSaysOnlyUnreachable() {
        FakeOrreryServer().use { server ->
            servePane(server, host = SILENT, unreachable = true)
            setPane(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_UNREACHABLE) and hasAnyAncestor(hasTestTag(HEAD_TITLE)))
            assertEquals(listOf(OrreryTokens.Ink.three), ringColours(hasTestTag(HEAD_TITLE)))
            under(HEAD_TITLE, hasText(UNREACHABLE)).assertCountEquals(1)
            under(HEAD_TITLE, hasContentDescription("stopped")).assertCountEquals(0)

            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("unreachable: $SILENT does not answer")
            under(DOCK, textNode).assertCountEquals(1)
            dockControls.forEach { tag -> assertEquals("no $tag on the dock", 0, countTag(tag)) }
            quickKeyLabels.forEach { label -> assertEquals("no $label quick key", 0, countText(label)) }
        }
    }

    @Test
    fun aSessionOnAHostThatDoesNotAnswerDrawsTheMarkInItsTitleRowAndItsDockSaysUnreachable() {
        FakeOrreryServer().use { server ->
            serveSession(server, host = SILENT)
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_UNREACHABLE) and hasAnyAncestor(hasTestTag(HEAD_TITLE)))
            assertEquals(listOf(OrreryTokens.Ink.three), ringColours(hasTestTag(HEAD_TITLE)))
            under(HEAD_TITLE, hasText(UNREACHABLE)).assertCountEquals(1)
            under(HEAD_TITLE, hasContentDescription("stopped")).assertCountEquals(0)

            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("unreachable: $SILENT does not answer")
        }
    }

    @Test
    fun aStepOnAHostThatDoesNotAnswerDrawsTheMarkInItsTitleRowAndItsDockSaysUnreachable() {
        FakeOrreryServer().use { server ->
            serveStep(server)
            setStep(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_UNREACHABLE))
            assertEquals(listOf(OrreryTokens.Ink.three), ringColours(hasTestTag(HOST_UNREACHABLE)))
            under(HOST_UNREACHABLE, hasText(UNREACHABLE)).assertCountEquals(1)
            composeRule.onAllNodes(hasContentDescription("stopped"), useUnmergedTree = true).assertCountEquals(0)

            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("unreachable: $SILENT does not answer")
        }
    }

    @Test
    fun aPaneOnAHostThatAnswersDrawsNoMarkAndKeepsItsDockBox() {
        FakeOrreryServer().use { server ->
            // The pane names no host, so its label, and with it the host's reach, waits for the Host.Status read.
            servePane(server, host = "", unreachable = false)
            setPane(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_LABEL) and hasAnyAncestor(hasTestTag(HEAD_TITLE)))
            assertEquals(0, countTag(HOST_UNREACHABLE))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("done")
            assertEquals(1, countTag("composer"))
            assertEquals("the Esc quick key on a host that answers", 1, countText("Esc"))
        }
    }
}
