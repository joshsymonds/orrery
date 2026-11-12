package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.ui.ConvoEntry
import io.github.joshsymonds.orrery.ui.ToolCall
import io.github.joshsymonds.orrery.ui.foldConversation
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ConversationContractTest {
    // The conversation route's example response from docs/contract.md, as the Connect answer carries it.
    private val contractResponse: ConversationResponse = ConversationResponse.newBuilder()
        .setOrigin("Example opening prompt.")
        .addItems(ConversationItemProto.newBuilder().setKind("prompt").setTime(1700000000L).setText("Example opening prompt."))
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("compaction")
                .setTime(1700000050L)
                .setText("Example summary.")
                .setTrigger("auto")
                .setTokens(611000L),
        )
        .addItems(ConversationItemProto.newBuilder().setKind("reply").setTime(1700000100L).setText("Example reply."))
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("tool")
                .setTime(1700000200L)
                .setTool("Bash")
                .setInput("example command")
                .setId("example-tool-use"),
        )
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("result")
                .setTime(1700000210L)
                .setText("Example output.")
                .setId("example-tool-use"),
        )
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("notice")
                .setTime(1700000300L)
                .setText("Example task finished.")
                .setNotice("task"),
        )
        .setGoal("Example goal.")
        .addWaiting(WaitingMessage.newBuilder().setText("Example queued message.").setTime(1700000250L).setCanceled(false))
        .setContext(ConversationContextProto.newBuilder().setTokens(240000L).setWindow(1000000L))
        .build()

    private fun contractConversation(): Conversation =
        FakeOrreryServer().use { server ->
            server.respond(CONVERSATION, contractResponse)
            requireNotNull(OrreryClient(server.baseUrl).conversation("%3"))
        }

    @Test
    fun contractExampleParsesEveryItemAndKey() {
        assertEquals(
            Conversation(
                origin = "Example opening prompt.",
                items = listOf(
                    ConversationItem("prompt", 1700000000L, "Example opening prompt.", "", "", "", false, "", "", 0L),
                    ConversationItem("compaction", 1700000050L, "Example summary.", "", "", "", false, "", "auto", 611000L),
                    ConversationItem("reply", 1700000100L, "Example reply.", "", "", "", false, "", "", 0L),
                    ConversationItem("tool", 1700000200L, "", "Bash", "example command", "example-tool-use", false, "", "", 0L),
                    ConversationItem("result", 1700000210L, "Example output.", "", "", "example-tool-use", false, "", "", 0L),
                    ConversationItem("notice", 1700000300L, "Example task finished.", "", "", "", false, "task", "", 0L),
                ),
                goal = "Example goal.",
                waiting = listOf(WaitingItem("Example queued message.", 1700000250L, false)),
                context = ConversationContext(tokens = 240000L, window = 1000000L),
            ),
            contractConversation(),
        )
    }

    @Test
    fun contractExampleCarriesNonEmptyGoal() {
        assertTrue(contractResponse.goal.isNotEmpty())
    }

    @Test
    fun foldOfContractExampleDrawsEachEntryInItsPlace() {
        val items = contractConversation().items

        assertEquals(
            listOf(
                ConvoEntry.Time(1700000000L),
                ConvoEntry.Prompt("Example opening prompt.", 1700000000L),
                ConvoEntry.Compaction("auto", 611000L, "Example summary.", 1700000050L),
                ConvoEntry.Time(1700000100L),
                ConvoEntry.Reply("Example reply.", 1700000100L),
                ConvoEntry.Tool(ToolCall("example-tool-use", "Bash", "example command", "Example output.", false, 1700000200L)),
                ConvoEntry.Notice("task", "Example task finished.", false, 1700000300L),
            ),
            foldConversation(items),
        )
    }

    private companion object {
        const val CONVERSATION = "/orrery.v1.Panes/Conversation"
    }
}
