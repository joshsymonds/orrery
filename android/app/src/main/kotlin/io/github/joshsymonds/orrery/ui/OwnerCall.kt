package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.StepCallResult
import java.io.IOException

/** What an owner call came to: orreryd landed it, refused it with its reason, or no answer came. */
sealed interface CallOutcome {
    data object Landed : CallOutcome
    data class Refused(val reason: String) : CallOutcome
    data object Unanswered : CallOutcome
}

private const val NO_REASON = "no reason was given"

/**
 * Runs an owner [call] on the caller's thread and classifies its answer. A refusal is an error answer with its message.
 * UNAVAILABLE, DEADLINE_EXCEEDED and UNKNOWN, and any IOException that is not an answer, mean no answer came.
 */
fun outcomeOf(call: () -> StepCallResult): CallOutcome =
    try {
        when (val result = call()) {
            StepCallResult.Landed -> CallOutcome.Landed
            is StepCallResult.Refused -> CallOutcome.Refused(result.message.ifEmpty { NO_REASON })
        }
    } catch (error: OrreryCallException) {
        when (error.code) {
            Code.UNAVAILABLE, Code.DEADLINE_EXCEEDED, Code.UNKNOWN -> CallOutcome.Unanswered
            else -> CallOutcome.Refused(error.message.orEmpty().ifEmpty { NO_REASON })
        }
    } catch (error: IOException) {
        CallOutcome.Unanswered
    }

enum class CallPhase { Rest, Asking, Sending, Reading }

/**
 * The call state a group of irreversible owner controls shares: rest, asking which control, sending, and reading the
 * epic again until a load at or past the call's generation lands. A call is sent once per confirm and never retried.
 */
class OwnerCall {
    var phase by mutableStateOf(CallPhase.Rest)
        private set
    var asking by mutableStateOf("")
        private set
    var line by mutableStateOf("")
        private set
    var refused by mutableStateOf(false)
        private set

    val disabled: Boolean get() = phase == CallPhase.Sending || phase == CallPhase.Reading

    private var restLine = ""
    private var readingGeneration = 0

    fun ask(which: String) {
        if (phase != CallPhase.Rest) return
        asking = which
        phase = CallPhase.Asking
    }

    fun keep() {
        if (phase != CallPhase.Asking) return
        asking = ""
        phase = CallPhase.Rest
    }

    /** True once per confirm, when the call goes out. Any other phase sends nothing and answers false. */
    fun send(): Boolean {
        if (phase != CallPhase.Asking) return false
        asking = ""
        refused = false
        line = "Sending…"
        phase = CallPhase.Sending
        return true
    }

    /**
     * Records the answer of the call sent by [send]. A refusal is rest at once with its line and no read. Otherwise
     * the epic is read again: [reread] starts the read and returns its generation.
     */
    fun answered(outcome: CallOutcome, reread: () -> Int) {
        if (phase != CallPhase.Sending) return
        when (outcome) {
            is CallOutcome.Refused -> {
                restLine = "Refused: ${outcome.reason}"
                line = restLine
                refused = true
                phase = CallPhase.Rest
            }
            CallOutcome.Landed -> {
                restLine = ""
                readAgain("Sent. Reading the epic again…", reread)
            }
            CallOutcome.Unanswered -> {
                restLine = "No answer came: it may have gone through."
                readAgain("No answer came: it may have gone through. Reading the epic again…", reread)
            }
        }
    }

    /** A load of the epic at [generation] landed. It ends the reading when it is at or past the stored generation. */
    fun loaded(generation: Int) {
        if (phase != CallPhase.Reading || generation < readingGeneration) return
        line = restLine
        phase = CallPhase.Rest
    }

    private fun readAgain(readingLine: String, reread: () -> Int) {
        line = readingLine
        phase = CallPhase.Reading
        readingGeneration = reread()
    }
}
