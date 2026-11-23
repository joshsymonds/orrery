package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
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
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The line under the toast when Undo's call does not answer. */
private const val UNDO_NO_ANSWER = "Could not bring it back: the server did not answer."

/** The line under the toast when Undo's call is refused. */
private const val UNDO_REFUSED = "Could not bring it back: the server refused it."

/**
 * What the foot's one slot shows: a hand archive's toast, or the notice of the moves an answer made. Each entry is told
 * apart by identity, so an Undo or a Show ends only the entry it was taken from.
 */
internal sealed interface FootEntry

/**
 * One archive the toast is for: the row's title, the kind and id Epics.Unarchive names the row by, and the [key] the row
 * is drawn by, which Undo hands back to the page that keeps the moves ("" when the row has none).
 */
internal class ArchivedRow(val title: String, val kind: String, val id: String, val key: String) : FootEntry

/**
 * The notice of the moves one answer made, in the answer's order. [brought] holds the keys of the rows an Undo has
 * unarchived, which a later Undo does not send again; it is read and written on the main thread only.
 */
internal class MoveNotice(val moves: List<Moved>) : FootEntry {
    val brought: MutableSet<String> = HashSet()
}

/**
 * The toast's state: the entry the foot shows, null when none shows, and the line a failed Undo left under it. A [show]
 * or a move notice replaces both, so one entry's failure never carries over to another.
 */
class ArchiveToastState internal constructor() {
    internal var shown by mutableStateOf<FootEntry?>(null)
    internal var failure by mutableStateOf<String?>(null)

    /** Shows the toast for the archive of the row titled [title], which Epics.Unarchive names by [kind] and [id]. */
    fun show(title: String, kind: String, id: String) = showArchive(title, kind, id, key = "")

    /**
     * Shows the toast for the archive of the row titled [title], drawn at [key], which Epics.Unarchive names by [kind] and
     * [id].
     */
    internal fun showArchive(title: String, kind: String, id: String, key: String) {
        shown = ArchivedRow(title, kind, id, key)
        failure = null
    }

    /** Shows the notice of [moves], which an answer made, in the slot in place of whatever it showed. */
    internal fun showMoves(moves: List<Moved>) {
        shown = MoveNotice(moves)
        failure = null
    }

    /** Takes [entry] away when it still shows: its Undo landed, or its Show was taken. */
    internal fun ended(entry: FootEntry) {
        if (shown === entry) {
            shown = null
            failure = null
        }
    }

    /** Shows [line] under the entry when it still shows [entry], an Undo that did not go through. */
    internal fun failed(entry: FootEntry, line: String) {
        if (shown === entry) failure = line
    }
}

/** The toast state the composition remembers, for the page that shows the toast. */
@Composable
fun rememberArchiveToast(): ArchiveToastState = remember { ArchiveToastState() }

/**
 * The toast an archive from a row's sheet shows: one line, `Archived “<title>”.`, and Undo at its end. Undo makes
 * Epics.Unarchive for the archived row. A landed Undo ends the toast, hands the row's key to [onRowsBack] and runs
 * [onUndone], which reads the list again so the row returns. An Undo that does not land keeps the toast and its Undo, and
 * says why under the line.
 *
 * The same slot shows the move notice of an answer that moved rows, in place of any toast: one line that names the moved
 * rows and their places, with Show, which ends the notice and runs [onShow] with the place of the first named row, and
 * Undo, which makes Epics.Unarchive for every moved row that it has not already brought back. A landed Undo hands the
 * brought-back rows' keys to [onRowsBack] before it runs [onUndone]; an Undo that does not land for every row keeps the
 * notice, says why under it, and reads the list again when any row came back.
 *
 * Nothing ends the slot on a timer: it stays until an Undo lands, a Show is taken, another entry replaces it, or the page
 * that shows it is left.
 */
@Composable
internal fun ArchiveToast(
    state: ArchiveToastState,
    client: OrreryClient,
    onUndone: () -> Unit,
    modifier: Modifier = Modifier,
    onRowsBack: (keys: List<String>) -> Unit = {},
    onShow: (Spot) -> Unit = {},
) {
    val scope = rememberCoroutineScope()
    val shown = state.shown ?: return
    val failure = state.failure
    when (shown) {
        is ArchivedRow -> ArchiveLine(shown, failure, state, client, scope, onUndone, onRowsBack, modifier)
        is MoveNotice -> MoveNoticeLine(shown, failure, state, client, scope, onUndone, onRowsBack, onShow, modifier)
    }
}

