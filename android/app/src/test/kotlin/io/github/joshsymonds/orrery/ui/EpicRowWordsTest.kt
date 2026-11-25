package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

// The three lines an epic's inbox row says, over synthetic epics at a synthetic clock.
class EpicRowWordsTest {
    private val now = 100_000L

    private fun epic(endedSteps: Int = 0, runningSteps: List<StepRow> = emptyList()) = EpicRow(
        id = "lighthouse/rota-export",
        repository = "/home/user/lighthouse",
        slug = "rota-export",
        outcome = "accepted",
        changed = 0L,
        live = true,
        runningSteps = runningSteps,
        endedSteps = endedSteps,
        phaseAt = now - 49 * 60,
    )

    private fun requirement(id: String, state: String) =
        EpicRequirementRow(id = id, text = "", evidence = "", kind = "check", state = state)

    private fun question(asked: Long) = EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = asked)

    private fun runningStep() = StepRow(
        id = "s1",
        task = "build",
        agent = "",
        end = "",
        status = "",
        held = false,
        started = now - 60,
        ended = 0L,
        doing = "running the suite",
        checks = emptyList(),
        reason = "",
    )

    private fun node(
        epic: EpicRow = epic(),
        requirements: List<EpicRequirementRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
        newest: String = "",
    ) = EpicNode(
        epic,
        EpicDetail(
            epic = epic,
            steps = emptyList(),
            requirements = requirements,
            questions = questions,
            newest = newest,
        ),
        contract = null,
    )

    private fun wordsOf(node: EpicNode): EpicRowWords = epicRowWords(node, now)

    @Test
    fun anEpicRowWordsShowsItsThreeLinesWithItsBlockerBeforeItsNewestSentence() {
        val words = wordsOf(
            node(
                requirements = listOf(
                    requirement("R1", "proven"),
                    requirement("R2", "in_flight"),
                    requirement("R3", "proven"),
                ),
                questions = listOf(question(now - 120)),
                newest = "Ran the migration against staging.",
            ),
        )

        assertEquals("rota-export · 2 of 3 proven", titleLineOf(words))
        assertEquals("planning · 49 min", words.words)
        assertEquals("? waits on your answer · 2 min", thirdLineOf(words))
    }

    @Test
    fun anEpicRowWordsThirdLineIsItsNewestSentenceWhenNothingBlocksIt() {
        val words = wordsOf(node(newest = "Wrote the release notes."))

        assertEquals("", words.blocker)
        assertEquals("Wrote the release notes.", thirdLineOf(words))
    }

    @Test
    fun anEpicRowWordsThirdLineIsEmptyWhenNothingBlocksAndNoNewestSentenceIsGiven() {
        assertEquals("", thirdLineOf(wordsOf(node())))
    }

    @Test
    fun anEpicRowWordsFirstLineReadsZeroOfZeroProvenWhenItHasNoRequirements() {
        assertEquals("rota-export · 0 of 0 proven", titleLineOf(wordsOf(node())))
    }

    @Test
    fun anEpicRowWordsReadsNoStepsEndedTextEvenWhileItsStepsHaveEnded() {
        val words = wordsOf(
            node(epic = epic(endedSteps = 3, runningSteps = listOf(runningStep())), newest = "Ran the tests."),
        )

        for (line in listOf(titleLineOf(words), words.words, thirdLineOf(words))) {
            assertFalse(line, line.contains("ended"))
        }
    }
}
