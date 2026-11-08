package io.github.joshsymonds.orrery.push

import android.app.Notification
import android.app.NotificationManager
import android.app.RemoteInput
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ReplyReceiverTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val manager = context.getSystemService(NotificationManager::class.java)
    private val server = FakeOrreryServer()

    private val pane = "%3"
    private val id = notificationId(pane)
    private val questionHash = "h"
    private val keysPath = "/v1/panes/%253/keys"
    private val textPath = "/v1/panes/%253/text"

    @Before
    fun setUp() {
        ServerSettings(context).saveServerUrl(server.baseUrl)
        shadowOf(RuntimeEnvironment.getApplication()).grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        PushNotifier(context).show(
            PushEvent(
                pane = pane,
                host = "devbox",
                project = "orrery-demo",
                state = "waiting",
                summary = "Build finished",
                label = "build",
                question = "Run tests?",
                questionHash = questionHash,
            ),
        )
    }

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun keyActionPostsOneKeysRequestWithExpectedQuestionAndCancels() {
        server.respond("POST", keysPath, 200)

        deliverKey("2")
        awaitSettled { server.requests.isNotEmpty() && !isShowing() }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(keysPath, request.rawPath)
        val body = JSONObject(request.body)
        assertEquals(listOf("2"), body.getJSONArray("keys").toStringList())
        assertEquals(questionHash, body.getString("expect_question"))
        assertFalse(isShowing())
    }

    @Test
    fun keyActionOnPromptChangedShowsPromptChangedAndKeepsNotification() {
        server.respond("POST", keysPath, 409)

        deliverKey("2")
        awaitSettled { shownText() == "prompt changed" }

        assertEquals(1, server.requests.size)
        assertEquals("prompt changed", shownText())
    }

    @Test
    fun replyPostsTypedTextOnceAndCancels() {
        server.respond("POST", textPath, 200)

        deliverReply("synthetic reply")
        awaitSettled { server.requests.isNotEmpty() && !isShowing() }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(textPath, request.rawPath)
        assertEquals("synthetic reply", JSONObject(request.body).getString("text"))
        assertFalse(isShowing())
    }

    @Test
    fun replyFailureShowsNotSentAndKeepsNotification() {
        server.respond("POST", textPath, 500)

        deliverReply("synthetic reply")
        awaitSettled { shownText() == "not sent" }

        assertEquals(1, server.requests.size)
        assertEquals("not sent", shownText())
    }

    @Test
    fun blankReplySendsNothingAndShowsNotSent() {
        deliverReply("   ")
        awaitSettled { shownText() == "not sent" }

        assertTrue(server.requests.isEmpty())
        assertEquals("not sent", shownText())
    }

    @Test
    fun manifestResolvesBothActionsToNonExportedReplyReceiver() {
        listOf(ACTION_KEY, ACTION_REPLY).forEach { action ->
            val resolved = context.packageManager
                .queryBroadcastReceivers(Intent(action).setPackage(context.packageName), 0)
            assertEquals("action $action", 1, resolved.size)
            assertEquals(ReplyReceiver::class.java.name, resolved.single().activityInfo.name)
            assertFalse("action $action", resolved.single().activityInfo.exported)
        }
    }

    private fun deliverKey(key: String) {
        val intent = Intent(ACTION_KEY)
            .setPackage(context.packageName)
            .putExtra(EXTRA_PANE, pane)
            .putExtra(EXTRA_KEY, key)
            .putExtra(EXTRA_QUESTION_HASH, questionHash)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
        context.sendBroadcast(intent)
    }

    private fun deliverReply(text: String) {
        val intent = Intent(ACTION_REPLY)
            .setPackage(context.packageName)
            .putExtra(EXTRA_PANE, pane)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
        val results = Bundle().apply { putCharSequence(REMOTE_INPUT_KEY, text) }
        RemoteInput.addResultsToIntent(arrayOf(RemoteInput.Builder(REMOTE_INPUT_KEY).build()), intent, results)
        context.sendBroadcast(intent)
    }

    private fun isShowing(): Boolean = manager.activeNotifications.any { it.id == id }

    private fun shownText(): String? =
        manager.activeNotifications
            .firstOrNull { it.id == id }
            ?.notification
            ?.extras
            ?.getCharSequence(Notification.EXTRA_TEXT)
            ?.toString()

    private fun awaitSettled(settled: () -> Boolean) {
        val deadline = System.currentTimeMillis() + TIMEOUT_MS
        while (!settled() && System.currentTimeMillis() < deadline) {
            shadowOf(Looper.getMainLooper()).idle()
            Thread.sleep(POLL_MS)
        }
    }

    private fun JSONArray.toStringList(): List<String> = List(length()) { getString(it) }

    private companion object {
        const val TIMEOUT_MS = 5_000L
        const val POLL_MS = 10L
    }
}
