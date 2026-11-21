package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class OrreryClientEscalationMakerTest {
    @Test
    fun `an escalated question maps its maker, session, reason and time`() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(
                        Epic.newBuilder()
                            .setId("example-repo/example-epic")
                            .setRepository("example-repo")
                            .setSlug("example-epic"),
                    )
                    .addQuestions(
                        EpicQuestion.newBuilder()
                            .setId("Q1")
                            .setText("Which lane?")
                            .setTurn(2L)
                            .setAsked(4102444700L)
                            .setEscalated(4102444900L)
                            .setEscalationReason("The lane choice changes the schema.")
                            .setEscalationBy("conversation")
                            .setEscalationSession("example-session-1"),
                    )
                    .build(),
            )

            val questions = OrreryClient(server.baseUrl).epic("example-repo/example-epic")?.questions

            assertEquals(
                listOf(
                    EpicQuestionRow(
                        id = "Q1",
                        text = "Which lane?",
                        turn = 2L,
                        asked = 4102444700L,
                        escalated = 4102444900L,
                        escalationReason = "The lane choice changes the schema.",
                        escalationBy = "conversation",
                        escalationSession = "example-session-1",
                    ),
                ),
                questions,
            )
        }
    }

    private companion object {
        const val EPICS_GET = "/orrery.v1.Epics/Get"
    }
}
