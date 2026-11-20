package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.BackgroundTask as BackgroundTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.ui.StreamedConversation
import io.github.joshsymonds.orrery.ui.applyDelta
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class OrreryClientHeadFieldsTest {
    @Test
    fun epicsCarryEachEpicsSteeringAsServed() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder()
                    .addEpics(EpicProto.newBuilder().setId("example-repo/steered").setSlug("steered").setSteering(SESSION_ID))
                    .addEpics(EpicProto.newBuilder().setId("example-repo/unsteered").setSlug("unsteered"))
                    .build(),
            )

            val steering = OrreryClient(server.baseUrl).epics().associate { it.id to it.steering }

            assertEquals(mapOf("example-repo/steered" to SESSION_ID, "example-repo/unsteered" to ""), steering)
        }
    }

    @Test
    fun panesCarryEachPanesOrreryIdAndPutOffAsServed() {
        FakeOrreryServer().use { server ->
            server.respond(
                PANES_LIST,
                PanesListResponse.newBuilder()
                    .addPanes(PaneProto.newBuilder().setId("%14").setOrreryId("example-orrery-id").setPutOff(true))
                    .addPanes(PaneProto.newBuilder().setId("%15"))
                    .build(),
            )

            val marks = OrreryClient(server.baseUrl).panes().map { it.orreryId to it.putOff }

            assertEquals(listOf("example-orrery-id" to true, "" to false), marks)
        }
    }

    @Test
    fun headlessSessionIsItsOwnOrreryIdAndCarriesItsPutOff() {
        FakeOrreryServer().use { server ->
            server.respond(
                LIST_HEADLESS,
                ListHeadlessResponse.newBuilder()
                    .addSessions(HeadlessSessionProto.newBuilder().setId(SESSION_ID).setPutOff(true))
                    .build(),
            )

            val session = OrreryClient(server.baseUrl).headless().single()

            assertEquals(SESSION_ID, session.orreryId)
            assertTrue(session.putOff)
        }
    }

    @Test
    fun sessionStreamBackgroundTasksReplaceOnAStateAndKeepWithoutOne() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) {
                Reply.Stream(listOf(firstDeltaProto, secondDeltaProto, thirdDeltaProto), StreamEnd.Clean)
            }
            val delivered = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { delivered.add(it) }
            }

            val backgrounds = delivered
                .runningFold<ConversationDelta, StreamedConversation?>(null) { held, delta -> held.applyDelta(delta) }
                .drop(1)
                .map { it?.conversation?.background }
            assertEquals(listOf(running, running, done), backgrounds)
        }
    }

    private val running = listOf(
        BackgroundTask(id = "example-agent", type = "agent", status = "running", description = "Example agent task."),
        BackgroundTask(id = "example-shell", type = "shell", status = "running", description = "Example shell task."),
    )

    private val done = listOf(
        BackgroundTask(id = "example-agent", type = "agent", status = "done", description = "Example agent task."),
    )

    private val firstDeltaProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(10L)
        .setState(
            ConversationStateProto.newBuilder()
                .setOrigin("Example opening prompt.")
                .addBackground(backgroundProto("example-agent", "agent", "running", "Example agent task."))
                .addBackground(backgroundProto("example-shell", "shell", "running", "Example shell task.")),
        )
        .build()

    private val secondDeltaProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(11L)
        .build()

    private val thirdDeltaProto = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(12L)
        .setState(
            ConversationStateProto.newBuilder()
                .setOrigin("Example opening prompt.")
                .addBackground(backgroundProto("example-agent", "agent", "done", "Example agent task.")),
        )
        .build()

    private fun backgroundProto(id: String, type: String, status: String, description: String): BackgroundTaskProto =
        BackgroundTaskProto.newBuilder()
            .setId(id)
            .setType(type)
            .setStatus(status)
            .setDescription(description)
            .build()

    private companion object {
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val SESSION_WATCH_CONVERSATION = "/orrery.v1.Sessions/WatchConversation"
    }
}
