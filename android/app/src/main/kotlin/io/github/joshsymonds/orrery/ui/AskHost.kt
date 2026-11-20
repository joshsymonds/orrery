package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val ANSWER_REFUSED =
    "Not answered: the question changed before your answer arrived. This is what it asks now."
private const val STEP_NOT_SENT = "Not sent."

/** Where a pane's or session's answer stands: in flight, taken by the server (the stream has not shown it gone), or refused. */
private enum class AnswerPhase { SENDING, TAKEN, REFUSED }

/** The answer the phone made to the question [key] names: the option [n] of [name], to [question]. */
private data class PendingAnswer(
    val key: String,
    val n: Int,
    val name: String,
    val question: String,
    val phase: AnswerPhase,
)

/** The words a refusal shows for the question [key] names. */
private data class AskNote(val key: String, val text: String)

/**
 * The Ask for [model], with the life of its answer: a tap sends its option once, taps send nothing while an answer is in
 * flight, a refusal leaves the Ask open with its words, and the answered line folds in once the model is gone. Not now,
 * Bring it back, Decline and Archive this session make their calls and show a refusal's reason. [onRefresh] runs after
 * each call the server took. An epic's or park's Ask is drawn without a call: the epic screen sends those answers.
 */
@Composable
internal fun AskHost(
    model: AskModel?,
    client: OrreryClient,
    now: Long,
    onRefresh: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val scope = rememberCoroutineScope()
    var answer by remember { mutableStateOf<PendingAnswer?>(null) }
    var note by remember { mutableStateOf<AskNote?>(null) }
    val key = model?.target?.let { keyOf(it) }

    // A question that is no longer shown ends the answer taken for it and any refusal for it. A sending answer stays.
    LaunchedEffect(key, answer, note) {
        val taken = answer
        if (key != null && taken != null && taken.phase != AnswerPhase.SENDING && taken.key != key) {
            answer = null
        }
        if (note?.key != key) {
            note = null
        }
    }

    fun pressAnswer(current: AskModel, n: Int) {
        val target = current.target ?: return
        val questionKey = keyOf(target)
        val held = answer
        val inFlight = held?.phase == AnswerPhase.SENDING
        val alreadyTaken = held != null && held.phase == AnswerPhase.TAKEN && held.key == questionKey
        if (inFlight || alreadyTaken) {
            return
        }
        val pending = PendingAnswer(questionKey, n, current.options[n - 1].name, current.question, AnswerPhase.SENDING)
        answer = pending
        note = null
        scope.launch {
            val taken = try {
                withContext(Dispatchers.IO) { answerCall(client, target, n) }
            } catch (e: IOException) {
                false
            }
            if (taken) {
                answer = pending.copy(phase = AnswerPhase.TAKEN)
                onRefresh()
            } else {
                answer = pending.copy(phase = AnswerPhase.REFUSED)
                note = AskNote(questionKey, ANSWER_REFUSED)
            }
        }
    }

    fun step(questionKey: String, call: () -> StepCallResult) {
        scope.launch {
            val result = try {
                withContext(Dispatchers.IO) { call() }
            } catch (e: IOException) {
                null
            }
            when (result) {
                StepCallResult.Landed -> onRefresh()
                is StepCallResult.Refused -> note = AskNote(questionKey, result.message)
                null -> note = AskNote(questionKey, STEP_NOT_SENT)
            }
        }
    }

    if (model != null) {
        val epic = model.kind == AskKind.EPIC || model.kind == AskKind.PARK
        val target = model.target
        val archive = model.archive
        val ownAnswer = answer?.takeIf { it.key == key }
        val shownNote = note?.takeIf { it.key == key }?.text.orEmpty()
        val onAnswer: ((Int) -> Unit)? = if (epic) null else ({ n: Int -> pressAnswer(model, n) })
        val onNotNow: (() -> Unit)? =
            if (target == null || epic) null else ({ step(keyOf(target)) { notNowCall(client, target) } })
        val onBringBack: (() -> Unit)? =
            if (target == null || epic) null else ({ step(keyOf(target)) { bringBackCall(client, target) } })
        val onDecline: (() -> Unit)? =
            if (target == null || epic) null else ({ step(keyOf(target)) { declineCall(client, target) } })
        val onArchive: (() -> Unit)? = if (target == null || archive == null) {
            null
        } else {
            ({ step(keyOf(target)) { client.archive(archive.first, archive.second) } })
        }
        Ask(
            model = model,
            now = now,
            phase = when (ownAnswer?.phase) {
                null -> AskPhase.OPEN
                AnswerPhase.REFUSED -> AskPhase.ERROR
                AnswerPhase.SENDING, AnswerPhase.TAKEN -> AskPhase.SENDING
            },
            picked = ownAnswer?.n,
            note = shownNote,
            onAnswer = onAnswer,
            onNotNow = onNotNow,
            onBringBack = onBringBack,
            onDecline = onDecline,
            onArchive = onArchive,
            onConfirm = null,
            modifier = modifier,
        )
    } else {
        val taken = answer?.takeIf { it.phase == AnswerPhase.TAKEN }
        if (taken != null) {
            Column(modifier = modifier) {
                AskAnsweredLine(n = taken.n, name = taken.name, question = taken.question)
            }
        }
    }
}

private fun keyOf(target: AskTarget): String = when (target) {
    is PaneTarget -> "pane:${target.id}:${target.questionHash}"
    is HeadlessTarget -> "headless:${target.id}:${target.requestId}"
}

/** Sends the answer: a pane's digit with its question hash, a session's option by its label. True when the server took it. */
private fun answerCall(client: OrreryClient, target: AskTarget, n: Int): Boolean = when (target) {
    is PaneTarget -> client.sendKeys(target.id, listOf("$n"), target.questionHash) == KeysResult.Sent
    is HeadlessTarget -> {
        client.answer(target.id, target.requestId, target.options[n - 1])
        true
    }
}

private fun notNowCall(client: OrreryClient, target: AskTarget): StepCallResult = when (target) {
    is PaneTarget -> client.notNow("pane", target.id, "", target.questionHash, target.changed)
    is HeadlessTarget -> client.notNow("headless", target.id, target.requestId, "", 0L)
}

private fun bringBackCall(client: OrreryClient, target: AskTarget): StepCallResult = when (target) {
    is PaneTarget -> client.bringBack("pane", target.id, "", target.questionHash, target.changed)
    is HeadlessTarget -> client.bringBack("headless", target.id, target.requestId, "", 0L)
}

private fun declineCall(client: OrreryClient, target: AskTarget): StepCallResult = when (target) {
    is PaneTarget -> client.decline("pane", target.id, "", target.questionHash)
    is HeadlessTarget -> client.decline("headless", target.id, target.requestId, "")
}
