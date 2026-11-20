package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EndedBarTest {
    private companion object {
        const val REPO = "/home/user/projects/synthetic-repo"
        const val PROJECT_DIR = "/home/user/project"
        const val EPIC = "synthetic-repo/synthetic-epic"
        const val SLUG = "synthetic-epic"
        const val SESSION = "sess-ended-headless"
        const val CONTINUED = "sess-continued"
        const val HOST = "synthetic-host"
        const val REFUSAL = "example refusal from orreryd"
        const val NO_ANSWER = "The server did not answer, so the move may have gone through. Reading the epic again…"
        const val ENDED_LEAD =
            "This conversation has ended, and $SLUG still runs. Continue it in a new conversation, which starts with " +
                "the contract and the epic’s log, or move it to one that is running."
        const val UNSTEERED_LEAD =
            "No conversation steers $SLUG. Continue it in a new conversation, which starts with the contract and " +
                "the epic’s log, or move it to one that is running."
        const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
        const val HOST_PATH = "/orrery.v1.Host/Status"
        const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
        const val GET_PATH = "/orrery.v1.Epics/Get"
        const val CONTINUE_PATH = "/orrery.v1.Epics/Continue"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
        const val UNLISTED = "example session is gone"
        const val COMPOSER_TAG = "composer"
        const val BAR_TAG = "ended-bar"
        const val DOCK_TAG = "dock"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The steerings the bar reported through onSteered, in order. */
    private val steered = CopyOnWriteArrayList<String>()

    private fun listedEpic(
        steering: String = SESSION,
        live: Boolean = true,
        source: String = "orreryd",
    ): EpicProto = EpicProto.newBuilder()
        .setId(EPIC)
        .setRepository(REPO)
        .setSlug(SLUG)
        .setLive(live)
        .setSource(source)
        .setSteering(steering)
        .build()

    private fun epicRow(): EpicRow = EpicRow(
        id = EPIC,
        repository = REPO,
        slug = SLUG,
        outcome = "",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        source = "orreryd",
        steering = SESSION,
    )

    private fun epicGetResponse(steering: String): EpicsGetResponse =
        EpicsGetResponse.newBuilder().setEpic(listedEpic(steering = steering)).build()

    private fun nodeExists(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    private fun enabled(tag: String): Boolean =
        !composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.contains(SemanticsProperties.Disabled)

    /** Taps the bar's [tag] button. */
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performClick()
    }

    private fun setBar(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                EndedBar(
                    client = client,
                    epic = epicRow(),
                    repository = REPO,
                    lead = endedLead(SLUG),
                    onSteered = { steered.add(it) },
                )
            }
        }
    }

    private fun setScreen(server: FakeOrreryServer, onSteered: (String) -> Unit = {}) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION, client, onSteered = onSteered) }
        }
    }

    /** The headless session in [state], streamed as the screen's one delta. */
    private fun serveWatch(server: FakeOrreryServer, state: String) {
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION)
            .setSeq(1L)
            .setState(
                ConversationState.newBuilder().setSession(
                    HeadlessSession.newBuilder()
                        .setId(SESSION)
                        .setDir(PROJECT_DIR)
                        .setLabel("example-label")
                        .setState(state),
                ),
            )
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(delta), StreamEnd.Open) }
    }

    private fun serveHost(server: FakeOrreryServer) {
        server.respond(HOST_PATH, HostStatusResponse.newBuilder().setHost(HOST).build())
    }

    /** Lets the main thread and every request it started finish, so a second call would be on the server before a count. */
    private fun settle() {
        Quiescence.awaitUntil("the work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    /**
     * Waits until the screen's head shows the host it read. The head reads its epics and then its host in one step,
     * so once the host shows, the epics the bar decides on are read too.
     */
    private fun awaitHeadRead() {
        composeRule.awaitUntil("the head reads its epics and host") {
            composeRule.onAllNodesWithText("· on $HOST", substring = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    /** A session in [state] steering [epic], where the screen decides between the bar and the composer. */
    private fun serveSessionSteering(server: FakeOrreryServer, state: String, epic: EpicProto) {
        serveHost(server)
        serveWatch(server, state)
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(GET_PATH, EpicsGetResponse.newBuilder().setEpic(epic).build())
    }

    @Test
    fun aStoppedSessionSteeringALiveOrreryEpicShowsTheEndedBarInPlaceOfTheComposer() {
        FakeOrreryServer().use { server ->
            serveSessionSteering(server, "stopped", listedEpic())
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(ENDED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(COMPOSER_TAG))
        }
    }

    @Test
    fun aFailedSessionSteeringALiveOrreryEpicShowsTheEndedBarInPlaceOfTheComposer() {
        FakeOrreryServer().use { server ->
            serveSessionSteering(server, "failed", listedEpic())
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(ENDED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(COMPOSER_TAG))
        }
    }

    /** A session whose watch answers not_found, listed by no pane or headless session, steering [epic]. */
    private fun serveUnlistedSessionSteering(server: FakeOrreryServer, epic: EpicProto) {
        serveHost(server)
        server.route(WATCH_PATH) { Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, UNLISTED)) }
        server.respond(PANES_PATH, PanesListResponse.getDefaultInstance())
        server.respond(HEADLESS_PATH, ListHeadlessResponse.getDefaultInstance())
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(GET_PATH, EpicsGetResponse.newBuilder().setEpic(epic).build())
    }

    @Test
    fun anUnlistedSessionSteeringALiveOrreryEpicShowsTheEndedBarInPlaceOfTheDock() {
        FakeOrreryServer().use { server ->
            serveUnlistedSessionSteering(server, listedEpic())
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(ENDED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(DOCK_TAG))
            assertFalse(nodeExists(COMPOSER_TAG))
        }
    }

    @Test
    fun aSessionWhoseWatchAnswersNotFoundButIsStillListedKeepsTheDockAndNoBar() {
        FakeOrreryServer().use { server ->
            serveUnlistedSessionSteering(server, listedEpic())
            server.respond(
                HEADLESS_PATH,
                ListHeadlessResponse.newBuilder()
                    .addSessions(HeadlessSession.newBuilder().setId(SESSION).setDir(PROJECT_DIR).setState("working"))
                    .build(),
            )
            setScreen(server)

            composeRule.awaitUntil("the listing is read") { server.requests.any { it.rawPath == HEADLESS_PATH } }
            settle()

            assertTrue(nodeExists(DOCK_TAG))
            assertFalse(nodeExists(BAR_TAG))
        }
    }

    @Test
    fun aWorkingSessionSteeringALiveOrreryEpicKeepsTheComposerAndNoBar() {
        FakeOrreryServer().use { server ->
            serveSessionSteering(server, "working", listedEpic())
            setScreen(server)

            awaitHeadRead()
            assertTrue(nodeExists(COMPOSER_TAG))
            assertFalse(nodeExists(BAR_TAG))
        }
    }

    @Test
    fun aStoppedSessionSteeringAnEndedOrreryEpicKeepsTheComposerAndNoBar() {
        FakeOrreryServer().use { server ->
            serveSessionSteering(server, "stopped", listedEpic(live = false))
            setScreen(server)

            awaitHeadRead()
            assertTrue(nodeExists(COMPOSER_TAG))
            assertFalse(nodeExists(BAR_TAG))
        }
    }

    @Test
    fun aStoppedSessionSteeringARecordEpicKeepsTheComposerAndNoBar() {
        FakeOrreryServer().use { server ->
            serveSessionSteering(server, "stopped", listedEpic(source = "record"))
            setScreen(server)

            awaitHeadRead()
            assertTrue(nodeExists(COMPOSER_TAG))
            assertFalse(nodeExists(BAR_TAG))
        }
    }

    @Test
    fun theBarsContinueReportsTheSteeringItsReadNamesThroughSessionScreensOnSteered() {
        FakeOrreryServer().use { server ->
            val continued = AtomicBoolean(false)
            serveHost(server)
            serveWatch(server, "stopped")
            server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(listedEpic()).build())
            server.route(GET_PATH) {
                Reply.Message(epicGetResponse(if (continued.get()) CONTINUED else SESSION), 0L)
            }
            server.route(CONTINUE_PATH) {
                continued.set(true)
                Reply.Message(EpicsContinueResponse.newBuilder().setSession(CONTINUED).build(), 0L)
            }
            setScreen(server, onSteered = { steered.add(it) })

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            tap("ended-continue")
            composeRule.awaitUntil("the re-read names the started conversation") { steered.isNotEmpty() }
            settle()

            assertEquals(listOf(CONTINUED), steered.toList())
        }
    }

    @Test
    fun endedContinueSendsOneContinueThenOneReadAndReportsTheSteeringItNames() {
        FakeOrreryServer().use { server ->
            server.respond(CONTINUE_PATH, EpicsContinueResponse.newBuilder().setSession(CONTINUED).build())
            server.respond(GET_PATH, epicGetResponse(CONTINUED))
            setBar(server)

            tap("ended-continue")
            composeRule.awaitUntil("the re-read names the started conversation") { steered.isNotEmpty() }
            settle()

            val continues = server.requests.filter { it.rawPath == CONTINUE_PATH }
            assertEquals(1, continues.size)
            assertEquals(EPIC, EpicsContinueRequest.parseFrom(continues.single().body).id)
            assertEquals(1, server.requests.count { it.rawPath == GET_PATH })
            assertEquals(listOf(CONTINUED), steered.toList())
        }
    }

    /**
     * The bar of a host that drops it once the epic moves, before the re-read answers. The host's scope runs the bar's
     * calls (as SessionScreen passes its own), so the re-read still reaches onSteered. Composition coroutines run on the
     * test's frame clock, which stands still while the re-read is held, so the host drops the bar on a frame this test
     * advances itself.
     */
    @Test
    fun aContinueWhoseBarLeavesBeforeTheReReadAnswersStillReportsTheSteeringTheReadNames() {
        FakeOrreryServer().use { server ->
            val reReads = AtomicInteger()
            val release = CountDownLatch(1)
            val barLeft = AtomicBoolean(false)
            val shown = mutableStateOf(true)
            server.respond(CONTINUE_PATH, EpicsContinueResponse.newBuilder().setSession(CONTINUED).build())
            server.route(GET_PATH) {
                reReads.incrementAndGet()
                release.await()
                Reply.Message(epicGetResponse(CONTINUED), 0L)
            }
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                val hostScope = rememberCoroutineScope()
                MaterialTheme {
                    if (shown.value) {
                        EndedBar(
                            client = client,
                            epic = epicRow(),
                            repository = REPO,
                            lead = endedLead(SLUG),
                            onSteered = { steered.add(it) },
                            scope = hostScope,
                        )
                        DisposableEffect(Unit) { onDispose { barLeft.set(true) } }
                    }
                }
            }
            // The held re-read is let go however the test ends, so a failing run closes the server and does not hang.
            try {
                tap("ended-continue")
                composeRule.awaitUntil("the re-read is out") { reReads.get() == 1 }

                shown.value = false
                Snapshot.sendApplyNotifications()
                composeRule.mainClock.advanceTimeByFrame()
                assertTrue(barLeft.get())
                assertTrue(steered.isEmpty())

                release.countDown()
                composeRule.awaitUntil("the re-read names the started conversation") { steered.isNotEmpty() }
                settle()

                assertEquals(1, server.requests.count { it.rawPath == CONTINUE_PATH })
                assertEquals(1, reReads.get())
                assertEquals(listOf(CONTINUED), steered.toList())
            } finally {
                release.countDown()
            }
        }
    }

    @Test
    fun aRefusedEndedContinueShowsTheServersMessageInTheBarWithNoRead() {
        FakeOrreryServer().use { server ->
            server.fail(CONTINUE_PATH, Code.FAILED_PRECONDITION, REFUSAL)
            setBar(server)

            tap("ended-continue")
            composeRule.awaitUntil("the refusal shows in the bar") { nodeExists("ended-note") }
            settle()

            composeRule.onNodeWithTag("ended-note").assertTextEquals(REFUSAL)
            assertEquals(0, server.requests.count { it.rawPath == GET_PATH })
            assertTrue(steered.isEmpty())
        }
    }

    @Test
    fun aDroppedEndedContinueShowsTheNoAnswerNoteInTheBarUntilTheReadAnswers() {
        FakeOrreryServer().use { server ->
            server.drop(CONTINUE_PATH)
            val release = CountDownLatch(1)
            server.route(GET_PATH) {
                release.await()
                Reply.Message(epicGetResponse(CONTINUED), 0L)
            }
            setBar(server)

            tap("ended-continue")
            composeRule.awaitUntil("the no-answer note shows in the bar") { nodeExists("ended-note") }

            composeRule.onNodeWithTag("ended-note").assertTextEquals(NO_ANSWER)
            assertFalse(enabled("ended-continue"))
            release.countDown()
            composeRule.awaitUntil("the read answers, so the note clears") { !nodeExists("ended-note") }
            settle()

            assertTrue(enabled("ended-continue"))
            assertEquals(1, server.requests.count { it.rawPath == CONTINUE_PATH })
            assertTrue(steered.isEmpty())
        }
    }

    @Test
    fun steerFromAnotherOpensTheSteerMenuSheet() {
        FakeOrreryServer().use { server ->
            setBar(server)

            tap("ended-steer")
            composeRule.awaitUntil("the steer menu opens") { nodeExists("steer-menu") }
            settle()

            composeRule.onNodeWithText("Steer $SLUG from").assertIsDisplayed()
            assertEquals(0, server.requests.count { it.rawPath == CONTINUE_PATH })
            assertTrue(steered.isEmpty())
        }
    }

    @Test
    fun unsteeredLeadNamesTheEpicAndTheWayOn() {
        assertEquals(UNSTEERED_LEAD, unsteeredLead(SLUG))
    }
}
