package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.ui.theme.Mocha
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val HOLD_LANDED = "hold landed"
private const val NUDGE_LANDED = "nudge landed"
private const val STOPPED = "stopped"
private const val UNREACHABLE = "could not reach the step"

/** The owner's calls on one labelled step and what the last one came to, as the step screen shows it. */
class StepCalls(private val client: OrreryClient, private val stepId: String) {
    private val shown = mutableStateOf<String?>(null)

    val outcome: String? get() = shown.value

    fun hold() {
        shown.value = try {
            client.holdStep(stepId).describe(HOLD_LANDED)
        } catch (e: IOException) {
            UNREACHABLE
        }
    }

    fun stop() {
        shown.value = try {
            client.stopStep(stepId).describe(STOPPED)
        } catch (e: IOException) {
            UNREACHABLE
        }
    }

    /** Sends [text] as a nudge. A refusal shows its reason and throws [NudgeRefusedException]; any other failure rethrows. */
    fun nudge(text: String) {
        val result = try {
            client.nudgeStep(stepId, text)
        } catch (e: IOException) {
            shown.value = UNREACHABLE
            throw e
        }
        when (result) {
            StepCallResult.Landed -> shown.value = NUDGE_LANDED
            is StepCallResult.Refused -> {
                shown.value = "refused: ${result.message}"
                throw NudgeRefusedException(result.message)
            }
        }
    }

    private fun StepCallResult.describe(landed: String): String = when (this) {
        StepCallResult.Landed -> landed
        is StepCallResult.Refused -> "refused: $message"
    }
}

class NudgeRefusedException(message: String) : IOException(message)

/** Hold, and Stop behind a second tap on Confirm stop, for one step; the outcome line below them. */
@Composable
fun StepActions(calls: StepCalls, held: Boolean) {
    val scope = rememberCoroutineScope()
    var confirming by remember(calls) { mutableStateOf(false) }

    Column(modifier = Modifier.fillMaxWidth()) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (!held) {
                StepButton("Hold", "step-hold") {
                    scope.launch { withContext(Dispatchers.IO) { calls.hold() } }
                }
            }
            StepButton("Stop", "step-stop") { confirming = true }
            if (confirming) {
                StepButton("Confirm stop", "step-stop-confirm") {
                    confirming = false
                    scope.launch { withContext(Dispatchers.IO) { calls.stop() } }
                }
            }
        }
        calls.outcome?.let { Text(it, modifier = Modifier.padding(top = 4.dp)) }
    }
}

@Composable
private fun StepButton(label: String, tag: String, onClick: () -> Unit) {
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
