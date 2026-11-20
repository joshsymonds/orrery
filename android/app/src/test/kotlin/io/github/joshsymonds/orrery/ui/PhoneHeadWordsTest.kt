package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private fun epic(id: String, slug: String) = EpicRow(
    id = id,
    repository = "repo-a",
    slug = slug,
    outcome = "",
    changed = 0L,
    live = true,
    runningSteps = emptyList(),
    endedSteps = 0,
    problem = "",
)

private fun line(epicId: String, slug: String) = AskLine(epicId, slug, "asks: Which?", AskWay.ANSWER)

private fun task(type: String, status: String) = BackgroundTask(id = "t", type = type, status = status, description = "")

private fun pane(state: String, putOff: Boolean = false) = Pane(
    id = "%3",
    host = "box",
    session = "example-session",
    window = 1,
    pane = 2,
    cwd = "/work/api",
    command = "claude",
    state = state,
    summary = "",
    label = "api",
    changed = 0L,
    seen = 0L,
    question = "",
    questionHash = "",
    putOff = putOff,
)

class PhoneHeadWordsTest {
    private val rota = epic("repo-a/rota-export", "rota-export")
    private val orbit = epic("repo-a/orbit", "orbit")

    @Test
    fun askingWordsIsEmptyWhenNothingAsks() {
        assertEquals("", askingWords(null, emptyList()))
    }

    @Test
    fun askingWordsNamesTheConversationForItsOwnQuestionAlone() {
        assertEquals("orrery asks", askingWords("orrery", emptyList()))
    }

    @Test
    fun askingWordsNamesTheSlugOfOneEpic() {
        assertEquals("rota-export asks", askingWords(null, listOf(line(rota.id, rota.slug))))
    }

    @Test
    fun askingWordsCountsTwoAsksToYou() {
        val lines = listOf(line("repo-a/rota", "rota"), line(orbit.id, orbit.slug))
        assertEquals("2 ask you", askingWords(null, lines))
    }

    @Test
    fun askingWordsCountsTheOwnQuestionWithTheEpics() {
        assertEquals("2 ask you", askingWords("orrery", listOf(line(rota.id, rota.slug))))
    }

    @Test
    fun ownAsksWhenWaitingAndNotPutOff() {
        assertTrue(ownAsks(pane("waiting")))
    }

    @Test
    fun ownAsksNotWhenNotWaiting() {
        assertFalse(ownAsks(pane("working")))
    }

    @Test
    fun ownAsksNotWhenPutOff() {
        assertFalse(ownAsks(pane("waiting", putOff = true)))
    }

    @Test
    fun ownAsksNotWithNoPane() {
        assertFalse(ownAsks(null))
    }

    @Test
    fun headBadgesOrdersEpicsThenAgentsThenShellsWithSlugBeforeTheDashAndAsks() {
        val background = listOf(
            task("agent", "running"),
            task("agent", "done"),
            task("shell", "running"),
            task("shell", "running"),
            task("agent", "running"),
        )
        val badges = headBadges(listOf(rota, orbit), listOf(line(rota.id, rota.slug)), background)
        assertEquals(
            listOf(
                HeadBadge(key = rota.id, label = "rota", count = 0, asks = true),
                HeadBadge(key = orbit.id, label = "orbit", count = 0, asks = false),
                HeadBadge(key = "agents", label = "agents", count = 2, asks = false),
                HeadBadge(key = "running", label = "running", count = 2, asks = false),
            ),
            badges,
        )
    }

    @Test
    fun headBadgesDrawsNoAgentsOrRunningBadgeWithoutRunningTasks() {
        val badges = headBadges(listOf(orbit), emptyList(), listOf(task("agent", "failed"), task("shell", "stopped")))
        assertEquals(listOf(HeadBadge(key = orbit.id, label = "orbit", count = 0, asks = false)), badges)
    }
}
