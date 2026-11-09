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
        val event = parse(message.content) ?: return
        PushNotifier(this).show(event)
    }

    override fun onRegistrationFailed(reason: FailedReason, instance: String) {
        Log.w(TAG, "registration failed: $reason")
    }

    override fun onUnregistered(instance: String) {
        Log.w(TAG, "unregistered by distributor")
    }

    private fun parse(content: ByteArray): PushEvent? {
        val event = try {
            val json = JSONObject(String(content, Charsets.UTF_8))
            PushEvent(
                pane = json.getString("pane"),
                host = json.getString("host"),
                project = json.getString("project"),
                state = json.getString("state"),
                summary = json.getString("summary"),
                label = json.getString("label"),
                question = json.getString("question"),
                questionHash = json.getString("question_hash"),
            )
        } catch (e: JSONException) {
            Log.w(TAG, "dropped malformed push", e)
            return null
        }
        if (event.pane.isEmpty()) {
            Log.w(TAG, "dropped push with an empty pane")
            return null
        }
        return event
    }
}
