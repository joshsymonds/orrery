package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

private const val STEER_ITEM = "Steer it from another conversation"

/**
 * The record's More: three dots in the title row, which open a menu of one command, "Steer it from another
 * conversation". The command opens the steering menu of the epic as a sheet. Its one [SteerSend] serves the sheet, and
 * a steering it settles on shuts the sheet and is reported to [onSteered].
 */
@Composable
fun EpicSteerMore(client: OrreryClient, detail: EpicDetail, onSteered: (String) -> Unit) {
    var menuOpen by remember { mutableStateOf(false) }
    var sheetOpen by remember { mutableStateOf(false) }
    val send = rememberSteerSend(client, detail.epic.id) { steering ->
        sheetOpen = false
        onSteered(steering)
    }
    Box {
        IconButton(
            onClick = { menuOpen = true },
            modifier = Modifier.testTag("epic-more").semantics { contentDescription = "More" },
        ) {
            Text("⋯", color = OrreryTokens.Ink.two, style = OrreryTokens.Type.title)
        }
        DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
            DropdownMenuItem(
                text = { Text(STEER_ITEM) },
                onClick = {
                    menuOpen = false
                    sheetOpen = true
                },
                modifier = Modifier.testTag("epic-steer"),
            )
        }
    }
    if (sheetOpen) {
        SteerMenuSheet(
            client = client,
            epicId = detail.epic.id,
            slug = detail.epic.slug,
            repository = detail.repository,
            steering = detail.epic.steering,
            send = send,
            onDismiss = { sheetOpen = false },
        )
    }
}
