package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.StepFacts
import org.junit.Assert.assertEquals
import org.junit.Test

class StepEndFactsTest {
    @Test
    fun noFactsDrawNoFactsLine() {
        assertEquals(emptyList<StepFact>(), stepFactsOf(null))
    }

    @Test
    fun countedFactsShowFilesThenLinesAddedAndRemovedThenTheTime() {
        val facts = StepFacts(rounds = 0, seconds = 45, counted = true, files = 2, added = 96, removed = 4)

        assertEquals(
            listOf(
                StepFact("2 files", FactKind.Plain),
                StepFact("+96", FactKind.Added),
                StepFact("−4", FactKind.Removed),
                StepFact("45s", FactKind.Plain),
            ),
            stepFactsOf(facts),
        )
    }

    @Test
    fun uncountedFactsShowTheTimeAloneAndNoFilesOrLines() {
        val facts = StepFacts(rounds = 0, seconds = 45, counted = false, files = 2, added = 96, removed = 4)

        assertEquals(listOf("45s"), textsOf(stepFactsOf(facts)))
    }

    @Test
    fun roundsShowOnlyWhenAboveZero() {
        assertEquals(listOf("8m"), textsOf(stepFactsOf(StepFacts(0, 480, false, 0, 0, 0))))
        assertEquals(listOf("1 round", "8m"), textsOf(stepFactsOf(StepFacts(1, 480, false, 0, 0, 0))))
        assertEquals(listOf("3 rounds", "8m"), textsOf(stepFactsOf(StepFacts(3, 480, false, 0, 0, 0))))
    }

    @Test
    fun timeShowsSecondsUnderAMinuteMinutesUnderAnHourAndHoursAfter() {
        assertEquals(listOf("45s"), textsOf(stepFactsOf(StepFacts(0, 45, false, 0, 0, 0))))
        assertEquals(listOf("8m"), textsOf(stepFactsOf(StepFacts(0, 480, false, 0, 0, 0))))
        assertEquals(listOf("1h 5m"), textsOf(stepFactsOf(StepFacts(0, 3900, false, 0, 0, 0))))
    }

    @Test
    fun timeShowsWheneverFactsArePresentEvenAtZero() {
        assertEquals(listOf("0s"), textsOf(stepFactsOf(StepFacts(0, 0, false, 0, 0, 0))))
    }

    private fun textsOf(facts: List<StepFact>): List<String> = facts.map { it.text }
}
