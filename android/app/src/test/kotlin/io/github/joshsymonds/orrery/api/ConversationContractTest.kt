package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.ui.ConvoEntry
import io.github.joshsymonds.orrery.ui.ToolCall
import io.github.joshsymonds.orrery.ui.foldConversation
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ConversationContractTest {
    // The conversation route's example response from docs/contract.md, copied byte for byte.
    private val contractBody = """
{"origin":"Example opening prompt.","items":[{"kind":"prompt","time":1700000000,"text":"Example opening prompt.","tool":"","input":"","id":"","error":false,"notice":""},{"kind":"notice","time":1700000050,"text":"","tool":"","input":"","id":"","error":false,"notice":"compaction"},{"kind":"reply","time":1700000100,"text":"Example reply.","tool":"","input":"","id":"","error":false,"notice":""},{"kind":"tool","time":1700000200,"text":"","tool":"Bash","input":"example command","id":"example-tool-use","error":false,"notice":""},{"kind":"result","time":1700000210,"text":"Example output.","tool":"","input":"","id":"example-tool-use","error":false,"notice":""},{"kind":"notice","time":1700000300,"text":"Example task finished.","tool":"","input":"","id":"","error":false,"notice":"task"}],"goal":"Example goal.","waiting":[{"text":"Example queued message.","time":1700000250,"canceled":false}]}
    """

    private fun contractConversation(): Conversation =
        FakeOrreryServer().use { server ->
            server.respond("GET", "/v1/panes/%253/conversation", 200, contractBody)
            requireNotNull(OrreryClient(server.baseUrl).conversation("%3"))
        }

    @Test
    fun contractExampleParsesEveryItemAndKey() {
        assertEquals(
            Conversation(
                origin = "Example opening prompt.",
                items = listOf(
                    ConversationItem("prompt", 1700000000L, "Example opening prompt.", "", "", "", false, ""),
                    ConversationItem("notice", 1700000050L, "", "", "", "", false, "compaction"),
                    ConversationItem("reply", 1700000100L, "Example reply.", "", "", "", false, ""),
                    ConversationItem("tool", 1700000200L, "", "Bash", "example command", "example-tool-use", false, ""),
                    ConversationItem("result", 1700000210L, "Example output.", "", "", "example-tool-use", false, ""),
                    ConversationItem("notice", 1700000300L, "Example task finished.", "", "", "", false, "task"),
                ),
                goal = "Example goal.",
                waiting = listOf(WaitingItem("Example queued message.", 1700000250L, false)),
            ),
            contractConversation(),
        )
    }

    @Test
    fun contractExampleCarriesNonEmptyGoal() {
        assertTrue(JSONObject(contractBody).getString("goal").isNotEmpty())
    }

    @Test
    fun foldOfContractExampleDrawsEachNoticeInItsPlace() {
        val items = contractConversation().items

        assertEquals(
            listOf(
                ConvoEntry.Time(1700000000L),
                ConvoEntry.Prompt("Example opening prompt.", 1700000000L),
                ConvoEntry.Notice("compaction", "", false, 1700000050L),
                ConvoEntry.Time(1700000100L),
                ConvoEntry.Reply("Example reply.", 1700000100L),
                ConvoEntry.Tool(ToolCall("example-tool-use", "Bash", "example command", "Example output.", false, 1700000200L)),
                ConvoEntry.Notice("task", "Example task finished.", false, 1700000300L),
            ),
            foldConversation(items),
        )
    }
}
