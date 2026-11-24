package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.mutableStateMapOf
import io.github.joshsymonds.orrery.api.DesignAnswerResult
import io.github.joshsymonds.orrery.api.DesignQuestionRow
import io.github.joshsymonds.orrery.api.DesignRecordRow
import io.github.joshsymonds.orrery.api.OrreryClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

/** The words a question's send shows when the server gave no reason it did not land. */
private const val ANSWER_NOT_LANDED = "the answer did not land"

/** What a design's page has read: nothing yet, the record, no design of the id, or a read that failed. */
internal sealed interface DesignLoad {
    /** The first read has not answered. */
    data object Loading : DesignLoad

    /** The design as Designs.Get serves it. */
    data class Loaded(val record: DesignRecordRow) : DesignLoad

    /** Designs.Get has no design of the id. */
    data object Missing : DesignLoad

    /** The read did not answer. */
    data object Failed : DesignLoad
}

/**
 * The design reads and question answers of one conversation's phone sheet, held per conversation screen with the [client]
 * that serves them and the [scope] their calls run in. [start] reads a design off the main thread and [loadOf] is what that
 * read has found; [answer] sends one owner's answer to a question of a design, and [sendOf] is what that send is doing.
 * Both are Compose-observable, so a page drawn from them follows each read and send as it lands.
 */
internal class DesignStages(private val client: OrreryClient, private val scope: CoroutineScope) {
    private val loads = mutableStateMapOf<String, DesignLoad>()
    private val sends = mutableStateMapOf<String, ThreadSend>()

    /** The designs whose read is on its way, so a design read again while it is read is not read twice at once. */
    private val reading = HashSet<String>()

    /** What the read of [design] has found: [DesignLoad.Loading] until its first read answers. */
    fun loadOf(design: String): DesignLoad = loads[design] ?: DesignLoad.Loading

    /** What the last send of [question] of [design] is doing: [ThreadSend.Idle] while none has been made. */
    fun sendOf(design: String, question: DesignQuestionRow): ThreadSend = sends[keyOf(design, question)] ?: ThreadSend.Idle

    /** Reads [design] again, off the main thread, unless a read of it is already on its way. */
    fun start(design: String) {
        if (!reading.add(design)) return
        scope.launch {
            try {
                readInto(design)
            } finally {
                reading.remove(design)
            }
        }
    }

    /**
     * Sends the owner's answer [words] to [question] of [design], or the question's recommendation when [accept] is set.
     * Nothing is sent while a send of the question is on its way or the question is settled. A refused answer or one that
     * fails to reach the server is the send's [ThreadSend.Failed] with its reason; a landed one reads the design again and
     * returns the send to [ThreadSend.Idle].
     */
    fun answer(design: String, question: DesignQuestionRow, words: String, accept: Boolean) {
        val key = keyOf(design, question)
        if (sends[key] == ThreadSend.Sending || question.answer.isNotEmpty()) return
        sends[key] = ThreadSend.Sending
        scope.launch {
            val result = try {
                withContext(Dispatchers.IO) { client.answerDesignQuestion(design, question.id, words, accept) }
            } catch (error: IOException) {
                sends[key] = ThreadSend.Failed(reasonOf(error.message))
                return@launch
            }
            when (result) {
                is DesignAnswerResult.Refused -> sends[key] = ThreadSend.Failed(reasonOf(result.reason))
                is DesignAnswerResult.Done -> {
                    readInto(design)
                    sends.remove(key)
                }
            }
        }
    }

    // readInto reads [design] off the main thread and holds what it found, a read that fails holding Failed.
    private suspend fun readInto(design: String) {
        loads[design] = try {
            withContext(Dispatchers.IO) { client.design(design) }?.let { DesignLoad.Loaded(it) } ?: DesignLoad.Missing
        } catch (_: IOException) {
            DesignLoad.Failed
        }
    }
}

// keyOf names a question of a design, the key its send is held under.
private fun keyOf(design: String, question: DesignQuestionRow): String = "$design/${question.id}"

// reasonOf is the words a send shows: the server's reason or the failure's own message, else that it did not land.
private fun reasonOf(reason: String?): String = if (reason.isNullOrEmpty()) ANSWER_NOT_LANDED else reason
