package io.github.joshsymonds.orrery.push

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.CHANNEL_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
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
class PushNotifierTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val manager = context.getSystemService(NotificationManager::class.java)
    private val notifier = PushNotifier(context)

    private val pane = "%7"
    private val idleEvent = PushEvent(
        pane = pane,
        host = "devbox",
        project = "orrery-demo",
        state = "waiting",
        summary = "Build finished",
        label = "build",
        question = "",
        questionHash = "",
    )
    private val questionEvent = idleEvent.copy(question = "Run tests?", questionHash = "qh-synthetic-1")

    @Before
    fun grantPermission() {
        shadowOf(RuntimeEnvironment.getApplication()).grantPermissions(Manifest.permission.POST_NOTIFICATIONS)
    }

    private fun posted(id: Int): Notification {
        val notification = shadowOf(manager).getNotification(null, id)
        assertNotNull(notification)
        return notification
    }

    private fun intentOf(pending: PendingIntent): Intent {
        val intent = shadowOf(pending).savedIntent
        assertNotNull(intent)
        return intent
    }

    @Test
    fun postsOneNotificationUnderPaneIdWithTitleAndText() {
        notifier.show(idleEvent)

        val notification = posted(notificationId(pane))
        assertEquals("orrery-demo · waiting", notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals("Build finished", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(1, shadowOf(manager).allNotifications.size)
        assertNotNull(manager.getNotificationChannel(CHANNEL_ID))
    }

    @Test
    fun textFallsBackToLabelWhenSummaryIsEmpty() {
        notifier.show(idleEvent.copy(summary = ""))

        val notification = posted(notificationId(pane))
        assertEquals("build", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
    }

    @Test
    fun contentIntentOpensMainActivityWithPane() {
        notifier.show(idleEvent)

        val pending = posted(notificationId(pane)).contentIntent
        val intent = intentOf(pending)
        assertEquals(MainActivity::class.java.name, intent.component?.className)
        assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
        assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
    }

    @Test
    fun questionOffersKeyActionsOneTwoThreeThenReply() {
        notifier.show(questionEvent)

        val actions = posted(notificationId(pane)).actions.orEmpty()
        assertEquals(4, actions.size)
        listOf("1", "2", "3").forEachIndexed { index, key ->
            val pending = actions[index].actionIntent
            val intent = intentOf(pending)
            assertEquals(ACTION_KEY, intent.action)
            assertEquals(context.packageName, intent.`package`)
            assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
            assertEquals(key, intent.getStringExtra(EXTRA_KEY))
            assertEquals(questionEvent.questionHash, intent.getStringExtra(EXTRA_QUESTION_HASH))
            assertEquals(notificationId(pane), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
            assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
        }
        assertEquals(1, actions[3].remoteInputs.size)
        assertEquals(REMOTE_INPUT_KEY, actions[3].remoteInputs[0].resultKey)
    }

    @Test
    fun withoutQuestionOnlyReplyIsOffered() {
        notifier.show(idleEvent)

        val actions = posted(notificationId(pane)).actions.orEmpty()
        assertEquals(1, actions.size)
        assertEquals(REMOTE_INPUT_KEY, actions[0].remoteInputs[0].resultKey)
        val pending = actions[0].actionIntent
        val intent = intentOf(pending)
        assertEquals(ACTION_REPLY, intent.action)
        assertEquals(context.packageName, intent.`package`)
        assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
        assertEquals(notificationId(pane), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
        assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_MUTABLE != 0)
    }

    @Test
    fun requestCodesAreDistinctPerPaneAndKey() {
        notifier.show(questionEvent)
        notifier.show(questionEvent.copy(pane = "%8"))

        val codes = listOf(pane, "%8").flatMap { p ->
            val notification = posted(notificationId(p))
            listOf(notification.contentIntent) + notification.actions.orEmpty().map { it.actionIntent }
        }.map { shadowOf(it).requestCode }

        assertEquals(codes.size, codes.toSet().size)
    }

    @Test
    fun laterPushForSamePaneReplacesNotificationAndCarriesNewestHash() {
        notifier.show(questionEvent)
        notifier.show(questionEvent.copy(questionHash = "qh-synthetic-2"))

        assertEquals(1, shadowOf(manager).allNotifications.size)
        val firstKey = posted(notificationId(pane)).actions.orEmpty()[0].actionIntent
        assertEquals("qh-synthetic-2", intentOf(firstKey).getStringExtra(EXTRA_QUESTION_HASH))
    }

    @Test
    fun showResultReplacesTextAndDropsActions() {
        notifier.show(questionEvent)

        notifier.showResult(notificationId(pane), "Answered: 2")

        val notification = posted(notificationId(pane))
        assertEquals("Answered: 2", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals("orrery-demo · waiting", notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals(MainActivity::class.java.name, intentOf(notification.contentIntent).component?.className)
        assertTrue(notification.actions.isNullOrEmpty())
        assertEquals(1, shadowOf(manager).allNotifications.size)
    }

    @Test
    fun deniedPermissionPostsNothing() {
        shadowOf(RuntimeEnvironment.getApplication()).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)

        notifier.show(questionEvent)
        notifier.showResult(notificationId(pane), "Answered: 2")

        assertEquals(0, shadowOf(manager).allNotifications.size)
        assertNull(shadowOf(manager).getNotification(null, notificationId(pane)))
    }
}
