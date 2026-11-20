package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * A move of an epic, as the transcript carries it: [Left] for the conversation the epic left, which names the session
 * it went to and who moved it as the block's body names them, and [Took] for the conversation that now steers it.
 */
sealed interface MoveLineEvent {
    val epic: String

    data class Left(override val epic: String, val to: String, val by: String) : MoveLineEvent
    data class Took(override val epic: String) : MoveLineEvent
}

// The mover the block names for the owner. The line says "you" for it.
private const val OWNER_MOVER = "the owner"

// movedIdPattern matches the id of a moved block, epic:<epic>:A<seq>, and captures the epic.
private val movedIdPattern = Regex("""epic:(.+):A\d+""")

// leftBodyPattern matches the body of the block the conversation the epic left gets: the epic's id and title, the
// session it went to, and the mover.
private val leftBodyPattern = Regex(
    """Epic \S+ \([\s\S]*\) moved to session (\S+), by (the owner|orreryd|session \S+)\.""",
)

// movedBlockPattern matches a moved event block as enginehost writes it: the opening tag with its kind and id, the
// body on lines of its own, and the closing tag.
private val movedBlockPattern = Regex("""<orrery-event kind="moved" id="([^"]*)">\n([\s\S]*)\n</orrery-event>""")

/**
 * Reads [text] as a moved event block. The conversation the epic left gets [MoveLineEvent.Left]; any other body, the
 * steering prompt the conversation that took the epic gets, gets [MoveLineEvent.Took]. It is null for any other text
 * and for a moved block whose id is not epic:<epic>:A<seq>.
 */
fun moveLineOf(text: String): MoveLineEvent? {
    val match = movedBlockPattern.matchEntire(text.trim()) ?: return null
    val (id, body) = match.destructured
    val epic = movedIdPattern.matchEntire(id)?.groupValues?.get(1) ?: return null
    val left = leftBodyPattern.matchEntire(body)
    return if (left != null) {
        val (to, by) = left.destructured
        MoveLineEvent.Left(epic, to, by)
    } else {
        MoveLineEvent.Took(epic)
    }
}

/** The words of a move line, which name the epic by its slug, the part of its id after the slash. */
private fun moveLineWords(event: MoveLineEvent): String {
    val slug = event.epic.substringAfter('/')
    return when (event) {
        is MoveLineEvent.Left -> {
            val who = if (event.by == OWNER_MOVER) "you" else event.by
            "$slug moved to session ${event.to}, by $who. Its questions go there now."
        }
        is MoveLineEvent.Took -> "Took over $slug. Its questions and its ending come here now."
    }
}

/** The line a move draws in the transcript in place of its prompt block. */
@Composable
fun MoveLine(event: MoveLineEvent, modifier: Modifier = Modifier) {
    Text(
        text = moveLineWords(event),
        modifier = modifier
            .fillMaxWidth()
            .padding(
                start = OrreryTokens.Space.twelve,
                end = OrreryTokens.Space.twelve,
                top = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                bottom = OrreryTokens.Space.twelve,
            )
            .testTag("move-line"),
        color = OrreryTokens.Ink.two,
        style = OrreryTokens.Type.body,
    )
}
