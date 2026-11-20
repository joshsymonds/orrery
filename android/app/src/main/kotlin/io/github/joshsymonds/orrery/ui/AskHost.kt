package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
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
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val ANSWER_REFUSED =
    "Not answered: the question changed before your answer arrived. This is what it asks now."
private const val STEP_NOT_SENT = "Not sent."

/** Where a pane's or session's answer stands: in flight, taken by the server (the stream has not shown it gone), or refused. */
internal enum class AnswerPhase { SENDING, TAKEN, REFUSED }

/** The answer the phone made to the question [key] names: the option [n] of [name], to [question]. */
internal data class PendingAnswer(
    val key: String,
    val n: Int,
    val name: String,
    val question: String,
    val phase: AnswerPhase,
)

/** The words a refusal shows for the question [key] names. */
internal data class AskNote(val key: String, val text: String)

/**
 * The life of a conversation's Ask: the answer in flight or taken, the refusal's words, and the scope its calls run in.
 * A screen holds one per conversation, outside the list item that draws the Ask, so scrolling that item away and back
 * keeps the answer. The calls run in the screen's scope, so leaving the composition does not cancel them.
 */
@Stable
internal class AskLife(private val scope: CoroutineScope) {
    internal var answer by mutableStateOf<PendingAnswer?>(null)
        private set
    internal var note by mutableStateOf<AskNote?>(null)
        private set

    /** Sends option [n] of [current] unless an answer is in flight or already taken for its question. */
    fun press(current: AskModel, client: OrreryClient, n: Int, onRefresh: () -> Unit) {
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

    /** Runs a Not now, Bring it back, Decline or Archive call for the question [questionKey] names. */
    fun step(questionKey: String, onRefresh: () -> Unit, call: () -> StepCallResult) {
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

    /** A question that is no longer shown ends the answer taken for it and any refusal for it. A sending answer stays. */
    fun dropStale(key: String?) {
        val taken = answer
        if (key != null && taken != null && taken.phase != AnswerPhase.SENDING && taken.key != key) {
            answer = null
        }
        if (note?.key != key) {
            note = null
        }
    }
}

/** The [AskLife] for the conversation [key] names, kept while that key holds. */
@Composable
internal fun rememberAskLife(key: Any): AskLife {
    val scope = rememberCoroutineScope()
    return remember(key) { AskLife(scope) }
}

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
    life: AskLife = rememberAskLife(Unit),
) {
    val key = model?.target?.let { keyOf(it) }

    LaunchedEffect(key, life.answer, life.note) { life.dropStale(key) }

    if (model != null) {
        val epic = model.kind == AskKind.EPIC || model.kind == AskKind.PARK
        val target = model.target
        val archive = model.archive
        val ownAnswer = life.answer?.takeIf { it.key == key }
        val shownNote = life.note?.takeIf { it.key == key }?.text.orEmpty()
        val onAnswer: ((Int) -> Unit)? = if (epic) null else ({ n: Int -> life.press(model, client, n, onRefresh) })
        val onNotNow: (() -> Unit)? =
            if (target == null || epic) null else ({ life.step(keyOf(target), onRefresh) { notNowCall(client, target) } })
        val onBringBack: (() -> Unit)? =
            if (target == null || epic) null else ({ life.step(keyOf(target), onRefresh) { bringBackCall(client, target) } })
        val onDecline: (() -> Unit)? =
            if (target == null || epic) null else ({ life.step(keyOf(target), onRefresh) { declineCall(client, target) } })
        val onArchive: (() -> Unit)? = if (target == null || archive == null) {
            null
        } else {
            ({ life.step(keyOf(target), onRefresh) { client.archive(archive.first, archive.second) } })
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
        val taken = life.answer?.takeIf { it.phase == AnswerPhase.TAKEN }
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
