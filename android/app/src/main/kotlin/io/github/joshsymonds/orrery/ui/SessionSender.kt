package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.OrreryClient
import java.io.IOException
import java.util.UUID

/**
 * Sends composed text to one headless session. A retry of the text whose send failed reuses that
 * send's prompt id, so the session records the prompt once; any other send takes a fresh id.
 * A failed send is rethrown unchanged, so the composer keeps its draft.
 */
class SessionSender(
    private val client: OrreryClient,
    private val sessionId: String,
    private val newPromptId: () -> String = { UUID.randomUUID().toString() },
) {
    private var failed: FailedSend? = null

    fun send(text: String) {
        val pending = failed
        val promptId = if (pending != null && pending.text == text) pending.promptId else newPromptId()
        try {
            client.say(sessionId, text, promptId)
        } catch (error: IOException) {
            failed = FailedSend(text, promptId)
            throw error
        }
        failed = null
    }

    private class FailedSend(val text: String, val promptId: String)
}
