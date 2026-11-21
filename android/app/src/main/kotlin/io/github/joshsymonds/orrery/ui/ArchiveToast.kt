package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The line under the toast when Undo's call does not answer. */
private const val UNDO_NO_ANSWER = "Could not bring it back: the server did not answer."

/** The line under the toast when Undo's call is refused. */
private const val UNDO_REFUSED = "Could not bring it back: the server refused it."

/** One archive the toast is for: the row's title, and the kind and id Epics.Unarchive names the row by. */
internal class ArchivedRow(val title: String, val kind: String, val id: String)

/**
 * The toast's state: the archive it is for, null when no toast shows, and the line a failed Undo left under it. A [show]
 * replaces both, so one archive's toast never carries another's failure.
 */
class ArchiveToastState internal constructor() {
    internal var shown by mutableStateOf<ArchivedRow?>(null)
    internal var failure by mutableStateOf<String?>(null)

    /** Shows the toast for the archive of the row titled [title], which Epics.Unarchive names by [kind] and [id]. */
    fun show(title: String, kind: String, id: String) {
        shown = ArchivedRow(title, kind, id)
        failure = null
    }

    /** Ends the toast when it still shows [entry], the archive Undo has brought back. */
    internal fun undone(entry: ArchivedRow) {
        if (shown === entry) {
            shown = null
            failure = null
        }
    }

    /** Shows [line] under the toast when it still shows [entry], the archive whose Undo did not go through. */
    internal fun undoFailed(entry: ArchivedRow, line: String) {
        if (shown === entry) failure = line
    }
}

/** The toast state the composition remembers, for the page that shows the toast. */
@Composable
fun rememberArchiveToast(): ArchiveToastState = remember { ArchiveToastState() }

/**
 * The toast an archive from a row's sheet shows: one line, `Archived “<title>”.`, and Undo at its end. Undo makes
 * Epics.Unarchive for the archived row. A landed Undo ends the toast and runs [onUndone], which reads the list again so
 * the row returns. An Undo that does not land keeps the toast and its Undo, and says why under the line. Nothing ends the
 * toast on a timer: it stays until an Undo lands, another archive replaces it, or the page that shows it is left.
 */
@Composable
fun ArchiveToast(
    state: ArchiveToastState,
    client: OrreryClient,
    onUndone: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val scope = rememberCoroutineScope()
    val shown = state.shown ?: return
    val failure = state.failure
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen, end = OrreryTokens.Space.sixteen, bottom = OrreryTokens.Space.sixteen)
            .testTag("archive-toast")
            .shadow(
                elevation = OrreryTokens.Elevation.Overlay.y,
                shape = shape,
                ambientColor = OrreryTokens.Elevation.Overlay.color.copy(alpha = OrreryTokens.Elevation.Overlay.alpha),
                spotColor = OrreryTokens.Elevation.Overlay.color.copy(alpha = OrreryTokens.Elevation.Overlay.alpha),
            )
            .background(OrreryTokens.Surface.pressed, shape)
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.twelve),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                "Archived “${shown.title}”.",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.one,
                modifier = Modifier.weight(1f).testTag("archive-toast-line"),
            )
            TextButton(
                onClick = {
                    scope.launch {
                        val line = withContext(Dispatchers.IO) { unarchiveLineOf(client, shown) }
                        if (line == null) {
                            state.undone(shown)
                            onUndone()
                        } else {
                            state.undoFailed(shown, line)
                        }
                    }
                },
                modifier = Modifier.testTag("archive-toast-undo"),
            ) {
                Text("Undo")
            }
        }
        failure?.let { line ->
            Text(
                line,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Role.attention,
                modifier = Modifier.testTag("archive-toast-failed"),
            )
        }
    }
}

/**
 * Makes Epics.Unarchive for [entry] on the calling thread. Null when it landed; otherwise the line the toast shows under
 * its line.
 */
private fun unarchiveLineOf(client: OrreryClient, entry: ArchivedRow): String? = try {
    when (client.unarchive(entry.kind, entry.id)) {
        StepCallResult.Landed -> null
        is StepCallResult.Refused -> UNDO_REFUSED
    }
} catch (error: IOException) {
    UNDO_NO_ANSWER
}
