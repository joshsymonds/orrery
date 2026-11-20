package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestionChoice
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRecommendation
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import org.junit.Assert.assertEquals
import org.junit.Test

class AskAnswerClientTest {
    @Test
    fun anEpicQuestionMapsItsChoicesAndRecommendationInOrder() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                EpicsGetResponse.newBuilder()
                    .setEpic(epicProto)
                    .addQuestions(
                        EpicQuestion.newBuilder()
                            .setId("Q1")
                            .setText("Which lane?")
                            .setTurn(2L)
                            .setAsked(4102444700L)
                            .addChoices(EpicQuestionChoice.newBuilder().setId("lane-a").setDoes("Use lane A."))
                            .addChoices(EpicQuestionChoice.newBuilder().setId("lane-b").setDoes("Use lane B."))
                            .setRecommendation(
                                EpicRecommendation.newBuilder()
                                    .setChoice("lane-b")
                                    .setWhy("Lane B is already tested.")
                                    .setConfidence("high"),
                            ),
                    )
                    .addQuestions(
                        EpicQuestion.newBuilder()
                            .setId("Q2")
                            .setText("Which name?")
                            .setTurn(3L)
                            .setAsked(4102444800L),
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
                        choices = listOf(
                            EpicQuestionChoiceRow(id = "lane-a", does = "Use lane A."),
                            EpicQuestionChoiceRow(id = "lane-b", does = "Use lane B."),
                        ),
                        recommendedChoice = "lane-b",
                        recommendedWhy = "Lane B is already tested.",
                    ),
                    EpicQuestionRow(id = "Q2", text = "Which name?", turn = 3L, asked = 4102444800L),
                ),
                questions,
            )
        }
    }

    @Test
    fun answerEpicSendsTheGivenChoiceIdAndItsText() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_ANSWER, EpicsAnswerResponse.getDefaultInstance())

            val result = OrreryClient(server.baseUrl).answerEpic(
                epicId = "example-repo/example-epic",
                questionId = "Q1",
                text = "Use lane B.",
                choice = "lane-b",
            )

            assertEquals(StepCallResult.Landed, result)
            val request = server.requests.single()
            assertEquals(EPICS_ANSWER, request.rawPath)
            val sent = EpicsAnswerRequest.parseFrom(request.body)
            assertEquals("Q1", sent.question)
            assertEquals("lane-b", sent.choice)
            assertEquals("Use lane B.", sent.text)
        }
    }

    private val epicProto = Epic.newBuilder()
        .setId("example-repo/example-epic")
        .setRepository("example-repo")
        .setSlug("example-epic")
        .setOutcome("in progress")
        .setChanged(4102444800L)
        .build()

    private companion object {
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_ANSWER = "/orrery.v1.Epics/Answer"
    }
}
