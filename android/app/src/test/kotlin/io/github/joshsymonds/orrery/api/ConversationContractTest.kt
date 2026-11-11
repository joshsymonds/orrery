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
{"origin":"Example opening prompt.","items":[{"kind":"prompt","time":1700000000,"text":"Example opening prompt.","tool":"","input":"","id":"","error":false,"notice":"","trigger":"","tokens":0},{"kind":"compaction","time":1700000050,"text":"Example summary.","tool":"","input":"","id":"","error":false,"notice":"","trigger":"auto","tokens":611000},{"kind":"reply","time":1700000100,"text":"Example reply.","tool":"","input":"","id":"","error":false,"notice":"","trigger":"","tokens":0},{"kind":"tool","time":1700000200,"text":"","tool":"Bash","input":"example command","id":"example-tool-use","error":false,"notice":"","trigger":"","tokens":0},{"kind":"result","time":1700000210,"text":"Example output.","tool":"","input":"","id":"example-tool-use","error":false,"notice":"","trigger":"","tokens":0},{"kind":"notice","time":1700000300,"text":"Example task finished.","tool":"","input":"","id":"","error":false,"notice":"task","trigger":"","tokens":0}],"goal":"Example goal.","waiting":[{"text":"Example queued message.","time":1700000250,"canceled":false}],"context":{"tokens":240000,"window":1000000}}
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
        assertTrue(JSONObject(contractBody).getString("goal").isNotEmpty())
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
}
