package io.github.joshsymonds.orrery.push

import android.app.NotificationManager
import android.app.RemoteInput
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.connectrpc.Code
import io.github.joshsymonds.orrery.BackgroundWork
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_ANSWER
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_DIR
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_HOST
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_REQUEST
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_SESSION
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import java.io.IOException
import java.util.UUID

private const val NOT_ANSWERED = "not answered"
private const val NOT_CONFIRMED = "not confirmed"
private const val NOT_SENT = "not sent"
private const val PROMPT_CHANGED = "The question changed before your answer arrived. Open it to see what it asks now."
private const val REFUSED_ANSWER = "The server refused it. Open it to see why."
private const val REFUSED_REPLY = "The server refused your reply."
private const val NO_ANSWER = "The server did not answer in time; it may have landed. Open it to see."
private const val ANSWER_NOT_REACHED = "Your answer did not reach the server."
private const val REPLY_NOT_REACHED = "Your reply did not reach the server."
private const val KEPT_IN_APP = "It is kept in the app."
private const val PANE_KIND = "pane"
private const val SESSION_KIND = "session"

/** What the shade shows for an action that did not land: its title, after the directory, and its text. */
private class Shown(val title: String, val text: String)

/** A typed reply, with the host and the conversation it names. */
private class TypedReply(val host: String, val kind: String, val conversation: String, val text: String)

/**
 * Handles one notification action per broadcast. The work runs on a plain
 * thread under goAsync, so nothing stays connected once the event is answered.
 */
class ReplyReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (!intent.hasExtra(EXTRA_NOTIFICATION_ID)) return
        val pending = goAsync()
        BackgroundWork.run("notification action ${intent.action}") {
            try {
                deliver(context, intent)
            } finally {
                pending.finish()
            }
        }
    }

    private fun deliver(context: Context, intent: Intent) {
        val id = intent.getIntExtra(EXTRA_NOTIFICATION_ID, 0)
        val dir = intent.getStringExtra(EXTRA_DIR).orEmpty()
        val notifier = PushNotifier(context)
        val manager = context.getSystemService(NotificationManager::class.java)
        val client = OrreryClient(ServerSettings(context).serverUrl)
        val reply = intent.action == ACTION_REPLY
        val shown = try {
            if (reply) sendReply(context, client, intent) else sendAnswer(client, intent)
        } catch (error: NoAnswerException) {
            Shown(NOT_CONFIRMED, NO_ANSWER)
        } catch (error: OrreryCallException) {
            Shown(NOT_ANSWERED, REFUSED_ANSWER)
        } catch (error: IOException) {
            Shown(NOT_SENT, ANSWER_NOT_REACHED)
        }
        if (shown != null) {
            notifier.showResult(id, "$dir: ${shown.title}", shown.text)
            return
        }
        manager.cancel(id)
        markSeen(client, intent)
    }

    /** Sends the answer an option names, and returns null when it landed. */
    private fun sendAnswer(client: OrreryClient, intent: Intent): Shown? {
        val session = intent.getStringExtra(EXTRA_SESSION)
        if (!session.isNullOrEmpty()) {
            val requestId = intent.getStringExtra(EXTRA_REQUEST)
            val answer = intent.getStringExtra(EXTRA_ANSWER)
            if (requestId.isNullOrEmpty() || answer.isNullOrEmpty()) return Shown(NOT_SENT, ANSWER_NOT_REACHED)
            return try {
                client.answer(session, requestId, answer)
                null
            } catch (error: OrreryCallException) {
                if (error.code == Code.NOT_FOUND) Shown(NOT_ANSWERED, PROMPT_CHANGED) else throw error
            }
        }
        val pane = intent.getStringExtra(EXTRA_PANE)
        val key = intent.getStringExtra(EXTRA_KEY)
        val questionHash = intent.getStringExtra(EXTRA_QUESTION_HASH)
        if (pane.isNullOrEmpty() || key.isNullOrEmpty() || questionHash.isNullOrEmpty()) {
            return Shown(NOT_SENT, ANSWER_NOT_REACHED)
        }
        return when (client.sendKeys(pane, listOf(key), questionHash)) {
            KeysResult.Sent -> null
            KeysResult.PromptChanged -> Shown(NOT_ANSWERED, PROMPT_CHANGED)
        }
    }

    /**
     * Sends the typed reply to the session or pane the action names, and returns null when it landed. A reply that did
     * not land is kept on this phone, and its result says so.
     */
    private fun sendReply(context: Context, client: OrreryClient, intent: Intent): Shown? {
        val text = RemoteInput.getResultsFromIntent(intent)?.getCharSequence(REMOTE_INPUT_KEY)?.toString()
        if (text.isNullOrBlank()) return Shown(NOT_SENT, REPLY_NOT_REACHED)
        val session = intent.getStringExtra(EXTRA_SESSION)
        val kind = if (!session.isNullOrEmpty()) SESSION_KIND else PANE_KIND
        val conversation = if (kind == SESSION_KIND) session else intent.getStringExtra(EXTRA_PANE)
        if (conversation.isNullOrEmpty()) return Shown(NOT_SENT, REPLY_NOT_REACHED)
        val reply = TypedReply(intent.getStringExtra(EXTRA_HOST).orEmpty(), kind, conversation, text)
        return try {
            if (kind == SESSION_KIND) {
                client.say(conversation, text, UUID.randomUUID().toString())
            } else {
                client.sendText(conversation, text)
            }
            null
        } catch (error: NoAnswerException) {
            unlanded(context, reply, NOT_CONFIRMED, NO_ANSWER)
        } catch (error: OrreryCallException) {
            unlanded(context, reply, NOT_SENT, REFUSED_REPLY)
        } catch (error: IOException) {
            unlanded(context, reply, NOT_SENT, REPLY_NOT_REACHED)
        }
    }

    /** Keeps a reply the receiver did not see land, and returns the result that says it is kept. */
    private fun unlanded(context: Context, reply: TypedReply, title: String, reason: String): Shown {
        val kept = KeptReplies(context).keep(reply.host, reply.kind, reply.conversation, reply.text)
        return Shown(title, if (kept) "$reason $KEPT_IN_APP" else reason)
    }

    /**
     * Records that the owner opened the notice, once its action landed. A Seen that fails changes nothing shown: the
     * action landed and the notice has already left.
     */
    private fun markSeen(client: OrreryClient, intent: Intent) {
        val session = intent.getStringExtra(EXTRA_SESSION)
        val pane = intent.getStringExtra(EXTRA_PANE)
        try {
            if (!session.isNullOrEmpty()) {
                client.sessionNoticeSeen(session)
            } else if (!pane.isNullOrEmpty()) {
                client.paneNoticeSeen(pane)
            }
        } catch (error: IOException) {
            // The action landed and the notice has left; a Seen that fails shows nothing.
        }
    }
}
