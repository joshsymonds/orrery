package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The line under a sheet's head when Panes.MarkSeen or Control.Seen does not go through. */
private const val MARK_SEEN_FAILED = "could not mark seen"

/** The line under a sheet's head when Epics.Archive or Epics.Unarchive does not go through. */
private const val ARCHIVE_FAILED = "could not change archive"

/** The row a long press on the inbox opened the sheet for: a conversation's pane or headless session, or an epic. */
internal sealed interface SheetRow {
    val title: String

    data class Conversation(val pane: Pane) : SheetRow {
        override val title: String get() = pane.label
    }

    data class Epic(val epic: EpicRow) : SheetRow {
        override val title: String get() = epic.slug
    }
}

/** What a row's sheet offers, each with the words it shows. */
internal enum class SheetItem(val label: String) {
    MARK_SEEN("Mark seen"),
    ARCHIVE("Archive"),
    UNARCHIVE("Unarchive"),
}

/**
 * The items a row's sheet offers, in order. Mark seen is offered for an unseen conversation. Archive, or Unarchive for
 * an archived one, is offered for an epic, a headless session, and a tmux pane with an orrery_id, the id it archives by.
 */
internal fun sheetItemsOf(row: SheetRow): List<SheetItem> = when (row) {
    is SheetRow.Conversation -> buildList {
        if (row.pane.unread()) add(SheetItem.MARK_SEEN)
        if (row.pane.headless || row.pane.orreryId.isNotEmpty()) add(archiveItemOf(row.pane.archived))
    }
    is SheetRow.Epic -> listOf(archiveItemOf(row.epic.archived))
}

private fun archiveItemOf(archived: Boolean): SheetItem = if (archived) SheetItem.UNARCHIVE else SheetItem.ARCHIVE

/**
 * The sheet a long press on the inbox opens: [row]'s title, a line under it when a call did not go through, and the
 * items [sheetItemsOf] offers. An item makes its call with the row's own kind and id, then runs [onLanded], which reads
 * the tree again. A landed archive first runs [onArchived] with the row's title and the kind and id it was archived by.
 * A call that does not land keeps the sheet open with its line. [onClose] dismisses the sheet.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun InboxSheet(
    row: SheetRow,
    client: OrreryClient,
    onArchived: (title: String, kind: String, id: String) -> Unit,
    onLanded: () -> Unit,
    onClose: () -> Unit,
) {
    val scope = rememberCoroutineScope()
    var failure by remember { mutableStateOf<String?>(null) }
    ModalBottomSheet(onDismissRequest = onClose) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            Text(row.title, style = OrreryTokens.Type.title, modifier = Modifier.testTag("sheet-title"))
            failure?.let { line ->
                Text(line, style = OrreryTokens.Type.small, color = OrreryTokens.Role.attention, modifier = Modifier.testTag("sheet-failed"))
            }
            for (item in sheetItemsOf(row)) {
                TextButton(
                    onClick = {
                        scope.launch {
                            val failed = withContext(Dispatchers.IO) { sendOf(client, row, item) }
                            if (failed == null) {
                                if (item == SheetItem.ARCHIVE) {
                                    val (kind, id) = archiveTargetOf(row)
                                    onArchived(row.title, kind, id)
                                }
                                onLanded()
                            } else {
                                failure = failed
                            }
                        }
                    },
                    modifier = Modifier.fillMaxWidth().testTag("sheet:${item.name}"),
                ) {
                    Text(item.label)
                }
            }
        }
    }
}

/**
 * Makes [item]'s call for [row] on the calling thread. Null when it landed; otherwise the line the sheet shows under its
 * head. Mark seen is offered only for a conversation, so [row] is one there.
 */
private fun sendOf(client: OrreryClient, row: SheetRow, item: SheetItem): String? = try {
    when (item) {
        SheetItem.MARK_SEEN -> {
            // A tmux pane is marked seen through the time of its newest change; a headless session by Control.Seen.
            val pane = (row as SheetRow.Conversation).pane
            if (pane.headless) client.sessionSeen(pane.id) else client.markSeen(pane.id, pane.changed)
            null
        }
        SheetItem.ARCHIVE, SheetItem.UNARCHIVE -> {
            val (kind, id) = archiveTargetOf(row)
            val result = if (item == SheetItem.ARCHIVE) client.archive(kind, id) else client.unarchive(kind, id)
            when (result) {
                StepCallResult.Landed -> null
                is StepCallResult.Refused -> "$ARCHIVE_FAILED: ${result.message}"
            }
        }
    }
} catch (error: IOException) {
    if (item == SheetItem.MARK_SEEN) MARK_SEEN_FAILED else ARCHIVE_FAILED
}

/**
 * The kind and id Epics.Archive and Epics.Unarchive name for [row]: a tmux pane by its orrery_id, a headless session by
 * its id, and an epic by its id.
 */
private fun archiveTargetOf(row: SheetRow): Pair<String, String> = when (row) {
    is SheetRow.Conversation -> if (row.pane.headless) "headless" to row.pane.id else "session" to row.pane.orreryId
    is SheetRow.Epic -> "epic" to row.epic.id
}
