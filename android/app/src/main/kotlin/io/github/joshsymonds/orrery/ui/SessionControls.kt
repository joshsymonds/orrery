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

private val LIVE_STATES = setOf("working", "waiting")

/** How a control call ended. [SENT] is the server taking it; the rest are the failures the screen names. */
private enum class ControlOutcome { SENT, NOT_ANSWERABLE, STOPPED, NOT_SENT }

private fun ControlOutcome.label(): String = when (this) {
    ControlOutcome.SENT -> ""
    ControlOutcome.NOT_ANSWERABLE -> "not answerable"
    ControlOutcome.STOPPED -> "stopped"
    ControlOutcome.NOT_SENT -> "not sent"
}

/** Posts an answer. A refused shape is [ControlOutcome.NOT_ANSWERABLE]; a not_found answer is a failure like any other. */
private fun answerOutcome(client: OrreryClient, sessionId: String, requestId: String, answer: String): ControlOutcome =
    try {
        client.answer(sessionId, requestId, answer)
        ControlOutcome.SENT
    } catch (e: OrreryCallException) {
        if (e.code == Code.INVALID_ARGUMENT) ControlOutcome.NOT_ANSWERABLE else ControlOutcome.NOT_SENT
    } catch (e: IOException) {
        ControlOutcome.NOT_SENT
    }

/** Runs an Interrupt or Stop call. A not_found answer means the session is not live, so it reads as [ControlOutcome.STOPPED]. */
private inline fun liveCallOutcome(post: () -> Unit): ControlOutcome =
    try {
        post()
        ControlOutcome.SENT
    } catch (e: OrreryCallException) {
        if (e.code == Code.NOT_FOUND) ControlOutcome.STOPPED else ControlOutcome.NOT_SENT
    } catch (e: IOException) {
        ControlOutcome.NOT_SENT
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
    val shown = if (options.isEmpty()) ControlOutcome.NOT_ANSWERABLE else outcome
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
                        if (result == ControlOutcome.SENT) onAnswered()
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
 * Interrupt and Stop for a headless session, shown only while it is working or waiting. Stop
 * asks for a second tap on its confirm button before it posts. A not_found answer to either
 * reads as stopped, and [onChanged] runs after an answer that moved the session.
 */
@Composable
fun SessionActions(sessionId: String, state: String, client: OrreryClient, onChanged: () -> Unit) {
    val scope = rememberCoroutineScope()
    var outcome by remember(sessionId) { mutableStateOf<ControlOutcome?>(null) }
    var confirming by remember(sessionId) { mutableStateOf(false) }
    if (state !in LIVE_STATES) return

    fun post(call: (String) -> Unit) {
        scope.launch {
            val result = withContext(Dispatchers.IO) {
                liveCallOutcome { call(sessionId) }
            }
            outcome = result
            if (result == ControlOutcome.SENT || result == ControlOutcome.STOPPED) onChanged()
        }
    }

    Column(modifier = Modifier.fillMaxWidth()) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            ControlButton("Interrupt", "session-interrupt") { post(client::interrupt) }
            ControlButton("Stop", "session-stop") { confirming = true }
            if (confirming) {
                ControlButton("Confirm stop", "session-stop-confirm") {
                    confirming = false
                    post(client::stop)
                }
            }
        }
        outcome?.label()?.takeIf { it.isNotEmpty() }?.let { Text(it, modifier = Modifier.padding(top = 4.dp)) }
    }
}

@Composable
private fun ControlButton(label: String, tag: String, onClick: () -> Unit) {
    Button(
        onClick = onClick,
        modifier = Modifier.testTag(tag),
        shape = RoundedCornerShape(10.dp),
        colors = ButtonDefaults.buttonColors(containerColor = Mocha.Surface0, contentColor = Mocha.Text),
        border = BorderStroke(1.dp, Mocha.Surface1),
        elevation = null,
        contentPadding = PaddingValues(horizontal = 12.dp, vertical = 10.dp),
    ) {
        Text(label)
    }
}
