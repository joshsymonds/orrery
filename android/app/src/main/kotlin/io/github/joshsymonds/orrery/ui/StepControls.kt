package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.mutableStateOf
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import java.io.IOException

private const val HOLD_LANDED = "hold landed"
private const val NUDGE_LANDED = "nudge landed"
private const val NUDGE_NOT_KNOWN = "nudge not known: it may have landed; nothing was resent"
private const val STOPPED = "stopped"
private const val UNREACHABLE = "could not reach the step"

/** A nudge whose outcome is not known: its [text] is not sent again until it changes, and [error] is its no-answer. */
private class UnknownNudge(val text: String, val error: NoAnswerException)

/** The owner's calls on one labelled step and what the last one came to, as the step screen shows it. */
class StepCalls(private val client: OrreryClient, private val stepId: String) {
    private val shown = mutableStateOf<String?>(null)
    private var unknownNudge: UnknownNudge? = null

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

    /**
     * Sends [text] as a nudge. A refusal shows its reason and throws [NudgeRefusedException]. A nudge that got no answer
     * shows that its outcome is not known and throws its [NoAnswerException]; the same text is not sent again until it
     * changes, so the same exception is thrown for it. Any other failure shows that the step could not be reached and
     * rethrows.
     */
    fun nudge(text: String) {
        unknownNudge?.takeIf { it.text == text }?.let { throw it.error }
        unknownNudge = null
        val result = try {
            client.nudgeStep(stepId, text)
        } catch (e: NoAnswerException) {
            unknownNudge = UnknownNudge(text, e)
            shown.value = NUDGE_NOT_KNOWN
            throw e
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
