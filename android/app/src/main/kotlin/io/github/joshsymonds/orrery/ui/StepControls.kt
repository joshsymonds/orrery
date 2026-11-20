package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.mutableStateOf
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import java.io.IOException

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
