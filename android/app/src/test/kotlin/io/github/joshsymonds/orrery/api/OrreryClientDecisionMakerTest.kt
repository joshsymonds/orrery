package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.EpicDecision
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class OrreryClientDecisionMakerTest {
    @Test
    fun `a history decision maps its maker and session`() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_HISTORY,
                EpicsHistoryResponse.newBuilder()
                    .addEntries(
                        EpicHistoryEntry.newBuilder()
                            .setTime(4102444850L)
                            .setCursor("example-cursor-1")
                            .setDecision(
                                EpicDecision.newBuilder()
                                    .setSeq(2L)
                                    .setId("example-decision-1")
                                    .setText("Example answer.")
                                    .setTime(4102444850L)
                                    .setQuestion("Q1")
                                    .setBy("conversation")
                                    .setSession("example-session-1"),
                            ),
                    )
                    .build(),
            )
            val client = OrreryClient(server.baseUrl)

            val page = client.history("example-repo/example-epic")

            assertEquals(
                EpicHistoryRow(
                    time = 4102444850L,
                    cursor = "example-cursor-1",
                    kind = "decision",
                    decision = EpicDecisionRow(
                        seq = 2L,
                        id = "example-decision-1",
                        text = "Example answer.",
                        time = 4102444850L,
                        question = "Q1",
                        by = "conversation",
                        session = "example-session-1",
                    ),
                ),
                page.entries.single(),
            )
        }
    }

    private companion object {
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
    }
}
