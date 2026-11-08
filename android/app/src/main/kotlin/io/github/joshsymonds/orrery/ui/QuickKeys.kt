package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val NOT_SENT = "key not sent"

/** Label shown on the button, paired with the tmux key name it posts. */
private val QUICK_KEYS = listOf(
    "1" to "1",
    "2" to "2",
    "3" to "3",
    "Esc" to "Escape",
    "Ctrl-C" to "C-c",
    "←" to "Left",
    "↑" to "Up",
    "↓" to "Down",
    "→" to "Right",
    "Tab" to "Tab",
)

/**
 * Unguarded quick-key row for a pane. Each button sends its key with no
 * expect_question, so it is not tied to the prompt currently shown.
 */
@Composable
fun QuickKeys(paneId: String, client: OrreryClient, modifier: Modifier = Modifier) {
    val scope = rememberCoroutineScope()
    var notSent by remember { mutableStateOf(false) }

    Column(modifier = modifier) {
        Row(
            modifier = Modifier.horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            QUICK_KEYS.forEach { (label, key) ->
                Button(onClick = {
                    scope.launch {
                        notSent = !sendKey(client, paneId, key)
                    }
                }) {
                    Text(label)
                }
            }
        }
        if (notSent) {
            Text(NOT_SENT)
        }
    }
}

private suspend fun sendKey(client: OrreryClient, paneId: String, key: String): Boolean =
    withContext(Dispatchers.IO) {
        try {
            client.sendKeys(paneId, listOf(key), expectQuestion = null) == KeysResult.Sent
        } catch (e: IOException) {
            false
        }
    }
