package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.push.PushNotifier
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController
import java.nio.ByteBuffer
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
class EpicTalkBarTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val CHANGED = 4_102_444_800L
        const val STEERING = "example-session"
        const val STEERING_LABEL = "synthetic-steering-label"
        const val PANE_ID = "%2"
        const val PANE_LABEL = "synthetic-pane-label"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val WATCH = "/orrery.v1.Sessions/WatchConversation"
        const val ENVELOPE_HEADER_BYTES = 5
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setBar(steering: String, onOpen: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme {
                EpicTalkBar(steering = steering, onOpen = onOpen)
            }
        }
    }

    private fun epic(steering: String): EpicsGetResponse {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome("")
            .setChanged(CHANGED)
            .setLive(true)
            .setSteering(steering)
        return EpicsGetResponse.newBuilder().setEpic(epic).build()
    }

    @Test
    fun tappingTheBarCallsOnOpenOnce() {
        val opened = AtomicInteger()
        setBar(steering = STEERING, onOpen = { opened.incrementAndGet() })

        composeRule.onNodeWithTag("epic-talk-bar")
            .assertTextEquals("Talk to the session that steers it")
            .performClick()

        assertEquals(1, opened.get())
    }

    @Test
    fun anEpicWithNoSteererDrawsTheNoConversationLineAndNoBar() {
        setBar(steering = "")

        composeRule.onNodeWithTag("epic-talk-none")
            .assertTextEquals("No conversation steers this epic")
        composeRule.onAllNodesWithTag("epic-talk-bar").assertCountEquals(0)
    }

    @Test
    fun theEpicScreenDrawsItsFootUnderTheLoadedPageWithTheEpicsSteering() {
        val steered = AtomicReference<String?>(null)
        val opened = AtomicInteger()
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, epic(steering = STEERING))
            server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
            composeRule.setContent {
                MaterialTheme {
                    EpicScreen(
                        client = OrreryClient(server.baseUrl),
                        epicId = EPIC_ID,
                        onBack = {},
                        onOpenStep = {},
                        foot = { detail ->
                            steered.set(detail.epic.steering)
                            EpicTalkBar(steering = detail.epic.steering, onOpen = { opened.incrementAndGet() })
                        },
                    )
                }
            }
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("epic-section:now", useUnmergedTree = true)
                    .fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("epic-talk-bar").assertIsDisplayed().performClick()

            assertEquals(STEERING, steered.get())
            assertEquals(1, opened.get())
        }
    }

    /** Launches the app on the epic [EPIC_ID] at the top, as a notice opens it, with its settings pointed at [server]. */
    private fun launchOnEpic(server: FakeOrreryServer): ActivityController<MainActivity> {
        val context = ApplicationProvider.getApplicationContext<Context>()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 0f)
        val intent = Intent(context, MainActivity::class.java).putExtra(PushNotifier.EXTRA_EPIC, EPIC_ID)
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup()
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun titled(label: String): SemanticsMatcher = hasTestTag("pane-title").and(hasText(label))

    /** The watch stream of the steering session: its state, as the session's screen opens. */
    private fun steeringState(): SessionsWatchConversationResponse = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(STEERING)
        .setSeq(1L)
        .setState(
            ConversationState.newBuilder()
                .setOrigin("synthetic origin")
                .setSession(
                    HeadlessSession.newBuilder()
                        .setId(STEERING)
                        .setDir("/home/user/example-repo")
                        .setLabel(STEERING_LABEL)
                        .setState("working")
                        .setChanged(CHANGED),
                ),
        )
        .build()

    /** The id a watch request names: its enveloped body, one flag byte, a big-endian length, then the payload. */
    private fun watchedId(request: RecordedRequest): String {
        val length = ByteBuffer.wrap(request.body, 1, 4).int
        return SessionsWatchConversationRequest.parseFrom(
            request.body.copyOfRange(ENVELOPE_HEADER_BYTES, ENVELOPE_HEADER_BYTES + length),
        ).id
    }

    @Test
    fun tappingTheFootBarOpensTheSteeringHeadlessSessionOnItsOwnScreen() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, epic(steering = STEERING))
            server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
            server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
            server.route(WATCH) { request ->
                if (watchedId(request) == STEERING) {
                    Reply.Stream(listOf(steeringState()), StreamEnd.Open)
                } else {
                    Reply.Failure(Code.NOT_FOUND, "", 0L)
                }
            }
            val app = launchOnEpic(server)
            try {
                awaitNode(hasTestTag("epic-talk-bar"))

                composeRule.onNodeWithTag("epic-talk-bar").performClick()

                awaitNode(titled(STEERING_LABEL))
            } finally {
                app.pause().stop().destroy()
            }
        }
    }

    @Test
    fun tappingTheFootBarOpensTheSteeringTmuxPaneOnItsOwnScreen() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, epic(steering = PANE_ID))
            server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
            server.respond(
                PANES_LIST,
                PanesListResponse.newBuilder()
                    .addPanes(PaneProto.newBuilder().setId(PANE_ID).setLabel(PANE_LABEL))
                    .build(),
            )
            val app = launchOnEpic(server)
            try {
                awaitNode(hasTestTag("epic-talk-bar"))

                composeRule.onNodeWithTag("epic-talk-bar").performClick()

                awaitNode(titled(PANE_LABEL))
            } finally {
                app.pause().stop().destroy()
            }
        }
    }
}
