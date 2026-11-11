package io.github.joshsymonds.orrery.push

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.unifiedpush.android.connector.data.PublicKeySet
import org.unifiedpush.android.connector.data.PushEndpoint
import org.unifiedpush.android.connector.data.PushMessage
import java.util.concurrent.TimeUnit

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class OrreryPushServiceTest {
    private val application = ApplicationProvider.getApplicationContext<Context>()
    private val manager = application.getSystemService(NotificationManager::class.java)
    private val server = FakeOrreryServer()
    private val service: OrreryPushService =
        Robolectric.buildService(OrreryPushService::class.java).create().get()

    private val instance = "default"
    private val endpointUrl = "https://push.example.com/up/synthetic"
    private val publicKey = "synthetic-public-key"
    private val authSecret = "synthetic-auth-secret"
    private val pane = "%7"

    @Before
    fun setUp() {
        ServerSettings(application).saveServerUrl(server.baseUrl)
        server.respond("POST", "/v1/push/register", 204)
        shadowOf(RuntimeEnvironment.getApplication()).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
    }

    @After
    fun tearDown() {
        server.close()
    }

    private fun payload(
        pane: String = this.pane,
        question: String = "Run synthetic tests?",
        questionHash: String = "qh-synthetic-1",
    ): ByteArray = JSONObject()
        .put("pane", pane)
        .put("host", "synthetic-host")
        .put("project", "synthetic-project")
        .put("state", "waiting")
        .put("summary", "Synthetic summary.")
        .put("label", "synthetic-label")
        .put("question", question)
        .put("question_hash", questionHash)
        .toString()
        .toByteArray(Charsets.UTF_8)

    private fun awaitRequests(count: Int): List<RecordedRequest> {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (server.requests.size < count && System.nanoTime() < deadline) {
            Thread.sleep(10)
        }
        return server.requests
    }

    private fun posted(): Notification {
        val notification = shadowOf(manager).getNotification(null, notificationId("synthetic-host", pane))
        assertNotNull("expected a notification for pane $pane", notification)
        return notification
    }

    @Test
    fun newEndpointPostsOneRegisterWithEndpointAndKeys() {
        service.onNewEndpoint(PushEndpoint(endpointUrl, PublicKeySet(publicKey, authSecret)), instance)

        val requests = awaitRequests(1)
        assertEquals(1, requests.size)
        val request = requests.single()
        assertEquals("POST", request.method)
        assertEquals("/v1/push/register", request.rawPath)
        val body = JSONObject(request.body)
        assertEquals(setOf("endpoint", "p256dh", "auth"), body.keys().asSequence().toSet())
        assertEquals(endpointUrl, body.getString("endpoint"))
        assertEquals(publicKey, body.getString("p256dh"))
        assertEquals(authSecret, body.getString("auth"))
    }

    @Test
    fun endpointWithoutKeysPostsNothing() {
        service.onNewEndpoint(PushEndpoint(endpointUrl, null), instance)

        Thread.sleep(500)
        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun decryptedQuestionPostsNotificationWithKeyActionsCarryingQuestionHash() {
        service.onMessage(PushMessage(payload(), decrypted = true), instance)

        val notification = posted()
        assertEquals(
            "synthetic-project · waiting",
            notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString(),
        )
        assertEquals(
            "Synthetic summary.",
            notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString(),
        )
        val actions = notification.actions.orEmpty()
        assertEquals(4, actions.size)
        listOf("1", "2", "3").forEachIndexed { index, key ->
            val intent = shadowOf(actions[index].actionIntent).savedIntent
            assertEquals(key, intent.getStringExtra(EXTRA_KEY))
            assertEquals("qh-synthetic-1", intent.getStringExtra(EXTRA_QUESTION_HASH))
        }
    }

    @Test
    fun undecryptedMessagePostsNoNotification() {
        service.onMessage(PushMessage(payload(), decrypted = false), instance)

        assertTrue(shadowOf(manager).allNotifications.isEmpty())
    }

    @Test
    fun malformedJsonPostsNoNotification() {
        service.onMessage(PushMessage("{not json".toByteArray(Charsets.UTF_8), decrypted = true), instance)

        assertTrue(shadowOf(manager).allNotifications.isEmpty())
    }

    @Test
    fun emptyPanePostsNoNotification() {
        service.onMessage(PushMessage(payload(pane = ""), decrypted = true), instance)

        assertTrue(shadowOf(manager).allNotifications.isEmpty())
    }
}
