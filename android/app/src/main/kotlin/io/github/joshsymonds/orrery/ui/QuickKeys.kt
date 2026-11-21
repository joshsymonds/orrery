package io.github.joshsymonds.orrery.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
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
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.spec
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val WINDOW_MILLIS = 5_000L
private const val NOTHING_CHANGED = "Nothing changed."

/** The server answers that mean no answer came back, so the key may or may not have landed. */
private val NO_ANSWER_CODES = setOf(Code.UNAVAILABLE, Code.DEADLINE_EXCEEDED, Code.UNKNOWN)

/** Keys whose Sent answer opens a window in which a change of the pane shows. */
private val WINDOW_KEYS = setOf("Escape", "C-c")

/** The pane's state and changed time at the send that opened a window, and the label of the key sent. */
private data class ReadWindow(val label: String, val state: String, val changed: Long)

/** Why a key was not sent: the server refused it, or no answer came back. */
private enum class NotSent(val reason: String) {
    Refused("the server refused it."),
    NoAnswer("the server did not answer."),
}

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
 * shows what the pane is now. When the window ends with no change seen, one read
 * of the pane shows nothing changed, or what the pane is now. A key not sent says
 * why, in the error tone, until a later key is sent. The row folds away while the
 * draft has text.
 */
@Composable
fun QuickKeys(
    paneId: String,
    client: OrreryClient,
    state: String,
    changed: Long,
    draftPresent: Boolean,
    modifier: Modifier = Modifier,
) {
    AnimatedVisibility(
        visible = !draftPresent,
        enter = expandVertically(Move.LAYOUT.spec(animated = true)) + fadeIn(Move.LAYOUT.spec(animated = true)),
        exit = shrinkVertically(Move.LAYOUT.spec(animated = true)) + fadeOut(Move.LAYOUT.spec(animated = true)),
        label = "quick-keys",
    ) {
        QuickKeysRow(paneId, client, state, changed, modifier)
    }
}

@Composable
private fun QuickKeysRow(paneId: String, client: OrreryClient, state: String, changed: Long, modifier: Modifier) {
    val scope = rememberCoroutineScope()
    var failure by remember { mutableStateOf<String?>(null) }
    var result by remember { mutableStateOf<String?>(null) }
    var window by remember { mutableStateOf<ReadWindow?>(null) }
    var windowClose by remember { mutableStateOf<Job?>(null) }

    LaunchedEffect(state, changed, window) {
        val open = window ?: return@LaunchedEffect
        if (state != open.state || changed > open.changed) {
            result = sentText(open.label, paneNow(state))
            window = null
            windowClose?.cancel()
        }
    }

    Column(modifier = modifier) {
        Row(
            modifier = Modifier.horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            QUICK_KEYS.forEach { (label, key) ->
                val feedback = rememberTapFeedback()
                Button(
                    onClick = {
                        feedback.tap()
                        result = null
                        window = null
                        windowClose?.cancel()
                        val sentAt = ReadWindow(label, state, changed)
                        scope.launch {
                            val notSent = sendKey(client, paneId, key)
                            failure = notSent?.let { "$label was not sent: ${it.reason}" }
                            if (notSent == null && key in WINDOW_KEYS) {
                                windowClose?.cancel()
                                window = sentAt
                                windowClose = scope.launch {
                                    delay(WINDOW_MILLIS)
                                    window = null
                                    result = readEnd(client, paneId, sentAt)
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
        failure?.let { InlineNote(it, NoteTone.Error) }
        result?.let { InlineNote(it, NoteTone.Ok) }
    }
}

/** What the pane is now, in the words a sent key's note ends with. */
private fun paneNow(state: String): String = when (state) {
    "" -> "The pane is now ended."
    "waiting" -> "The pane is now waiting for you."
    else -> "The pane is now $state."
}

private fun sentText(label: String, words: String): String = "$label sent. $words"

/**
 * The note a window shows when it ends with no change seen: nothing changed when the pane's state and changed
 * time are the window's, else what the pane is now. Null when the read fails or the pane is gone.
 */
private suspend fun readEnd(client: OrreryClient, paneId: String, sentAt: ReadWindow): String? {
    val pane = readPane(client, paneId) ?: return null
    val words = if (pane.state == sentAt.state && pane.changed == sentAt.changed) {
        NOTHING_CHANGED
    } else {
        paneNow(pane.state)
    }
    return sentText(sentAt.label, words)
}

private suspend fun readPane(client: OrreryClient, paneId: String): Pane? =
    withContext(Dispatchers.IO) {
        try {
            client.panes().firstOrNull { it.id == paneId }
        } catch (e: IOException) {
            null
        }
    }

/** Sends [key]; null when it was sent, else why it was not. */
private suspend fun sendKey(client: OrreryClient, paneId: String, key: String): NotSent? =
    withContext(Dispatchers.IO) {
        try {
            when (client.sendKeys(paneId, listOf(key), expectQuestion = null)) {
                KeysResult.Sent -> null
                KeysResult.PromptChanged -> NotSent.Refused
            }
        } catch (e: OrreryCallException) {
            if (e.code in NO_ANSWER_CODES) NotSent.NoAnswer else NotSent.Refused
        } catch (e: IOException) {
            NotSent.NoAnswer
        }
    }
