package io.github.joshsymonds.orrery.push

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_EPIC
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.unifiedpush.android.connector.data.PushMessage
import java.io.File

private const val FIXTURE_PATH = "../../internal/push/testdata/epic_payloads.json"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class OrreryPushServiceEpicTest {
    private val application = ApplicationProvider.getApplicationContext<Context>()
    private val manager = application.getSystemService(NotificationManager::class.java)
    private val service: OrreryPushService =
        Robolectric.buildService(OrreryPushService::class.java).create().get()

    private val fixture = JSONObject(File(FIXTURE_PATH).readText())
    private val notice = fixture.getJSONObject("notice")
    private val clear = fixture.getJSONObject("clear")
    private val host = notice.getString("host")
    private val epic = notice.getString("epic")
    private val olderSeen = "1700000100"
    private val instance = "default"

    @Before
    fun setUp() {
        shadowOf(RuntimeEnvironment.getApplication()).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
    }

    private fun push(json: JSONObject) {
        service.onMessage(PushMessage(json.toString().toByteArray(Charsets.UTF_8), decrypted = true), instance)
    }

    private fun postedForEpic(): Notification? = shadowOf(manager).getNotification(null, notificationId(host, epic))

    private fun posted(): Notification {
        val notification = postedForEpic()
        assertNotNull("expected a notification for epic $epic", notification)
        return notification!!
    }

    @Test
    fun epicNoticePostsOneNotificationTaggedByEpic() {
        push(notice)

        assertEquals(1, shadowOf(manager).allNotifications.size)
        assertNotNull(postedForEpic())
    }

    @Test
    fun epicNoticeTextShowsSummary() {
        push(notice)

        assertEquals(
            notice.getString("summary"),
            posted().extras.getCharSequence(Notification.EXTRA_TEXT).toString(),
        )
    }

    @Test
    fun epicNoticeAddsNoAction() {
        push(notice)

        assertEquals(0, posted().actions.orEmpty().size)
    }

    @Test
    fun epicNoticeWithQuestionAddsNoKeyAction() {
        push(JSONObject(notice.toString()).put("question", "Run synthetic tests?"))

        assertEquals(0, posted().actions.orEmpty().size)
    }

    @Test
    fun epicNoticeTapIntentCarriesEpic() {
        push(notice)

        assertEquals(epic, shadowOf(posted().contentIntent).savedIntent.getStringExtra(EXTRA_EPIC))
    }

    @Test
    fun epicClearRemovesEpicNotice() {
        push(notice)
        assertNotNull(postedForEpic())

        push(clear)

        assertNull(postedForEpic())
    }

    @Test
    fun epicClearWithOlderSeenLeavesEpicNotice() {
        push(notice)
        assertNotNull(postedForEpic())

        push(JSONObject(clear.toString()).put("seen", olderSeen))

        assertNotNull(postedForEpic())
    }
}
