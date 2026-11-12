package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.PickRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private enum class PickerWork { IDLE, ASKING, RUNNING }

/** The message a failed call shows, or a fixed phrase when the failure carries none. */
private fun Exception.reason(): String = message ?: "request failed"

/**
 * The picker: text in a field, asked of the server, and the rows it answers. Ask sends the
 * non-blank text and shows a working mark until rows or a failure arrive. Tapping a row runs
 * exactly that row and reports the pane it opened through [onOpened]. A failure shows its reason
 * and leaves the panel usable. Cancel calls [onClose].
 */
@Composable
fun PickerPanel(
    client: OrreryClient,
    onOpened: (String) -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var text by remember { mutableStateOf("") }
    var rows by remember { mutableStateOf<List<PickRow>?>(null) }
    var work by remember { mutableStateOf(PickerWork.IDLE) }
    var failure by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val canAsk = text.isNotBlank() && work == PickerWork.IDLE

    fun ask() {
        val asked = text
        work = PickerWork.ASKING
        failure = null
        rows = null
        scope.launch {
            var answer: List<PickRow>? = null
            try {
                answer = withContext(Dispatchers.IO) { client.ask(asked) }
            } catch (error: IOException) {
                failure = error.reason()
            }
            rows = answer
            work = PickerWork.IDLE
        }
    }

    fun run(row: PickRow) {
        work = PickerWork.RUNNING
        failure = null
        scope.launch {
            var pane: String? = null
            try {
                pane = withContext(Dispatchers.IO) { client.run(row.token) }
            } catch (error: IOException) {
                failure = error.reason()
            }
            work = PickerWork.IDLE
            pane?.let(onOpened)
        }
    }

    Column(
        modifier = modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        OutlinedTextField(
            value = text,
            onValueChange = { text = it },
            modifier = Modifier.fillMaxWidth().testTag("picker-text"),
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { if (canAsk) ask() }),
        )
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextButton(onClick = onClose, modifier = Modifier.testTag("picker-cancel")) {
                Text("Cancel")
            }
            Button(
                onClick = { ask() },
                enabled = canAsk,
                modifier = Modifier.testTag("picker-ask"),
            ) {
                Text("Ask")
            }
        }
        if (work != PickerWork.IDLE) {
            Text("working...", modifier = Modifier.testTag("picker-working"))
        }
        failure?.let { reason ->
            Text(reason, modifier = Modifier.testTag("picker-error"), color = Mocha.Red)
        }
        rows?.forEachIndexed { index, row ->
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("picker-row:$index")
                    .clickable(enabled = work == PickerWork.IDLE) { run(row) }
                    .padding(vertical = 6.dp),
            ) {
                Text(row.kind, style = MaterialTheme.typography.labelSmall, color = Mocha.Lavender)
                Text(row.dir, style = MaterialTheme.typography.bodySmall, color = Mocha.Subtext0)
                if (row.label.isNotBlank()) {
                    Text(row.label, style = MaterialTheme.typography.bodyLarge, color = Mocha.Text)
                }
                if (row.note.isNotBlank()) {
                    Text(row.note, style = MaterialTheme.typography.bodyMedium, color = Mocha.Text)
                }
                if (row.prompt) {
                    Text("first prompt", style = MaterialTheme.typography.labelSmall, color = Mocha.Yellow)
                }
            }
        }
    }
}
