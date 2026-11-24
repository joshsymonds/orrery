package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SteerMenuModelTest {
    private val repository = "/home/owner/code/lighthouse"

    @Test
    fun steerChoicesMarksTheSteererCurrentAndOffersOnlyLiveInProjectConversationsInInputOrder() {
        val panes = listOf(
            headless("sess-steer", "/home/owner/code/lighthouse", "running"),
            headless("sess-live", "/home/owner/code/lighthouse", "waiting"),
            headless("sess-stopped", "/home/owner/code/lighthouse", "stopped"),
            headless("sess-failed", "/home/owner/code/lighthouse", "failed"),
            tmux("pane-empty", "/home/owner/code/lighthouse", orreryId = "sess-empty", state = ""),
            tmux("pane-unset", "/home/owner/code/lighthouse", orreryId = "", state = "idle"),
            tmux("pane-archived", "/home/owner/code/lighthouse", orreryId = "sess-pane-archived", state = "idle"),
            headless("sess-archived", "/home/owner/code/lighthouse", "running"),
            headless("sess-other", "/home/owner/code/rota", "running"),
            tmux(
                "pane-worktree",
                "/home/owner/code/lighthouse.worktrees/rota-export",
                orreryId = "sess-wt",
                state = "idle",
            ),
            tmux(
                "pane-worktree-dash",
                "/home/owner/code/lighthouse-worktrees/rota",
                orreryId = "sess-wt-dash",
                state = "idle",
            ),
            tmux("pane-subdir", "/home/owner/code/lighthouse/app", orreryId = "sess-sub", state = "busy"),
            tmux("pane-sibling", "/home/owner/code/lighthouse-notes", orreryId = "sess-sibling", state = "idle"),
            tmux("pane-plain", "/home/owner/code/lighthouse", orreryId = "sess-pane", state = "idle"),
        )
        val archived = setOf("sess-pane-archived", "sess-archived")

        val choices = steerChoices(panes, archived, "sess-steer", repository)

        assertEquals("sess-steer", choices.current?.orreryId)
        assertEquals(
            listOf("sess-live", "sess-wt", "sess-wt-dash", "sess-sub", "sess-pane"),
            choices.others.map { it.orreryId },
        )
    }

    @Test
    fun steerChoicesOffersNoRowsForANoRepository() {
        val panes = listOf(headless("sess-live", "/home/owner/code/lighthouse", "running"))

        assertTrue(steerChoices(panes, emptySet(), "", "").others.isEmpty())
    }

    @Test
    fun conversationEndedIsFalseForALiveSteerer() {
        val panes = listOf(headless("sess-steer", repository, "running"))

        assertFalse(conversationEnded(panes, "sess-steer"))
    }

    @Test
    fun conversationEndedIsTrueForAStoppedSteerer() {
        val panes = listOf(headless("sess-steer", repository, "stopped"))

        assertTrue(conversationEnded(panes, "sess-steer"))
    }

    @Test
    fun conversationEndedIsTrueForAFailedSteerer() {
        val panes = listOf(headless("sess-steer", repository, "failed"))

        assertTrue(conversationEnded(panes, "sess-steer"))
    }

    @Test
    fun conversationEndedIsTrueForAnUnlistedSteerer() {
        val panes = listOf(headless("sess-other", repository, "running"))

        assertTrue(conversationEnded(panes, "sess-steer"))
    }

    @Test
    fun conversationEndedIsFalseForNoSteerer() {
        val panes = listOf(headless("sess-other", repository, "running"))

        assertFalse(conversationEnded(panes, ""))
    }

    @Test
    fun steersLiveIsTrueOnlyForALiveEpic() {
        assertTrue(steersLive(epic(live = true)))
        assertFalse(steersLive(epic(live = false)))
    }

    private fun headless(id: String, cwd: String, state: String): Pane =
        pane(id = id, cwd = cwd, state = state, orreryId = id, headless = true)

    private fun tmux(id: String, cwd: String, orreryId: String, state: String): Pane =
        pane(id = id, cwd = cwd, state = state, orreryId = orreryId, headless = false)

    private fun pane(id: String, cwd: String, state: String, orreryId: String, headless: Boolean): Pane = Pane(
        id = id,
        host = "",
        session = "",
        window = 0,
        pane = 0,
        cwd = cwd,
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = 0L,
        seen = 0L,
        question = "",
        questionHash = "",
        headless = headless,
        orreryId = orreryId,
    )

    private fun epic(live: Boolean): EpicRow = EpicRow(
        id = "lighthouse/rota-export",
        repository = repository,
        slug = "lighthouse-rota-export",
        outcome = "",
        changed = 0L,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
    )
}
