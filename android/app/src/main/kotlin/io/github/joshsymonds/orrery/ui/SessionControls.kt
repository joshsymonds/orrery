package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.Mocha
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal val LIVE_STATES = setOf("working", "waiting")

/** The one state a Resume is offered in. */
private const val RESUMABLE_STATE = "interrupted"

/** The control call a tap made. */
enum class LiveCall { INTERRUPT, STOP, RESUME }

/**
 * How a control call ended. [Sent] is the server taking it; the rest are the outcomes the screen names.
 * [Refused] is a failed_precondition answer, shown as the server's [reason].
 */
private sealed interface ControlOutcome {
    data object Sent : ControlOutcome
    data object NotAnswerable : ControlOutcome
    data object Stopped : ControlOutcome
    data object NotSent : ControlOutcome
    data class Refused(val reason: String) : ControlOutcome
}

private fun ControlOutcome.label(): String = when (this) {
    ControlOutcome.Sent -> ""
    ControlOutcome.NotAnswerable -> "not answerable"
    ControlOutcome.Stopped -> "stopped"
    ControlOutcome.NotSent -> "not sent"
    is ControlOutcome.Refused -> reason
}

/** Posts an answer. A refused shape is [ControlOutcome.NotAnswerable]; a not_found answer is a failure like any other. */
private fun answerOutcome(client: OrreryClient, sessionId: String, requestId: String, answer: String): ControlOutcome =
    try {
        client.answer(sessionId, requestId, answer)
        ControlOutcome.Sent
    } catch (e: OrreryCallException) {
        if (e.code == Code.INVALID_ARGUMENT) ControlOutcome.NotAnswerable else ControlOutcome.NotSent
    } catch (e: IOException) {
        ControlOutcome.NotSent
    }

/** Runs an Interrupt or Stop call. A not_found answer means the session is not live, so it reads as [ControlOutcome.Stopped]. */
private inline fun liveCallOutcome(post: () -> Unit): ControlOutcome =
    try {
        post()
        ControlOutcome.Sent
    } catch (e: OrreryCallException) {
        if (e.code == Code.NOT_FOUND) ControlOutcome.Stopped else ControlOutcome.NotSent
    } catch (e: IOException) {
        ControlOutcome.NotSent
    }

/** Posts a Resume. A failed_precondition answer is [ControlOutcome.Refused] with the server's reason; any other failure is [ControlOutcome.NotSent]. */
private fun resumeOutcome(client: OrreryClient, sessionId: String): ControlOutcome =
    try {
        client.resume(sessionId)
        ControlOutcome.Sent
    } catch (e: OrreryCallException) {
        if (e.code == Code.FAILED_PRECONDITION) ControlOutcome.Refused(e.message.orEmpty()) else ControlOutcome.NotSent
    } catch (e: IOException) {
        ControlOutcome.NotSent
    }

/**
 * A headless session's open request: its question, with one button per option. A tap posts that
 * option as the answer to [requestId]. With no options the request is not answerable from the
 * phone, and the card says so without posting anything.
 */
@Composable
fun SessionRequestCard(
    sessionId: String,
    requestId: String,
    question: String,
    options: List<String>,
    client: OrreryClient,
    onAnswered: () -> Unit,
) {
    val scope = rememberCoroutineScope()
    var outcome by remember(requestId) { mutableStateOf<ControlOutcome?>(null) }
    val shown = if (options.isEmpty()) ControlOutcome.NotAnswerable else outcome
    val cardShape = RoundedCornerShape(14.dp)
    val buttonShape = RoundedCornerShape(10.dp)

    Column(
        modifier = Modifier
            .testTag("session-request")
            .fillMaxWidth()
            .clip(cardShape)
            .background(Mocha.Peach.copy(alpha = 0.06f))
            .border(1.dp, Mocha.Peach, cardShape)
            .padding(12.dp),
    ) {
        Text("CLAUDE IS ASKING", color = Mocha.Peach, fontWeight = FontWeight.Bold, fontSize = 12.sp)
        Text(question, fontWeight = FontWeight.Bold, modifier = Modifier.padding(top = 2.dp))
        options.forEach { label ->
            Button(
                onClick = {
                    scope.launch {
                        val result = withContext(Dispatchers.IO) {
                            answerOutcome(client, sessionId, requestId, label)
                        }
                        outcome = result
                        if (result == ControlOutcome.Sent) onAnswered()
                    }
                },
                modifier = Modifier.testTag("session-option:$label").fillMaxWidth().padding(top = 8.dp),
                shape = buttonShape,
                colors = ButtonDefaults.buttonColors(containerColor = Mocha.Surface0, contentColor = Mocha.Text),
                border = BorderStroke(1.dp, Mocha.Surface1),
                elevation = null,
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 10.dp),
            ) {
                Text(label)
            }
        }
        shown?.label()?.takeIf { it.isNotEmpty() }?.let { Text(it, modifier = Modifier.padding(top = 4.dp)) }
    }
}

