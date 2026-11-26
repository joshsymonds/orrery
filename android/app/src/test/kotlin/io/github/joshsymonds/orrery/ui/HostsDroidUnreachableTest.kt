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
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostEntry as HostEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PendingEvent
import io.github.joshsymonds.orrery.gen.orrery.v1.QueuedMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
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
// host, an epic and a step on it. Each title row draws the unreachable mark, the grey dashed ring and the word, and never a
// stopped glyph. A pane's, session's or step's dock on such a host is its one line, `unreachable: <host> does not answer`,
// with no box, queue or control, though each is working with messages queued, so on a host that answers the same dock
// offers its queue, Interrupt and More stop. A pane on a host that answers draws no mark. The inbox, served the same answers, draws the
// pane, session and step rows with the grey ring and the word, and the epic's host with the mark. Checks read semantics
// only, and every screen runs on the fixed clock NOW.
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
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val DESIGNS_LIST = "/orrery.v1.Designs/List"
        const val CONVO_ID = "synthetic-convo"
        const val INBOX_EPIC_ID = "lighthouse/silent-epic"
        const val SILENT_STEP_ID = "silent-step"
        const val SERVING_STEP_ID = "serving-step"
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

        // The inbox's rows, as the inbox keys them, on the pane, session, epic and step above.
        const val INBOX_PANE_ROW = "convo:pane:$SILENT:$PANE_ID"
        const val INBOX_SESSION_ROW = "convo:session:$SESSION_ID"
        const val INBOX_EPIC_ROW = "hung:epic:$INBOX_EPIC_ID"
        const val INBOX_STEP_ROW = "step:$SILENT_STEP_ID"
    }

    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    /** The controls a dock offers an input, a queue or a control with: none of them on a dock that says unreachable. */
    private val dockControls = listOf("composer", "dock-tray", "dock-waits", "dock-interrupt", "dock-resume", "dock-more")

    /** Asserts the dock is the one line `unreachable: [host] does not answer`, with no input, queue or control node. */
    private fun assertDockSaysOnlyUnreachable(host: String) {
        composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("unreachable: $host does not answer")
        under(DOCK, textNode).assertCountEquals(1)
        dockControls.forEach { tag -> assertEquals("no $tag on the dock", 0, countTag(tag)) }
    }

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
     * One working pane on [host], flagged unreachable or not, with a message waiting to be sent, whose stream gives up so
     * the screen polls it. A pane with an empty [host] reads as the serving host, which its label shows once the
     * Host.Status read has named it.
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
                        .setState("working")
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
                .addWaiting(WaitingMessage.newBuilder().setText("synthetic waiting message").setTime(NOW - 10).build())
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

    /**
     * One working headless session on [host], whose first delta holds its state and goal, a queued message and an event
     * that waits until the owner speaks, and whose stream stays open.
     */
    private fun serveSession(server: FakeOrreryServer, host: String) {
        val state = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setGoal(GOAL)
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/lighthouse")
                    .setLabel("Lamp dimmer")
                    .setState("working")
                    .setChanged(NOW - 120)
                    .setSeen(NOW - 60)
                    .setHost(host),
            )
            .addPending(PendingEvent.newBuilder().setId("synthetic-event").setKind("question").setWaits("speak"))
            .build()
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .addItems(ConversationItemProto.newBuilder().setKind("reply").setText("synthetic reply").build())
            .setState(state)
            .addQueued(
                QueuedMessage.newBuilder()
                    .setPromptId("synthetic-prompt")
                    .setText("synthetic queued message")
                    .setState("queued")
                    .setWaits("turn"),
            )
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

    /** The running step screen of a step on [host], which names its host itself, so the title row draws that host. */
    private fun setStep(server: FakeOrreryServer, host: String) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionScreen(
                    SESSION_ID,
                    client,
                    step = StepMode(held = false, label = STEP_LABEL, host = host),
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

            assertDockSaysOnlyUnreachable(SILENT)
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

            assertDockSaysOnlyUnreachable(SILENT)
        }
    }

    @Test
    fun aStepOnAHostThatDoesNotAnswerDrawsTheMarkInItsTitleRowAndItsDockSaysUnreachable() {
        FakeOrreryServer().use { server ->
            serveStep(server)
            setStep(server, SILENT)

            composeRule.awaitExactlyOne(hasTestTag(HOST_UNREACHABLE))
            assertEquals(listOf(OrreryTokens.Ink.three), ringColours(hasTestTag(HOST_UNREACHABLE)))
            under(HOST_UNREACHABLE, hasText(UNREACHABLE)).assertCountEquals(1)
            composeRule.onAllNodes(hasContentDescription("stopped"), useUnmergedTree = true).assertCountEquals(0)

            assertDockSaysOnlyUnreachable(SILENT)
        }
    }

    @Test
    fun aSessionOnAHostThatAnswersOffersItsQueueInterruptAndMoreStop() {
        FakeOrreryServer().use { server ->
            serveSession(server, host = SERVING)
            setSession(server)

            composeRule.awaitExactlyOne(hasTestTag("dock-tray"))
            assertEquals(0, countTag(HOST_UNREACHABLE))
            listOf("composer", "dock-waits", "dock-interrupt", "dock-more").forEach { tag ->
                assertEquals("the $tag on a host that answers", 1, countTag(tag))
            }
        }
    }

    @Test
    fun aStepOnAHostThatAnswersOffersInterruptAndMoreStop() {
        FakeOrreryServer().use { server ->
            serveStep(server)
            setStep(server, SERVING)

            composeRule.awaitExactlyOne(hasTestTag("dock-more"))
            assertEquals(0, countTag(HOST_UNREACHABLE))
            assertEquals(1, countTag("dock-interrupt"))
        }
    }

    /** A live epic on [SILENT] with no steps, so the epic's host is the only host mark its screen draws: its title row's. */
    private fun serveEpic(server: FakeOrreryServer) {
        val epic = EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(INBOX_EPIC_ID)
                    .setRepository("lighthouse")
                    .setSlug("silent-epic")
                    .setChanged(NOW - 30)
                    .setLive(true)
                    .setSource("orreryd")
                    .setHost(SILENT),
            )
            .build()
        server.respond(EPICS_GET, epic)
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
        server.respond(HOST_STATUS, hostStatus())
    }

    /** Mounts the epic screen of [INBOX_EPIC_ID] on a client of [server], on the fixed clock. */
    private fun setEpic(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                EpicScreen(
                    client = client,
                    epicId = INBOX_EPIC_ID,
                    onBack = {},
                    onOpenStep = {},
                    onOpenContract = {},
                    onTalk = {},
                    listedSteerer = { null },
                    now = { NOW },
                )
            }
        }
    }

    @Test
    fun anEpicOnAHostThatDoesNotAnswerDrawsTheMarkInItsTitleRowNeverStopped() {
        FakeOrreryServer().use { server ->
            serveEpic(server)
            setEpic(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_UNREACHABLE))
            assertEquals(listOf(OrreryTokens.Ink.three), ringColours(hasTestTag(HOST_UNREACHABLE)))
            under(HOST_UNREACHABLE, hasText(UNREACHABLE)).assertCountEquals(1)
            composeRule.onAllNodes(hasContentDescription("stopped"), useUnmergedTree = true).assertCountEquals(0)
        }
    }

    @Test
    fun aPaneOnAHostThatAnswersDrawsNoMarkAndOffersItsBoxQueueAndInterrupt() {
        FakeOrreryServer().use { server ->
            // The pane names no host, so its label, and with it the host's reach, waits for the Host.Status read.
            servePane(server, host = "", unreachable = false)
            setPane(server)

            composeRule.awaitExactlyOne(hasTestTag(HOST_LABEL) and hasAnyAncestor(hasTestTag(HEAD_TITLE)))
            composeRule.awaitExactlyOne(hasTestTag("dock-tray"))
            assertEquals(0, countTag(HOST_UNREACHABLE))
            composeRule.onNodeWithTag(DOCK_WORD).assertTextEquals("working")
            listOf("composer", "dock-interrupt").forEach { tag ->
                assertEquals("the $tag on a host that answers", 1, countTag(tag))
            }
            assertEquals("the Esc quick key on a host that answers", 1, countText("Esc"))
        }
    }

    /** The grey or state colour of each glyph named [word] under the node tagged [tag], on its colour key. */
    private fun glyphColours(tag: String, word: String): List<Color> =
        under(tag, hasContentDescription(word)).fetchSemanticsNodes().mapNotNull { it.config.getOrNull(GlyphColourKey) }

    /** A pane on [host] that the inbox lists, flagged unreachable or not. */
    private fun inboxPane(host: String, unreachable: Boolean): PaneProto =
        PaneProto.newBuilder()
            .setId(PANE_ID)
            .setHost(host)
            .setSession("example-session")
            .setWindow(1)
            .setPane(2)
            .setCwd("/home/user/lighthouse")
            .setCommand("claude")
            .setClaudeSession("synthetic-claude")
            .setOrreryId(CONVO_ID)
            .setState("working")
            .setLabel("Lamp dimmer")
            .setChanged(NOW - 120)
            .setSeen(0L)
            .setUnreachable(unreachable)
            .build()

    /** A headless session on [host] the inbox lists, still working so the inbox keeps it in view. */
    private fun inboxSession(host: String): HeadlessSession =
        HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir("/home/user/lighthouse")
            .setLabel("Lamp dimmer")
            .setState("working")
            .setChanged(NOW - 120)
            .setSeen(0L)
            .setHost(host)
            .build()

    /** A running step on [host], as the epic lists it. */
    private fun epicStep(id: String, host: String): EpicStep =
        EpicStep.newBuilder()
            .setId(id)
            .setTask("synthetic-task")
            .setAgent("synthetic-agent")
            .setStatus("working")
            .setStarted(NOW - 60)
            .setDoing("Synthetic work.")
            .setHost(host)
            .build()

    /** A live epic on [SILENT], with one running step on [SILENT] and one on [SERVING]. */
    private fun inboxEpic(): Epic =
        Epic.newBuilder()
            .setId(INBOX_EPIC_ID)
            .setRepository("lighthouse")
            .setSlug("silent-epic")
            .setChanged(NOW - 30)
            .setLive(true)
            .setSource("orreryd")
            .setHost(SILENT)
            .addRunningSteps(epicStep(SILENT_STEP_ID, SILENT))
            .addRunningSteps(epicStep(SERVING_STEP_ID, SERVING))
            .build()

    /**
     * The inbox's reads, served from the fake server: an unreachable pane, a headless session, a live epic and a step on
     * [SILENT], and the host status that says [SILENT] does not answer. The lists the inbox reads with no rows answer empty.
     */
    private fun serveInbox(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, hostStatus())
        server.respond(PANES_LIST, PanesListResponse.newBuilder().addPanes(inboxPane(SILENT, unreachable = true)).build())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.newBuilder().addSessions(inboxSession(SILENT)).build())
        server.respond(SESSIONS_LIST, ListResponse.getDefaultInstance())
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(inboxEpic()).build())
        server.respond(CONTRACTS_LIST, ContractsListResponse.getDefaultInstance())
        server.respond(DESIGNS_LIST, DesignsListResponse.getDefaultInstance())
    }

    /** Mounts the inbox, as the home page draws it, on a client of [server]. */
    private fun setInbox(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    now = { NOW },
                )
            }
        }
    }

    @Test
    fun theInboxRowsOnAHostThatDoesNotAnswerDrawTheGreyRingAndTheWordNeverStopped() {
        FakeOrreryServer().use { server ->
            serveInbox(server)
            setInbox(server)

            composeRule.awaitExactlyOne(hasTestTag(INBOX_PANE_ROW))
            // A pane, a session and a step each carry the unreachable state glyph in the grey ink, and never a stopped one.
            listOf(INBOX_PANE_ROW, INBOX_SESSION_ROW, INBOX_STEP_ROW).forEach { row ->
                assertEquals("$row glyph colour", listOf(OrreryTokens.Ink.three), glyphColours(row, UNREACHABLE))
                under(row, hasContentDescription("stopped")).assertCountEquals(0)
                under(row, hasText(UNREACHABLE)).assertCountEquals(1)
            }
            // An epic has no state glyph: its host, which does not answer, carries the grey dashed ring and the word.
            assertEquals(listOf(OrreryTokens.Ink.three), ringColours(hasTestTag(INBOX_EPIC_ROW)))
            under(INBOX_EPIC_ROW, hasTestTag(HOST_UNREACHABLE)).assertCountEquals(1)
            under(INBOX_EPIC_ROW, hasContentDescription("stopped")).assertCountEquals(0)
            under(INBOX_EPIC_ROW, hasText(UNREACHABLE)).assertCountEquals(1)
        }
    }
}
