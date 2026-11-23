package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.SemanticsPropertyReceiver
import io.github.joshsymonds.orrery.api.Pane

/**
 * The line of the inbox a conversation is drawn on: its tree under a project, the project's ended line, or the archived
 * view.
 */
internal enum class RowLine { TREE, ENDED, ARCHIVED }

/** Where a conversation is drawn: the project it sits under, by its key and name, and the line it is drawn on. */
internal data class Spot(val project: String /* Project.key */, val name: String /* Project.name */, val line: RowLine)

/** A conversation's pane, with the [spot] it is drawn at. */
internal data class Placed(val pane: Pane, val spot: Spot)

/**
 * A conversation found in both reads whose spot differs: [key] is its Convo.key, [pane] its pane in the later read, and
 * [from] and [to] the spots it was drawn at before and after.
 */
internal data class Moved(val key: String, val pane: Pane, val from: Spot, val to: Spot)

/**
 * Where each conversation with a pane is drawn, keyed by its Convo.key: a conversation in [inbox] is on the tree line
 * of its project, or on the ended line; one in [archived] is on the archived line. Stand-in rows and conversations with
 * no pane are left out.
 */
internal fun spotsOf(inbox: List<Project>, archived: List<Project>): Map<String, Placed> {
    val spots = LinkedHashMap<String, Placed>()
    fun place(project: Project, convos: List<Convo>, line: RowLine) {
        for (convo in convos) {
            val pane = convo.pane ?: continue
            spots[convo.key] = Placed(pane, Spot(project.key, project.name, line))
        }
    }
    for (project in inbox) {
        place(project, project.convos, RowLine.TREE)
        place(project, project.ended, RowLine.ENDED)
    }
    for (project in archived) {
        place(project, project.convos, RowLine.ARCHIVED)
    }
    return spots
}

/**
 * The rows found in both [before] and [after] whose spot differs, in [after]'s order. A row found in only one of the two
 * maps is not a move.
 */
internal fun movesBetween(before: Map<String, Placed>, after: Map<String, Placed>): List<Moved> = buildList {
    for ((key, placed) in after) {
        val previous = before[key] ?: continue
        if (previous.spot != placed.spot) add(Moved(key, placed.pane, previous.spot, placed.spot))
    }
}

/** The line the move notice shows for [moves]. */
internal fun moveNoticeText(moves: List<Moved>): String = ""

/** The semantics flag a moving row or ended line carries. */
internal val Moving = SemanticsPropertyKey<Boolean>("Moving")

/** Whether a row or ended line carries [Moving]: it is moving to a new place. */
internal var SemanticsPropertyReceiver.moving by Moving
