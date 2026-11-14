package io.github.joshsymonds.orrery.push

import android.util.Log
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.OrreryClient
import org.json.JSONException
import org.json.JSONObject
import org.unifiedpush.android.connector.FailedReason
import org.unifiedpush.android.connector.PushService
import org.unifiedpush.android.connector.data.PushEndpoint
import org.unifiedpush.android.connector.data.PushMessage

private const val TAG = "OrreryPushService"
private const val CLEAR_KIND = "clear"
private const val SILENT_TRUE = "true"

class OrreryPushService : PushService() {
    override fun onNewEndpoint(endpoint: PushEndpoint, instance: String) {
        val keys = endpoint.pubKeySet
        if (keys == null) {
            Log.w(TAG, "endpoint without keys; not registering")
            return
        }
        val registration = PushRegistration(OrreryClient(ServerSettings(this).serverUrl))
        Thread {
            registration.register(endpoint.url, keys.pubKey, keys.auth)
        }.start()
    }

    override fun onMessage(message: PushMessage, instance: String) {
        if (!message.decrypted) {
            Log.w(TAG, "dropped push that did not decrypt")
            return
        }
        val json = try {
            JSONObject(String(message.content, Charsets.UTF_8))
        } catch (e: JSONException) {
            Log.w(TAG, "dropped malformed push", e)
            return
        }
        if (json.optString("kind") == CLEAR_KIND) {
            val clear = parseClear(json) ?: return
            PushNotifier(this).clear(clear.host, clear.pane, clear.session, clear.seen, clear.epic)
            return
        }
        val event = parse(json) ?: return
        PushNotifier(this).show(event)
    }

    override fun onRegistrationFailed(reason: FailedReason, instance: String) {
        Log.w(TAG, "registration failed: $reason")
    }

    override fun onUnregistered(instance: String) {
        Log.w(TAG, "unregistered by distributor")
    }

    private fun parse(json: JSONObject): PushEvent? {
        val event = try {
            PushEvent(
                pane = json.getString("pane"),
                host = json.getString("host"),
                project = json.getString("project"),
                state = json.getString("state"),
                summary = json.getString("summary"),
                label = json.getString("label"),
                question = json.getString("question"),
                questionHash = json.getString("question_hash"),
                session = json.optString("session"),
                epic = json.optString("epic"),
                time = optDecimal(json.optString("time")),
                silent = json.opt("silent") == SILENT_TRUE,
            )
        } catch (e: JSONException) {
            Log.w(TAG, "dropped malformed push", e)
            return null
        }
        if (event.pane.isEmpty() && event.session.isEmpty() && event.epic.isEmpty()) {
            Log.w(TAG, "dropped push with an empty pane, session, and epic")
            return null
        }
        return event
    }

    private fun parseClear(json: JSONObject): ClearEvent? {
        val clear = try {
            ClearEvent(
                host = json.getString("host"),
                pane = json.optString("pane"),
                session = json.optString("session"),
                epic = json.optString("epic"),
                seen = decimal(json.getString("seen")),
            )
        } catch (e: JSONException) {
            Log.w(TAG, "dropped malformed clear", e)
            return null
        }
        if (clear.pane.isEmpty() && clear.session.isEmpty() && clear.epic.isEmpty()) {
            Log.w(TAG, "dropped clear with no pane, session, or epic")
            return null
        }
        return clear
    }

    private fun optDecimal(text: String): Long = if (text.isEmpty()) 0L else decimal(text)

    private fun decimal(text: String): Long = text.toLongOrNull() ?: throw JSONException("not decimal: $text")
}

private data class ClearEvent(
    val host: String,
    val pane: String,
    val session: String,
    val epic: String,
    val seen: Long,
)