/** The archive toast's line: `Archived “<title>”.` and Undo, with the failure of an Undo under it. */
@Composable
private fun ArchiveLine(
    shown: ArchivedRow,
    failure: String?,
    state: ArchiveToastState,
    client: OrreryClient,
    scope: CoroutineScope,
    onUndone: () -> Unit,
    onRowsBack: (keys: List<String>) -> Unit,
    modifier: Modifier,
) {
    ToastFrame(tag = "archive-toast", modifier = modifier) {
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
                        val line = withContext(Dispatchers.IO) { unarchiveLineOf(client, shown.kind, shown.id) }
                        if (line == null) {
                            state.ended(shown)
                            onRowsBack(listOf(shown.key))
                            onUndone()
                        } else {
                            state.failed(shown, line)
                        }
                    }
                },
                modifier = Modifier.testTag("archive-toast-undo"),
            ) {
                Text("Undo")
            }
        }
        failure?.let { line ->
            FailureLine(line, tag = "archive-toast-failed")
        }
    }
}

/**
 * The move notice: the line the moves read as, Show and Undo under it at the foot's end, and the failure of an Undo under
 * those. The actions sit on a row of their own, so the notice draws the same at any width.
 */
@Composable
private fun MoveNoticeLine(
    notice: MoveNotice,
    failure: String?,
    state: ArchiveToastState,
    client: OrreryClient,
    scope: CoroutineScope,
    onUndone: () -> Unit,
    onRowsBack: (keys: List<String>) -> Unit,
    onShow: (Spot) -> Unit,
    modifier: Modifier,
) {
    ToastFrame(tag = "move-notice", modifier = modifier) {
        Text(
            moveNoticeText(notice.moves),
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.one,
            modifier = Modifier.fillMaxWidth().testTag("move-notice-line"),
        )
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.End,
        ) {
            TextButton(
                onClick = {
                    state.ended(notice)
                    onShow(notice.moves.first().to)
                },
                modifier = Modifier.testTag("move-notice-show"),
            ) {
                Text("Show")
            }
            TextButton(
                onClick = {
                    val pending = notice.moves.filter { it.key !in notice.brought }
                    scope.launch {
                        val outcome = withContext(Dispatchers.IO) { unarchiveMovesOf(client, pending) }
                        notice.brought.addAll(outcome.landed)
                        if (outcome.landed.isNotEmpty()) onRowsBack(outcome.landed)
                        val line = outcome.failure
                        if (line == null) {
                            state.ended(notice)
                            onUndone()
                        } else {
                            state.failed(notice, line)
                            if (outcome.landed.isNotEmpty()) onUndone()
                        }
                    }
                },
                modifier = Modifier.testTag("move-notice-undo"),
            ) {
                Text("Undo")
            }
        }
        failure?.let { line ->
            FailureLine(line, tag = "move-notice-failed")
        }
    }
}

/** The frame both toasts draw in: the card at the foot, tagged [tag], with its content in a column. */
@Composable
private fun ToastFrame(tag: String, modifier: Modifier, content: @Composable ColumnScope.() -> Unit) {
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen, end = OrreryTokens.Space.sixteen, bottom = OrreryTokens.Space.sixteen)
            .testTag(tag)
            .shadow(
                elevation = OrreryTokens.Elevation.Overlay.y,
                shape = shape,
                ambientColor = OrreryTokens.Elevation.Overlay.color.copy(alpha = OrreryTokens.Elevation.Overlay.alpha),
                spotColor = OrreryTokens.Elevation.Overlay.color.copy(alpha = OrreryTokens.Elevation.Overlay.alpha),
            )
            .background(OrreryTokens.Surface.pressed, shape)
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.twelve),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
        content = content,
    )
}

/** The line a failed Undo leaves under the toast's line, in the attention role. */
@Composable
private fun FailureLine(line: String, tag: String) {
    Text(
        line,
        style = OrreryTokens.Type.small,
        color = OrreryTokens.Role.attention,
        modifier = Modifier.testTag(tag),
    )
}

/** What an Undo of a move notice did: the keys of the rows it brought back, and the line of the first call that failed. */
private class UndoOutcome(val landed: List<String>, val failure: String?)

/**
 * Makes Epics.Unarchive for each of [moves] on the calling thread, each row by its own kind and id. A tmux pane with no
 * orrery_id is skipped. Every row is tried, so one refused row does not hold back the others; the outcome's failure is
 * the first line any call gave.
 */
private fun unarchiveMovesOf(client: OrreryClient, moves: List<Moved>): UndoOutcome {
    val landed = mutableListOf<String>()
    var failure: String? = null
    for (moved in moves) {
        val (kind, id) = unarchiveTargetOf(moved.pane) ?: continue
        val line = unarchiveLineOf(client, kind, id)
        if (line == null) {
            landed.add(moved.key)
        } else if (failure == null) {
            failure = line
        }
    }
    return UndoOutcome(landed, failure)
}

/**
 * Makes Epics.Unarchive for the row of [kind] named [id] on the calling thread. Null when it landed; otherwise the line
 * the toast shows under its line.
 */
private fun unarchiveLineOf(client: OrreryClient, kind: String, id: String): String? = try {
    when (client.unarchive(kind, id)) {
        StepCallResult.Landed -> null
        is StepCallResult.Refused -> UNDO_REFUSED
    }
} catch (error: IOException) {
    UNDO_NO_ANSWER
}
