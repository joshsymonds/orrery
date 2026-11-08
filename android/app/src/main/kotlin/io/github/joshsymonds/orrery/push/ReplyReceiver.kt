package io.github.joshsymonds.orrery.push

import android.app.NotificationManager
import android.app.RemoteInput
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.KeysResult
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import java.io.IOException

private const val PROMPT_CHANGED = "prompt changed"
private const val NOT_SENT = "not sent"

/**
 * Handles one notification action per broadcast. The work runs on a plain
 * thread under goAsync, so nothing stays connected once the event is answered.
 */
class ReplyReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (!intent.hasExtra(EXTRA_NOTIFICATION_ID)) return
        val pending = goAsync()
        Thread {
            try {
                deliver(context, intent)
            } finally {
                pending.finish()
            }
        }.start()
    }

    private fun deliver(context: Context, intent: Intent) {
        val id = intent.getIntExtra(EXTRA_NOTIFICATION_ID, 0)
        val pane = intent.getStringExtra(EXTRA_PANE)
        val notifier = PushNotifier(context)
        val manager = context.getSystemService(NotificationManager::class.java)
        val client = OrreryClient(ServerSettings(context).serverUrl)
        try {
            when (intent.action) {
                ACTION_KEY -> {
                    val key = intent.getStringExtra(EXTRA_KEY)
                    val questionHash = intent.getStringExtra(EXTRA_QUESTION_HASH)
                    if (pane.isNullOrEmpty() || key.isNullOrEmpty() || questionHash.isNullOrEmpty()) {
                        notifier.showResult(id, NOT_SENT)
                        return
                    }
                    when (client.sendKeys(pane, listOf(key), questionHash)) {
                        KeysResult.Sent -> manager.cancel(id)
                        KeysResult.PromptChanged -> notifier.showResult(id, PROMPT_CHANGED)
                    }
                }
                ACTION_REPLY -> {
                    val text = RemoteInput.getResultsFromIntent(intent)?.getCharSequence(REMOTE_INPUT_KEY)?.toString()
                    if (pane.isNullOrEmpty() || text.isNullOrBlank()) {
                        notifier.showResult(id, NOT_SENT)
                        return
                    }
                    client.sendText(pane, text)
                    manager.cancel(id)
                }
            }
        } catch (error: IOException) {
            notifier.showResult(id, NOT_SENT)
        }
    }
}
