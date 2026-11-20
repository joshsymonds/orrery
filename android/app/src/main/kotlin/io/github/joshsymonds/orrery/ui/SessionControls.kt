package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
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
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** How an answer to a headless session's request ended. [Sent] is the server taking it; the rest are the outcomes the card names. */
private sealed interface ControlOutcome {
    data object Sent : ControlOutcome
    data object NotAnswerable : ControlOutcome
    data object NotSent : ControlOutcome
}

private fun ControlOutcome.label(): String = when (this) {
    ControlOutcome.Sent -> ""
    ControlOutcome.NotAnswerable -> "not answerable"
    ControlOutcome.NotSent -> "not sent"
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
    val cardShape = RoundedCornerShape(OrreryTokens.Radius.three)
    val buttonShape = RoundedCornerShape(OrreryTokens.Radius.two)

    Column(
        modifier = Modifier
            .testTag("session-request")
            .fillMaxWidth()
            .clip(cardShape)
            .background(OrreryTokens.Role.attentionFill)
            .border(OrreryTokens.Size.hairline, OrreryTokens.Role.attention, cardShape)
            .padding(OrreryTokens.Space.twelve),
    ) {
        Text("CLAUDE IS ASKING".inLabelCase(), color = OrreryTokens.Role.attention, style = OrreryTokens.Type.label)
        Text(question, style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold), modifier = Modifier.padding(top = OrreryTokens.Space.two))
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
                modifier = Modifier.testTag("session-option:$label").fillMaxWidth().padding(top = OrreryTokens.Space.eight),
                shape = buttonShape,
                colors = ButtonDefaults.buttonColors(containerColor = OrreryTokens.Surface.raised, contentColor = OrreryTokens.Ink.one),
                border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
                elevation = null,
                contentPadding = PaddingValues(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.eight + OrreryTokens.Space.two),
            ) {
                Text(label)
            }
        }
        shown?.label()?.takeIf { it.isNotEmpty() }?.let { Text(it, modifier = Modifier.padding(top = OrreryTokens.Space.four)) }
    }
}
