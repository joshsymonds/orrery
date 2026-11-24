package io.github.joshsymonds.orrery.ui

import androidx.activity.OnBackPressedDispatcherOwner
import androidx.activity.compose.LocalOnBackPressedDispatcherOwner
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Session as SessionProto
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CountDownLatch
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
class InboxTreeScreenTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val HOST = "example-host"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val EPIC_ID = "example-repo/example-epic"
        const val EPIC_REPOSITORY = "example-repo"
        const val EPIC_SLUG = "example-epic"
        const val CONTRACT_ID = "example-repo/example-contract"
        const val ALPHA_SESSION = "00000000000000000000000000000001"
        const val BETA_SESSION = "00000000000000000000000000000002"
        const val IDLE_SESSION = "00000000000000000000000000000003"
        const val ENDED_SESSION = "00000000000000000000000000000004"
        const val ALPHA_KEY = "|$ALPHA"
        const val STEP_ID = "example-step"
        const val EMPTY_HINT = "Start a session in a directory and it shows here, grouped by that directory."
        const val SEARCH_LABEL = "Search sessions and epics"
        const val SEARCH_LOOKS_AT = "Search looks at titles, directories, hosts, states, and the names of epics and contracts."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var openedPane: Pane? = null
    private var openedEpic: String? = null
    private var backOwner: OnBackPressedDispatcherOwner? = null

    private fun projectTag(dir: String): String = "project:|$dir"

    private fun convoTag(paneId: String): String = "convo:pane:$HOST:$paneId"

    private fun sessionTag(sessionId: String): String = "convo:session:$sessionId"

    private fun hungTag(key: String): String = "hung:$key"

    private fun pane(
        id: String,
        cwd: String,
        state: String,
        changed: Long,
        seen: Long,
        newestReply: Long,
        orreryId: String = "",
        question: String = "",
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(HOST)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState(state)
        .setSummary("Synthetic summary.")
        .setLabel("label-$id")
        .setChanged(changed)
        .setSeen(seen)
        .setQuestion(question)
        .setQuestionHash("")
        .setNewestReply(newestReply)
        .setOrreryId(orreryId)
        .build()

    private fun panesOf(vararg panes: PaneProto): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(panes.toList()).build()

    private fun headless(id: String, dir: String, state: String, changed: Long, seen: Long): HeadlessSession =
        HeadlessSession.newBuilder()
            .setId(id)
            .setDir(dir)
            .setLabel("synthetic-$state")
            .setState(state)
            .setChanged(changed)
            .setSeen(seen)
            .setSummary("Synthetic $state session.")
            .setNewestReply(changed)
            .build()

    private fun liveEpic(steering: String, vararg steps: EpicStep): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setEndedSteps(0)
        .setSteering(steering)
        .addAllRunningSteps(steps.toList())
        .build()

    private fun runningStep(id: String): EpicStep = EpicStep.newBuilder()
        .setId(id)
        .setTask("synthetic-task")
        .setAgent("synthetic-agent")
        .setEnd("")
        .setStatus("working")
        .setHeld(false)
        .setStarted(NOW - 60)
        .setEnded(0L)
        .setDoing("Synthetic work.")
        .setReason("")
        .build()

    private fun openQuestion(): EpicQuestion = EpicQuestion.newBuilder()
        .setId("Q1")
        .setText("Synthetic question text?")
        .setTurn(1L)
        .setAsked(NOW - 120L)
        .build()

    private fun contract(id: String, state: String, conversation: String, repository: String): Contract =
        Contract.newBuilder()
            .setId(id)
            .setRepository(repository)
            .setTitle("Synthetic contract title")
            .setCare("serious")
            .setState(state)
            .setVersion(1L)
            .setOpenDecisions(0L)
            .setConversation(conversation)
            .setCreated(NOW - 600)
            .setChanged(NOW - 600)
            .build()

    /** The host the panes come from is the serving host, and no epics are listed, unless a test says otherwise. */
    private fun serveDefaults(server: FakeOrreryServer) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
    }

    /** Whether the screen is in the composition: a test takes it out and puts it back to leave the screen and return. */
    private var screenShown by mutableStateOf(true)

    private fun setScreen(server: FakeOrreryServer, state: PaneListState = PaneListState()) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            backOwner = LocalOnBackPressedDispatcherOwner.current
            if (screenShown) {
                PaneListScreen(
                    client = client,
                    onOpenPane = { openedPane = it },
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    onOpenEpic = { openedEpic = it },
                    now = { NOW },
                    state = state,
                )
            }
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    private fun awaitText(text: String) {
        awaitNode(hasText(text))
    }

    private fun nodeTop(matcher: SemanticsMatcher): Float =
        composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot.top

    private fun requestsTo(server: FakeOrreryServer, path: String): Int = server.requests.count { it.rawPath == path }

    /**
     * Two projects. In alpha, %1 waits on a question and is unseen. In beta, %2 is read and steers an epic with an open
     * question, so it waits through that epic; %3 is read and does not wait.
     */
    private fun servePanesAndEpic(server: FakeOrreryServer) {
        serveDefaults(server)
        server.respond(
            PANES_LIST,
            panesOf(
                pane("%1", ALPHA, "waiting", NOW - 60, NOW - 120, NOW - 60, ALPHA_SESSION, "Ship the build?"),
                pane("%2", BETA, "working", NOW - 90, NOW - 90, NOW - 90, BETA_SESSION),
                pane("%3", BETA, "idle", NOW - 7_200, NOW - 7_200, NOW - 7_200, IDLE_SESSION),
            ),
        )
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic(BETA_SESSION)).build())
        server.respond(
            EPICS_GET,
            EpicsGetResponse.newBuilder().setEpic(liveEpic(BETA_SESSION)).addQuestions(openQuestion()).build(),
        )
    }

    @Test
    fun projectsDrawAsCardsWithTheirConversations() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(
                PANES_LIST,
                panesOf(
                    pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30),
                    pane("%2", BETA, "idle", NOW - 7_200, NOW - 7_200, NOW - 7_200),
                ),
            )
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%1")))

            assertEquals(1, countNodes(hasTestTag(projectTag(ALPHA))))
            assertEquals(1, countNodes(hasTestTag(projectTag(BETA))))
            assertEquals(1, countNodes(hasText("~/alpha") and hasAnyAncestor(hasTestTag(projectTag(ALPHA)))))
            assertEquals(1, countNodes(hasTestTag(convoTag("%1")) and hasAnyAncestor(hasTestTag(projectTag(ALPHA)))))
            assertEquals(1, countNodes(hasTestTag(convoTag("%2")) and hasAnyAncestor(hasTestTag(projectTag(BETA)))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%2")) and hasAnyAncestor(hasTestTag(projectTag(ALPHA)))))
        }
    }

    @Test
    fun anEpicHangsUnderTheConversationItSteers() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(
                PANES_LIST,
                panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)),
            )
            server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic(ALPHA_SESSION)).build())
            setScreen(server)
            awaitNode(hasTestTag(hungTag("epic:$EPIC_ID")))

            val hung = nodeTop(hasTestTag(hungTag("epic:$EPIC_ID")))
            val convo = nodeTop(hasTestTag(convoTag("%1")))
            assertEquals(1, countNodes(hasTestTag(hungTag("epic:$EPIC_ID")) and hasAnyAncestor(hasTestTag(projectTag(ALPHA)))))
            assertTrue("the epic hangs below its conversation", convo < hung)
            assertEquals("the epic draws once", 1, countNodes(hasTestTag(hungTag("epic:$EPIC_ID"))))
        }
    }

    @Test
    fun aProposedContractHangsUnderItsConversation() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(
                PANES_LIST,
                panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)),
            )
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(contract(CONTRACT_ID, "proposed", ALPHA_SESSION, ALPHA))
                    .build(),
            )
            setScreen(server)
            awaitNode(hasTestTag(hungTag("contract:$CONTRACT_ID")))

            val hung = nodeTop(hasTestTag(hungTag("contract:$CONTRACT_ID")))
            val convo = nodeTop(hasTestTag(convoTag("%1")))
            assertEquals(
                1,
                countNodes(hasTestTag(hungTag("contract:$CONTRACT_ID")) and hasAnyAncestor(hasTestTag(projectTag(ALPHA)))),
            )
            assertTrue("the contract hangs below its conversation", convo < hung)
        }
    }

    @Test
    fun chipsCountWhatWaitsAndWhatIsNew() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)

            // %1 waits, and %2 waits through its epic's question: two waiting things. Only %1 is unseen.
            awaitText("2 wait on you")
            awaitText("1 new")
        }
    }

    @Test
    fun waitingChipNarrowsTheTreeAndPressingItAgainReturnsIt() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            composeRule.onNodeWithText("2 wait on you").performClick()

            assertEquals("the idle conversation leaves the tree", 0, countNodes(hasTestTag(convoTag("%3"))))
            assertEquals(1, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(1, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals("what waits under %2 stays", 1, countNodes(hasTestTag(hungTag("epic:$EPIC_ID"))))

            composeRule.onNodeWithText("2 wait on you").performClick()

            assertEquals(1, countNodes(hasTestTag(convoTag("%3"))))
        }
    }

    @Test
    fun newChipNarrowsToUnseenRowsWithNothingHungUnderThem() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%2")))

            composeRule.onNodeWithText("1 new").performClick()

            assertEquals(1, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals("a read row leaves the tree", 0, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals("unseen rows hang nothing", 0, countNodes(hasTestTag(hungTag("epic:$EPIC_ID"))))

            composeRule.onNodeWithText("1 new").performClick()

            assertEquals(1, countNodes(hasTestTag(convoTag("%2"))))
        }
    }

    @Test
    fun foldingAProjectHidesItsRowsAndItsHeadCountsWhatWaits() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(
                PANES_LIST,
                panesOf(
                    pane("%1", ALPHA, "waiting", NOW - 60, NOW - 120, NOW - 60, ALPHA_SESSION, "Ship the build?"),
                    pane("%2", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, BETA_SESSION),
                ),
            )
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%1")))

            composeRule.onNodeWithTag("fold:|$ALPHA").performClick()

            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals(1, countNodes(hasText("1 waiting")))

            composeRule.onNodeWithTag("fold:|$ALPHA").performClick()

            assertEquals(1, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(0, countNodes(hasText("1 waiting")))
        }
    }

    @Test
    fun endedLineOpensToTheEndedConversations() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(
                PANES_LIST,
                panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)),
            )
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder()
                    .addSessions(headless(ENDED_SESSION, ALPHA, "stopped", NOW - 900, 0))
                    .build(),
            )
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%1")))

            assertEquals(1, countNodes(hasText("1 ended")))
            assertEquals(0, countNodes(hasTestTag(sessionTag(ENDED_SESSION))))

            composeRule.onNodeWithTag("ended:$ALPHA_KEY").performClick()

            assertEquals(1, countNodes(hasTestTag(sessionTag(ENDED_SESSION))))

            composeRule.onNodeWithTag("ended:$ALPHA_KEY").performClick()

            assertEquals(0, countNodes(hasTestTag(sessionTag(ENDED_SESSION))))
        }
    }

    @Test
    fun loadingShowsBeforeTheFirstAnswer() {
        FakeOrreryServer().use { server ->
            val gate = CountDownLatch(1)
            try {
                serveDefaults(server)
                server.route(PANES_LIST) {
                    gate.await()
                    Reply.Message(panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)), 0L)
                }
                setScreen(server)

                composeRule.waitForIdle()
                composeRule.onNodeWithText("loading…").assertExists()
                assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))
            } finally {
                gate.countDown()
            }

            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(0, countNodes(hasText("loading…")))
        }
    }

    @Test
    fun emptyTreeSaysNothingIsRunningAndNewOpensThePicker() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            setScreen(server)

            awaitText("Nothing is running.")
            composeRule.onNodeWithText(EMPTY_HINT).assertExists()
            composeRule.onNodeWithTag("empty-new").performClick()

            awaitNode(hasTestTag("picker-text"))
        }
    }

    @Test
    fun failedPanesReadShowsTheServerDidNotAnswerAndTryNowReadsAgain() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            serveDefaults(server)
            server.fail(PANES_LIST, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitFailedRead(server, PANES_LIST)
            composeRule.mainClock.advanceTimeByFrame()

            composeRule.onNodeWithText("The server did not answer.").assertExists()
            composeRule.onNodeWithText("Trying again…").assertExists()
            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))

            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)))
            composeRule.onNodeWithText("Try now").performClick()
            composeRule.mainClock.advanceTimeByFrame()

            // The clock has moved only by frames, so the fixed delay has not run: the read is Try now's own.
            composeRule.awaitUntil("the read Try now asked for") {
                Quiescence.inFlight() == null && requestsTo(server, PANES_LIST) == 2
            }
            composeRule.mainClock.advanceTimeByFrame()
            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(0, countNodes(hasText("The server did not answer.")))
        }
    }

    @Test
    fun failedEpicsReadShowsTheServerDidNotAnswer() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)))
            server.fail(EPICS_LIST, Code.INTERNAL_ERROR, "boom")
            setScreen(server)

            awaitText("The server did not answer.")
            composeRule.onNodeWithText("Trying again…").assertExists()
            assertEquals("the tree is not drawn beside the error", 0, countNodes(hasTestTag(convoTag("%1"))))
            composeRule.onNodeWithText("Try now").assertExists()
        }
    }

    @Test
    fun failedEpicsReadTryNowReadsAgainAndDrawsTheTree() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)))
            server.fail(EPICS_LIST, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitFailedRead(server, EPICS_LIST)
            composeRule.mainClock.advanceTimeByFrame()
            composeRule.onNodeWithText("The server did not answer.").assertExists()

            server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
            composeRule.onNodeWithText("Try now").performClick()
            composeRule.mainClock.advanceTimeByFrame()

            composeRule.awaitUntil("the read Try now asked for") {
                Quiescence.inFlight() == null && requestsTo(server, EPICS_LIST) == 2
            }
            composeRule.mainClock.advanceTimeByFrame()
            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(0, countNodes(hasText("The server did not answer.")))
        }
    }

    @Test
    fun aTapOnAnEpicOpensIt() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic(ALPHA_SESSION)).build())
            setScreen(server)
            awaitNode(hasTestTag(hungTag("epic:$EPIC_ID")))

            composeRule.onNodeWithTag(hungTag("epic:$EPIC_ID")).performClick()

            assertEquals(EPIC_ID, openedEpic)
        }
    }

    @Test
    fun aTapOnAStepOpensItsEpic() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder().addEpics(liveEpic(ALPHA_SESSION, runningStep(STEP_ID))).build(),
            )
            setScreen(server)
            awaitNode(hasTestTag("step:$STEP_ID"))

            composeRule.onNodeWithTag("step:$STEP_ID").performClick()

            assertEquals(EPIC_ID, openedEpic)
        }
    }

    @Test
    fun aHeldContractSurvivesComingBackWhileContractsListFails() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(contract(CONTRACT_ID, "proposed", ALPHA_SESSION, ALPHA))
                    .build(),
            )
            val state = PaneListState()
            setScreen(server, state)
            awaitNode(hasTestTag(hungTag("contract:$CONTRACT_ID")))

            composeRule.runOnIdle { screenShown = false }
            server.fail(CONTRACTS_LIST, Code.INTERNAL_ERROR, "boom")
            composeRule.runOnIdle { screenShown = true }

            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals("the contracts were read again", 2, requestsTo(server, CONTRACTS_LIST))
            assertEquals(1, countNodes(hasTestTag(hungTag("contract:$CONTRACT_ID"))))
            assertEquals(0, countNodes(hasText("The server did not answer.")))
        }
    }

    @Test
    fun aFailedContractsReadWithNothingHeldDrawsTheTreeWithNoErrorLine() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.fail(CONTRACTS_LIST, Code.INTERNAL_ERROR, "boom")
            setScreen(server)

            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(0, countNodes(hasText("The server did not answer.")))
        }
    }

    @Test
    fun aHeldHostSurvivesComingBackWhileHostStatusFails() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            val state = PaneListState()
            setScreen(server, state)
            awaitNode(hasTestTag(projectTag(ALPHA)))

            composeRule.runOnIdle { screenShown = false }
            server.fail(HOST_STATUS, Code.INTERNAL_ERROR, "boom")
            composeRule.runOnIdle { screenShown = true }

            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(1, countNodes(hasTestTag(projectTag(ALPHA))))
        }
    }

    @Test
    fun failedReadTriesAgainOnlyAfterTheDelay() {
        FakeOrreryServer().use { server ->
            composeRule.mainClock.autoAdvance = false
            serveDefaults(server)
            server.fail(PANES_LIST, Code.INTERNAL_ERROR, "boom")
            setScreen(server)
            awaitFailedRead(server, PANES_LIST)

            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)))
            composeRule.mainClock.advanceTimeBy(RETRY_DELAY_MILLIS - 1, ignoreFrameDuration = true)
            composeRule.waitForIdle()
            // A read the retry started is in flight until it is answered, so once nothing is in flight, a read that
            // had begun would have been counted.
            composeRule.awaitUntil("the screen is quiet") { Quiescence.inFlight() == null }
            assertEquals("no read before the delay", 1, requestsTo(server, PANES_LIST))

            composeRule.mainClock.advanceTimeBy(1L, ignoreFrameDuration = true)
            composeRule.mainClock.advanceTimeByFrame()
            composeRule.awaitUntil("the retry reads") { Quiescence.inFlight() == null && requestsTo(server, PANES_LIST) == 2 }
            composeRule.mainClock.advanceTimeByFrame()
            awaitNode(hasTestTag(convoTag("%1")))
        }
    }

    /** Waits until the first read has failed and its failure has been handled, with the clock stopped. */
    private fun awaitFailedRead(server: FakeOrreryServer, path: String) {
        composeRule.awaitUntil("the first read failed") {
            composeRule.waitForIdle()
            Quiescence.inFlight() == null && requestsTo(server, path) == 1
        }
        // The answer's continuation is queued on the main thread once the read finishes; running it writes the failure.
        composeRule.waitForIdle()
    }

    @Test
    fun headIsTheOrreryMarkSearchTheBellPoolsSettingsAndNewWithNoFilter() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30)))
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%1")))

            assertEquals(1, countNodes(hasTestTag("title-mark")))
            assertEquals(1, countNodes(hasText("orrery")))
            assertEquals(1, countNodes(hasContentDescription(SEARCH_LABEL) and hasTestTag("open-search")))
            assertEquals(1, countNodes(hasContentDescription("Notices") and hasTestTag("open-notices")))
            assertEquals(1, countNodes(hasText("New") and hasAnyAncestor(hasTestTag("picker-new"))))
            // Pools and Settings are in the bar, or in More when the bar folded them.
            if (countNodes(hasTestTag("inbox-more")) == 1) composeRule.onNodeWithTag("inbox-more").performClick()
            assertEquals(1, countNodes(hasTestTag("open-pools")))
            assertEquals(1, countNodes(hasTestTag("open-settings")))
            for (tag in listOf("filter-all", "filter-new", "filter-waiting")) {
                assertEquals("$tag is gone", 0, countNodes(hasTestTag(tag)))
            }
            assertEquals(0, countNodes(hasText("Needs you")))
        }
    }

    private fun openSearch() {
        composeRule.onNodeWithTag("open-search").performClick()
    }

    private fun typeSearch(words: String) {
        composeRule.onNodeWithTag("search-field").performTextInput(words)
    }

    @Test
    fun theSearchControlOpensTheFieldUnderTheHead() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))
            assertEquals(0, countNodes(hasTestTag("search-field")))

            openSearch()

            assertEquals(1, countNodes(hasTestTag("search-field") and hasContentDescription(SEARCH_LABEL)))
            assertEquals(1, countNodes(hasText(SEARCH_LABEL)))
        }
    }

    @Test
    fun typingAProjectsWordsKeepsItsRowsAndDropsTheRest() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            openSearch()
            typeSearch("alpha")

            assertEquals(1, countNodes(hasTestTag(projectTag(ALPHA))))
            assertEquals(1, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%3"))))
            assertEquals(0, countNodes(hasTestTag(projectTag(BETA))))
        }
    }

    @Test
    fun typingAnEpicsWordsKeepsItsConversationAndTheEpicAndDropsTheRest() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            openSearch()
            typeSearch(EPIC_SLUG)

            assertEquals(1, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals(1, countNodes(hasTestTag(hungTag("epic:$EPIC_ID"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%3"))))
        }
    }

    @Test
    fun typingAContractsWordsKeepsItsConversationAndTheContractAndDropsTheRest() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(
                PANES_LIST,
                panesOf(
                    pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION),
                    pane("%2", BETA, "idle", NOW - 7_200, NOW - 7_200, NOW - 7_200, BETA_SESSION),
                ),
            )
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(contract(CONTRACT_ID, "proposed", ALPHA_SESSION, ALPHA))
                    .build(),
            )
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%2")))

            openSearch()
            typeSearch("contract")

            assertEquals(1, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(1, countNodes(hasTestTag(hungTag("contract:$CONTRACT_ID"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))
        }
    }

    @Test
    fun noMatchDrawsTheWordsAndWhatSearchLooksAt() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            openSearch()
            typeSearch("zzz")

            assertEquals(1, countNodes(hasText("Nothing matches “zzz”.")))
            assertEquals(1, countNodes(hasText(SEARCH_LOOKS_AT)))
            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))
            assertEquals(0, countNodes(hasText("Nothing is running.")))
        }
    }

    @Test
    fun theClearControlClearsTheWordsAndClosesTheField() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            openSearch()
            typeSearch("alpha")
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))

            composeRule.onNodeWithTag("clear-search").performClick()

            assertEquals(0, countNodes(hasTestTag("search-field")))
            assertEquals(1, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals(1, countNodes(hasTestTag(convoTag("%3"))))
        }
    }

    @Test
    fun theSystemBackClearsTheWordsAndClosesTheField() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%3")))

            openSearch()
            typeSearch("alpha")
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))

            composeRule.runOnIdle { checkNotNull(backOwner).onBackPressedDispatcher.onBackPressed() }

            assertEquals(0, countNodes(hasTestTag("search-field")))
            assertEquals(1, countNodes(hasTestTag(convoTag("%2"))))
        }
    }

    @Test
    fun theSearchWordsSurviveLeavingTheScreenAndComingBack() {
        FakeOrreryServer().use { server ->
            servePanesAndEpic(server)
            val state = PaneListState()
            setScreen(server, state)
            awaitNode(hasTestTag(convoTag("%3")))

            openSearch()
            typeSearch("alpha")

            composeRule.runOnIdle { screenShown = false }
            composeRule.runOnIdle { screenShown = true }

            awaitNode(hasTestTag(convoTag("%1")))
            composeRule.onNodeWithTag("search-field", useUnmergedTree = true).assertTextEquals("alpha")
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))
        }
    }

    @Test
    fun openingAContractWithNoEpicOpensNothing() {
        FakeOrreryServer().use { server ->
            serveDefaults(server)
            server.respond(PANES_LIST, panesOf(pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION)))
            server.respond(
                CONTRACTS_LIST,
                ContractsListResponse.newBuilder()
                    .addContracts(contract(CONTRACT_ID, "proposed", "", ALPHA))
                    .build(),
            )
            setScreen(server)
            awaitNode(hasTestTag(hungTag("contract:$CONTRACT_ID")))

            composeRule.onNodeWithTag(hungTag("contract:$CONTRACT_ID")).performClick()

            assertNull(openedEpic)
            assertNull(openedPane)
        }
    }

    /** Two conversations: %1 is archived, with a live epic it steers; %2 is not archived. */
    private fun serveArchivedAndListed(server: FakeOrreryServer) {
        serveDefaults(server)
        server.respond(
            PANES_LIST,
            panesOf(
                pane("%1", ALPHA, "working", NOW - 30, NOW - 10, NOW - 30, ALPHA_SESSION),
                pane("%2", BETA, "working", NOW - 40, NOW - 10, NOW - 40, BETA_SESSION),
            ),
        )
        server.respond(
            SESSIONS_LIST,
            ListResponse.newBuilder().addSessions(SessionProto.newBuilder().setId(ALPHA_SESSION).setArchived(true)).build(),
        )
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addEpics(liveEpic(ALPHA_SESSION)).build())
        server.respond(EPICS_GET, EpicsGetResponse.newBuilder().setEpic(liveEpic(ALPHA_SESSION)).build())
    }

    @Test
    fun theArchivedViewShowsTheArchivedConversationWithWhatItSteeredAndBackReturnsToTheInbox() {
        FakeOrreryServer().use { server ->
            serveArchivedAndListed(server)
            setScreen(server)
            awaitNode(hasTestTag(convoTag("%2")))
            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))

            composeRule.onNodeWithTag("open-archived").performClick()

            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(1, countNodes(hasTestTag(hungTag("epic:$EPIC_ID"))))
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))

            composeRule.onNodeWithTag("back-to-inbox").performClick()

            awaitNode(hasTestTag(convoTag("%2")))
            assertEquals(0, countNodes(hasTestTag(convoTag("%1"))))
        }
    }

    @Test
    fun theArchivedViewSurvivesLeavingTheScreen() {
        FakeOrreryServer().use { server ->
            serveArchivedAndListed(server)
            setScreen(server, PaneListState())
            awaitNode(hasTestTag(convoTag("%2")))
            composeRule.onNodeWithTag("open-archived").performClick()
            awaitNode(hasTestTag("back-to-inbox"))

            composeRule.runOnIdle { screenShown = false }
            composeRule.runOnIdle { screenShown = true }

            awaitNode(hasTestTag(convoTag("%1")))
            assertEquals(0, countNodes(hasTestTag(convoTag("%2"))))
            assertEquals(1, countNodes(hasTestTag("back-to-inbox")))
        }
    }
}