/**
 * Interrupt and Stop for a headless session, shown only while it is working or waiting, and Resume
 * only while it is interrupted. Stop asks for a second tap on its confirm button before it posts.
 * A not_found answer to Interrupt or Stop reads as stopped, and [onChanged] runs after an answer
 * that moved the session. A refused Resume shows the server's reason; any other failed Resume reads
 * as not sent. [onTaken] runs with the call a tap made once the server takes it. A tap on any button
 * gives its feedback at once; the confirm button stays on screen until its flash ends.
 */
@Composable
fun SessionActions(
    sessionId: String,
    state: String,
    client: OrreryClient,
    onChanged: () -> Unit,
    onTaken: (LiveCall) -> Unit = {},
) {
    val scope = rememberCoroutineScope()
    var outcome by remember(sessionId) { mutableStateOf<ControlOutcome?>(null) }
    var confirming by remember(sessionId) { mutableStateOf(false) }
    val interruptFeedback = rememberTapFeedback()
    val stopFeedback = rememberTapFeedback()
    val confirmFeedback = rememberTapFeedback()
    val resumeFeedback = rememberTapFeedback()
    if (state !in LIVE_STATES && state != RESUMABLE_STATE) return

    fun post(kind: LiveCall, outcomeOf: () -> ControlOutcome) {
        scope.launch {
            val result = withContext(Dispatchers.IO) { outcomeOf() }
            outcome = result
            if (result == ControlOutcome.Sent) onTaken(kind)
            if (result == ControlOutcome.Sent || result == ControlOutcome.Stopped) onChanged()
        }
    }

    Column(modifier = Modifier.fillMaxWidth()) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (state == RESUMABLE_STATE) {
                ControlButton("Resume", "session-resume", resumeFeedback) {
                    post(LiveCall.RESUME) { resumeOutcome(client, sessionId) }
                }
            } else {
                ControlButton("Interrupt", "session-interrupt", interruptFeedback) {
                    post(LiveCall.INTERRUPT) { liveCallOutcome { client.interrupt(sessionId) } }
                }
                ControlButton("Stop", "session-stop", stopFeedback) { confirming = true }
                if (confirming || confirmFeedback.flashing) {
                    ControlButton("Confirm stop", "session-stop-confirm", confirmFeedback) {
                        if (confirming) {
                            confirming = false
                            post(LiveCall.STOP) { liveCallOutcome { client.stop(sessionId) } }
                        }
                    }
                }
            }
        }
        outcome?.label()?.takeIf { it.isNotEmpty() }?.let { Text(it, modifier = Modifier.padding(top = 4.dp)) }
    }
}

@Composable
private fun ControlButton(label: String, tag: String, feedback: TapFeedback, onClick: () -> Unit) {
    Button(
        onClick = {
            feedback.tap()
            onClick()
        },
        modifier = Modifier.testTag(tag).tapFeedback(feedback),
        shape = RoundedCornerShape(10.dp),
        colors = if (feedback.flashing) {
            tapFeedbackColors(feedback)
        } else {
            ButtonDefaults.buttonColors(containerColor = Mocha.Surface0, contentColor = Mocha.Text)
        },
        border = BorderStroke(1.dp, Mocha.Surface1),
        elevation = null,
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 10.dp),
    ) {
        Text(label)
    }
}
