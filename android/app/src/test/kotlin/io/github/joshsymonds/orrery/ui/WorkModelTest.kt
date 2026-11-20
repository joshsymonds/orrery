package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicRow
import org.junit.Assert.assertEquals
import org.junit.Test

class WorkModelTest {
    @Test
    fun shortNameDropsTheRepositoryIdSuffixBeforeTheSlugsFirstWord() {
        val epic = epic(repository = "orrery-0668c87", slug = "orrery-right-rail")
        assertEquals("right", shortNameOfEpic(epic))
    }

    @Test
    fun shortNameDropsARepositoryNameWithNoSuffix() {
        val epic = epic(repository = "lighthouse", slug = "lighthouse-rota")
        assertEquals("rota", shortNameOfEpic(epic))
    }

    private fun shortNameOfEpic(epic: EpicRow): String {
        val groups = workGroupsOf(
            session = "s1",
            pane = true,
            epics = listOf(epic),
            details = emptyMap(),
            background = emptyList(),
        )
        return (groups.single().things.single() as EpicThing).short
    }

    private fun epic(repository: String, slug: String): EpicRow = EpicRow(
        id = "$repository/$slug",
        repository = repository,
        slug = slug,
        outcome = "",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        steering = "s1",
    )
}
