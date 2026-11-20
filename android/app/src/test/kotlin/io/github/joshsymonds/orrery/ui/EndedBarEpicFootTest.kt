package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EndedBarEpicFootTest {
    private companion object {
        const val REPO = "/home/user/projects/synthetic-repo"
        const val EPIC = "synthetic-repo/synthetic-epic"
        const val SLUG = "synthetic-epic"
        const val SESSION = "sess-steerer"
        const val CONTINUED = "sess-continued"
        const val PANES_PATH = "/orrery.v1.Panes/List"
        const val HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
        const val CONTINUE_PATH = "/orrery.v1.Epics/Continue"
        const val GET_PATH = "/orrery.v1.Epics/Get"
        const val BAR_TAG = "ended-bar"
        const val CONTINUE_TAG = "ended-continue"
        const val STEER_TAG = "ended-steer"
        const val TALK_TAG = "epic-talk-bar"
        const val NONE_TAG = "epic-talk-none"
        const val ENDED_LEAD =
            "This conversation has ended, and $SLUG still runs. Continue it in a new conversation, which starts with " +
                "the contract and the epic’s log, or move it to one that is running."
        const val UNSTEERED_LEAD =
            "No conversation steers $SLUG. Continue it in a new conversation, which starts with the contract and " +
                "the epic’s log, or move it to one that is running."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The steerings the foot reported through onSteered, in order. */
    private val steered = CopyOnWriteArrayList<String>()

    private fun epicRow(steering: String, live: Boolean = true, source: String = "orreryd"): EpicRow = EpicRow(
        id = EPIC,
        repository = REPO,
        slug = SLUG,
        outcome = "",
        changed = 0L,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        source = source,
        steering = steering,
    )

    private fun detail(steering: String, live: Boolean = true, source: String = "orreryd"): EpicDetail = EpicDetail(
        epic = epicRow(steering, live, source),
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        repository = REPO,
    )

    private fun epicProto(steering: String): EpicProto = EpicProto.newBuilder()
        .setId(EPIC)
        .setRepository(REPO)
        .setSlug(SLUG)
        .setLive(true)
        .setSource("orreryd")
        .setSteering(steering)
        .build()

    private fun headless(id: String, state: String): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setDir(REPO)
        .setLabel("synthetic-label")
        .setState(state)
        .build()

    /** The conversations the foot reads: the headless sessions, and the tmux panes, which are empty here. */
    private fun serveConversations(server: FakeOrreryServer, vararg sessions: HeadlessSession) {
        server.respond(PANES_PATH, PanesListResponse.getDefaultInstance())
        server.respond(HEADLESS_PATH, ListHeadlessResponse.newBuilder().addAllSessions(sessions.toList()).build())
    }

    private fun nodeExists(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performClick()
    }

    private fun setFoot(server: FakeOrreryServer, detail: EpicDetail) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                EpicFoot(
                    client = client,
                    detail = detail,
                    onOpenSteerer = {},
                    onSteered = { steered.add(it) },
                )
            }
        }
    }

    /** Lets the foot's read and every request it started finish, so a count taken after it sees them all. */
    private fun settle() {
        Quiescence.awaitUntil("the work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    @Test
    fun aLiveOrreryEpicNoConversationSteersDrawsTheEndedBarWithTheUnsteeredLead() {
        FakeOrreryServer().use { server ->
            setFoot(server, detail(steering = ""))

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(UNSTEERED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(NONE_TAG))
            assertFalse(nodeExists(TALK_TAG))
        }
    }

    @Test
    fun aLiveOrreryEpicWhoseStoppedHeadlessSteererEndedDrawsTheEndedBarWithTheEndedLead() {
        FakeOrreryServer().use { server ->
            serveConversations(server, headless(SESSION, "stopped"))
            setFoot(server, detail(steering = SESSION))

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(ENDED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(TALK_TAG))
        }
    }

    @Test
    fun aLiveOrreryEpicWhoseFailedHeadlessSteererEndedDrawsTheEndedBarWithTheEndedLead() {
        FakeOrreryServer().use { server ->
            serveConversations(server, headless(SESSION, "failed"))
            setFoot(server, detail(steering = SESSION))

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(ENDED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(TALK_TAG))
        }
    }

    @Test
    fun aLiveOrreryEpicWhoseSteererNoPaneOrSessionCarriesDrawsTheEndedBarWithTheEndedLead() {
        FakeOrreryServer().use { server ->
            server.respond(
                PANES_PATH,
                PanesListResponse.newBuilder()
                    .addPanes(PaneProto.newBuilder().setId("%3").setOrreryId("pane-other").setLabel("synthetic-pane"))
                    .build(),
            )
            server.respond(
                HEADLESS_PATH,
                ListHeadlessResponse.newBuilder().addSessions(headless("sess-other", "working")).build(),
            )
            setFoot(server, detail(steering = SESSION))

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            composeRule.onNodeWithText(ENDED_LEAD).assertIsDisplayed()
            assertFalse(nodeExists(TALK_TAG))
        }
    }

    @Test
    fun aLiveOrreryEpicWhoseSteererIsWorkingDrawsTheTalkBarAndNoBar() {
        FakeOrreryServer().use { server ->
            serveConversations(server, headless(SESSION, "working"))
            setFoot(server, detail(steering = SESSION))

            composeRule.awaitUntil("the steerer is read") {
                server.requests.any { it.rawPath == HEADLESS_PATH }
            }
            settle()

            composeRule.onNodeWithTag(TALK_TAG).assertIsDisplayed()
            assertFalse(nodeExists(BAR_TAG))
        }
    }

    @Test
    fun anEndedOrreryEpicWithNoSteererDrawsTheNoConversationLineAndNoBarOrButton() {
        FakeOrreryServer().use { server ->
            setFoot(server, detail(steering = "", live = false))

            composeRule.awaitExactlyOne(hasTestTag(NONE_TAG))
            assertFalse(nodeExists(BAR_TAG))
            assertFalse(nodeExists(CONTINUE_TAG))
            assertFalse(nodeExists(STEER_TAG))
            assertFalse(nodeExists(TALK_TAG))
        }
    }

    @Test
    fun aRecordEpicWithNoSteererDrawsTheNoConversationLineAndNoBarOrButton() {
        FakeOrreryServer().use { server ->
            setFoot(server, detail(steering = "", source = "record"))

            composeRule.awaitExactlyOne(hasTestTag(NONE_TAG))
            assertFalse(nodeExists(BAR_TAG))
            assertFalse(nodeExists(CONTINUE_TAG))
            assertFalse(nodeExists(STEER_TAG))
            assertFalse(nodeExists(TALK_TAG))
        }
    }

    @Test
    fun endedContinueFromTheFootSendsOneContinueThenOneReadAndReportsTheSteeringItNames() {
        FakeOrreryServer().use { server ->
            serveConversations(server, headless(SESSION, "stopped"))
            server.respond(CONTINUE_PATH, EpicsContinueResponse.newBuilder().setSession(CONTINUED).build())
            server.respond(GET_PATH, EpicsGetResponse.newBuilder().setEpic(epicProto(CONTINUED)).build())
            setFoot(server, detail(steering = SESSION))

            composeRule.awaitExactlyOne(hasTestTag(BAR_TAG))
            tap(CONTINUE_TAG)
            composeRule.awaitUntil("the re-read names the started conversation") { steered.isNotEmpty() }
            settle()

            val continues = server.requests.filter { it.rawPath == CONTINUE_PATH }
            assertEquals(1, continues.size)
            assertEquals(EPIC, EpicsContinueRequest.parseFrom(continues.single().body).id)
            assertEquals(1, server.requests.count { it.rawPath == GET_PATH })
            assertEquals(listOf(CONTINUED), steered.toList())
        }
    }
}
