package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane

/** The states of a headless session that has ended and cannot take an epic's questions. */
internal const val STEER_STOPPED = "stopped"
internal const val STEER_FAILED = "failed"

/**
 * Whether a conversation in [dir] belongs to the project of [repository]: the repository itself, a path under it, or a
 * path under its worktree directories, `<repository>.worktrees/` and `<repository>-worktrees/`. An empty repository
 * belongs to no project.
 */
fun inProject(dir: String, repository: String): Boolean {
    if (repository.isEmpty()) return false
    return dir == repository ||
        dir.startsWith("$repository/") ||
        dir.startsWith("$repository.worktrees/") ||
        dir.startsWith("$repository-worktrees/")
}

/** The pane or headless session whose orrery id is [steering]; null for an empty id or when none carries it. */
fun steererOf(panes: List<Pane>, steering: String): Pane? {
    if (steering.isEmpty()) return null
    return panes.firstOrNull { it.orreryId == steering }
}

/**
 * Whether [pane] is a live conversation the steering menu can offer: it carries an orrery id, the id is not in
 * [archived], and it is running. A headless session is live unless it is stopped or failed; a tmux pane is live while
 * its state is set.
 */
fun isLiveConversation(pane: Pane, archived: Set<String>): Boolean {
    if (pane.orreryId.isEmpty() || pane.orreryId in archived) return false
    return if (pane.headless) {
        pane.state != STEER_STOPPED && pane.state != STEER_FAILED
    } else {
        pane.state.isNotEmpty()
    }
}

/**
 * The steering menu's rows: [current] is the session that steers the epic, and [others] are the live conversations it
 * can move to.
 */
data class SteerChoices(val current: Pane?, val others: List<Pane>)

/**
 * The steering menu for an epic steered by [steering] in [repository]. [others] are the live conversations of the
 * project, in the order of [panes], other than the steerer.
 */
fun steerChoices(panes: List<Pane>, archived: Set<String>, steering: String, repository: String): SteerChoices =
    SteerChoices(
        current = steererOf(panes, steering),
        others = panes.filter {
            isLiveConversation(it, archived) && inProject(it.cwd, repository) && it.orreryId != steering
        },
    )

/** Whether the conversation that steers an epic has ended: it is unlisted, or a headless session that stopped or failed. */
fun conversationEnded(panes: List<Pane>, steering: String): Boolean {
    if (steering.isEmpty()) return false
    val steerer = steererOf(panes, steering) ?: return true
    return steerer.headless && (steerer.state == STEER_STOPPED || steerer.state == STEER_FAILED)
}

/** Whether [epic] is a live epic orreryd runs, so a conversation can steer it. */
fun steersLive(epic: EpicRow): Boolean = epic.live
