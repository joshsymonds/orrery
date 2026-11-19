package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.BackgroundTask
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.ui.StreamedConversation
import io.github.joshsymonds.orrery.ui.applyDelta
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class OrreryClientBackgroundTest {
    private val steeredProto = Epic.newBuilder()
        .setId("example-repo/example-epic")
        .setRepository("example-repo")
        .setSlug("example-epic")
        .setSteering("example-steering-session")
        .setArchived(true)
        .build()

    private val unsteeredProto = Epic.newBuilder()
        .setId("example-repo/example-other")
        .setRepository("example-repo")
        .setSlug("example-other")
        .build()

    private val agentTaskProto = BackgroundTask.newBuilder()
        .setId("example-task-1")
        .setToolUseId("example-tool-use-1")
        .setDescription("Example agent description.")
        .setType("agent")
        .setSubagentType("example-subagent")
        .setStatus("running")
        .setActivity("Example activity.")
        .setStarted(1700000400L)
        .setEnded(0L)
        .build()

    private val shellTaskProto = BackgroundTask.newBuilder()
        .setId("example-task-2")
        .setToolUseId("example-tool-use-2")
        .setDescription("Example shell description.")
        .setType("shell")
        .setSubagentType("")
        .setStatus("done")
        .setActivity("Example shell activity.")
        .setStarted(1700000500L)
        .setEnded(1700000600L)
        .build()

    private val agentTaskRow = BackgroundTaskRow(
        id = "example-task-1",
        toolUseId = "example-tool-use-1",
        description = "Example agent description.",
        type = "agent",
        subagentType = "example-subagent",
        status = "running",
        activity = "Example activity.",
        started = 1700000400L,
        ended = 0L,
    )

    private val shellTaskRow = BackgroundTaskRow(
        id = "example-task-2",
        toolUseId = "example-tool-use-2",
        description = "Example shell description.",
        type = "shell",
        subagentType = "",
        status = "done",
        activity = "Example shell activity.",
        started = 1700000500L,
        ended = 1700000600L,
    )

    @Test
    fun epicsMapsSteeringAndArchivedFromTheServedEpics() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_LIST,
                EpicsListResponse.newBuilder().addEpics(steeredProto).addEpics(unsteeredProto).build(),
            )

            val epics = OrreryClient(server.baseUrl).epics()

            assertEquals(
                listOf("example-steering-session" to true, "" to false),
                epics.map { it.steering to it.archived },
            )
        }
    }

    @Test
    fun aWatchedSessionDeltaMapsEveryBackgroundTaskWithItsTypeAndStatus() {
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) {
                Reply.Stream(listOf(sessionDeltaProto(agentTaskProto, shellTaskProto)), StreamEnd.Clean)
            }
            val received = mutableListOf<ConversationDelta>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.add(it) }
            }

            assertEquals(listOf(agentTaskRow, shellTaskRow), received.single().state?.background)
        }
    }

    @Test
    fun aDeltaWithNoStateKeepsTheBackgroundHeldAcrossIt() {
        val held = held(background = listOf(agentTaskRow))

        val next = held.applyDelta(keepaliveDelta(state = null))

        assertEquals(listOf(agentTaskRow), next.background)
    }

    @Test
    fun aDeltaWithAStateReplacesTheHeldBackground() {
        val held = held(background = listOf(agentTaskRow))

        val next = held.applyDelta(keepaliveDelta(state = stateWith(listOf(shellTaskRow))))

        assertEquals(listOf(shellTaskRow), next.background)
    }

    @Test
    fun aResetDeltaTakesItsStateBackgroundAndHoldsNoneWithoutAState() {
        val held = held(background = listOf(agentTaskRow))

        val withState = held.applyDelta(keepaliveDelta(state = stateWith(listOf(shellTaskRow))).copy(reset = true))
        val withoutState = held.applyDelta(keepaliveDelta(state = null).copy(reset = true))

        assertEquals(listOf(shellTaskRow), withState.background)
        assertTrue(withoutState.background.isEmpty())
    }

    private fun sessionDeltaProto(vararg tasks: BackgroundTask): SessionsWatchConversationResponse =
        SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(10L)
            .setState(ConversationStateProto.newBuilder().addAllBackground(tasks.toList()))
            .build()

    private fun keepaliveDelta(state: ConversationState?): ConversationDelta =
        ConversationDelta(transcript = SESSION_ID, seq = 11L, reset = false, items = emptyList(), state = state)

    private fun stateWith(background: List<BackgroundTaskRow>): ConversationState =
        ConversationState(
            origin = "Example opening prompt.",
            goal = "",
            waiting = emptyList(),
            context = null,
            pane = null,
            background = background,
        )

    private fun held(background: List<BackgroundTaskRow>): StreamedConversation =
        StreamedConversation(
            transcript = SESSION_ID,
            seq = 9L,
            conversation = Conversation(origin = "Example opening prompt.", items = emptyList()),
            pane = null,
            background = background,
        )

    private companion object {
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val SESSION_WATCH_CONVERSATION = "/orrery.v1.Sessions/WatchConversation"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
    }
}
