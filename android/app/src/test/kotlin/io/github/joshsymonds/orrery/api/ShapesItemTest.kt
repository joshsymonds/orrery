package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepFacts as StepFactsProto
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ShapesItemTest {
    @Test
    fun shapesStreamedItemKeepsItsSenderRoundPromptIdAndEpic() {
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setReset(true)
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("prompt")
                    .setTime(1700000000L)
                    .setText("Example check round.")
                    .setSender("engine")
                    .setRound(2)
                    .setPromptId("prompt-1")
                    .setEpic("example-epic"),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(listOf(delta), StreamEnd.Clean) }
            val received = mutableListOf<ConversationItem>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.addAll(it.items) }
            }

            val item = received.single()
            assertEquals("engine", item.sender)
            assertEquals(2, item.round)
            assertEquals("prompt-1", item.promptId)
            assertEquals("example-epic", item.epic)
        }
    }

    @Test
    fun shapesFetchedItemKeepsItsSenderRoundPromptIdAndEpic() {
        val response = ConversationResponse.newBuilder()
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("prompt")
                    .setTime(1700000000L)
                    .setText("Example owner prompt.")
                    .setSender("owner")
                    .setPromptId("prompt-2")
                    .setEpic("example-epic-2"),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val item = requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).items.single()

            assertEquals("owner", item.sender)
            assertEquals(0, item.round)
            assertEquals("prompt-2", item.promptId)
            assertEquals("example-epic-2", item.epic)
        }
    }

    @Test
    fun shapesStepEndItemKeepsItsStepFacts() {
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(2L)
            .addItems(
                ConversationItemProto.newBuilder()
                    .setKind("prompt")
                    .setTime(1700000100L)
                    .setSender("event")
                    .setStepFacts(
                        StepFactsProto.newBuilder()
                            .setRounds(3)
                            .setSeconds(65L)
                            .setCounted(true)
                            .setFiles(2L)
                            .setAdded(96L)
                            .setRemoved(4L),
                    ),
            )
            .build()
        FakeOrreryServer().use { server ->
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(listOf(delta), StreamEnd.Clean) }
            val received = mutableListOf<ConversationItem>()

            runBlocking {
                OrreryClient(server.baseUrl).watchSession(SESSION_ID, 0L) { received.addAll(it.items) }
            }

            assertEquals(
                StepFacts(rounds = 3, seconds = 65L, counted = true, files = 2L, added = 96L, removed = 4L),
                received.single().stepFacts,
            )
        }
    }

    @Test
    fun shapesItemWithoutStepFactsHasNoStepFacts() {
        val response = ConversationResponse.newBuilder()
            .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(1700000000L).setText("Example prompt."))
            .build()
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, response)

            val item = requireNotNull(OrreryClient(server.baseUrl).conversation("%3")).items.single()

            assertNull(item.stepFacts)
        }
    }

    private companion object {
        const val SESSION_ID = "0123456789abcdef0123456789abcdef"
        const val SESSION_WATCH_CONVERSATION = "/orrery.v1.Sessions/WatchConversation"
        const val CONVERSATION = "/orrery.v1.Panes/Conversation"
    }
}
