package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.WindowKind
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

@Composable
fun NewWindowButton(
    paneId: String,
    client: OrreryClient,
    onOpened: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    fun open(kind: WindowKind) {
        expanded = false
        failed = false
        scope.launch {
            try {
                withContext(Dispatchers.IO) { client.openWindow(paneId, kind) }
                onOpened()
            } catch (error: IOException) {
                failed = true
            }
        }
    }

    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box {
            TextButton(onClick = { expanded = true }) {
                Text("New")
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                DropdownMenuItem(
                    text = { Text("Claude pane") },
                    onClick = { open(WindowKind.CLAUDE) },
                )
                DropdownMenuItem(
                    text = { Text("Shell") },
                    onClick = { open(WindowKind.SHELL) },
                )
            }
        }
        if (failed) {
            Text("could not open", color = MaterialTheme.colorScheme.error)
        }
    }
}
