package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val NOT_SENT = "key not sent"
private const val WINDOW_MILLIS = 5_000L

/** Keys whose Sent answer opens a window in which a change of the pane shows. */
private val WINDOW_KEYS = setOf("Escape", "C-c")

/** The pane's state and changed time at the send that opened a window. */
private data class ReadWindow(val state: String, val changed: Long)

/** Label shown on the button, paired with the tmux key name it posts. */
private val QUICK_KEYS = listOf(
    "Esc" to "Escape",
    "Ctrl-C" to "C-c",
    "Enter" to "Enter",
    "←" to "Left",
    "↑" to "Up",
    "↓" to "Down",
    "→" to "Right",
)

/**
 * Unguarded quick-key row for a pane. Each button sends its key with no
 * expect_question, so it is not tied to the prompt currently shown. After Esc or
 * Ctrl-C is sent, a change of the pane's state or changed time within the window
 * shows what the pane is now.
 */
@Composable
fun QuickKeys(paneId: String, client: OrreryClient, state: String, changed: Long, modifier: Modifier = Modifier) {
    val scope = rememberCoroutineScope()
    var notSent by remember { mutableStateOf(false) }
    var result by remember { mutableStateOf<String?>(null) }
    var window by remember { mutableStateOf<ReadWindow?>(null) }
    var windowClose by remember { mutableStateOf<Job?>(null) }

    LaunchedEffect(state, changed, window) {
        val open = window ?: return@LaunchedEffect
        if (state != open.state || changed > open.changed) {
            result = resultFor(state)
            window = null
            windowClose?.cancel()
        }
    }

    Column(modifier = modifier) {
        Row(
            modifier = Modifier.horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            QUICK_KEYS.forEach { (label, key) ->
                val feedback = rememberTapFeedback()
                Button(
                    onClick = {
                        feedback.tap()
                        val sentAt = ReadWindow(state, changed)
                        scope.launch {
                            val sent = sendKey(client, paneId, key)
                            notSent = !sent
                            if (sent && key in WINDOW_KEYS) {
                                windowClose?.cancel()
                                window = sentAt
                                windowClose = scope.launch {
                                    delay(WINDOW_MILLIS)
                                    window = null
                                }
                            }
                        }
                    },
                    modifier = Modifier.tapFeedback(feedback),
                    colors = tapFeedbackColors(feedback),
                ) {
                    Text(label)
                }
            }
        }
        if (notSent) {
            Text(NOT_SENT)
        }
        result?.let { Text(it) }
    }
}

private fun resultFor(state: String): String = when (state) {
    "" -> "now ended"
    "waiting" -> "now waiting for you"
    else -> "now $state"
}

private suspend fun sendKey(client: OrreryClient, paneId: String, key: String): Boolean =
    withContext(Dispatchers.IO) {
        try {
            client.sendKeys(paneId, listOf(key), expectQuestion = null) == KeysResult.Sent
        } catch (e: IOException) {
            false
        }
    }
