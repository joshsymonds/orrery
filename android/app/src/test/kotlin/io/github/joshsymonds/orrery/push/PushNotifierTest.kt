package io.github.joshsymonds.orrery.push

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.CHANNEL_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_SESSION
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.SILENT_CHANNEL_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
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
    private val host = "devbox"
    private val idleEvent = PushEvent(
        pane = pane,
        host = host,
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

    private fun titleOf(notification: Notification): String =
        notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString()

    private fun textOf(notification: Notification): String =
        notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString()

    @Test
    fun postsOneNotificationUnderPaneIdWithTitleAndText() {
        notifier.show(idleEvent)

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo asks you a question", notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals("Build finished", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(1, shadowOf(manager).allNotifications.size)
        assertNotNull(manager.getNotificationChannel(CHANNEL_ID))
    }

    @Test
    fun textFallsBackToLabelWhenSummaryIsEmpty() {
        notifier.show(idleEvent.copy(summary = ""))

        val notification = posted(notificationId(host, pane))
        assertEquals("build", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
    }

    @Test
    fun contentIntentOpensMainActivityWithPane() {
        notifier.show(idleEvent)

        val pending = posted(notificationId(host, pane)).contentIntent
        val intent = intentOf(pending)
        assertEquals(MainActivity::class.java.name, intent.component?.className)
        assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
        assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
    }

    @Test
    fun sessionContentIntentOpensMainActivityWithSessionNotPane() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1"))

        val intent = intentOf(posted(notificationId(host, "sess-1")).contentIntent)
        assertEquals(MainActivity::class.java.name, intent.component?.className)
        assertEquals("sess-1", intent.getStringExtra(EXTRA_SESSION))
        assertFalse(intent.hasExtra(EXTRA_PANE))
    }

    @Test
    fun sessionQuestionPostsNoActions() {
        notifier.show(questionEvent.copy(pane = "", session = "sess-1"))

        assertTrue(posted(notificationId(host, "sess-1")).actions.isNullOrEmpty())
    }

    @Test
    fun questionOffersKeyActionsOneTwoThreeThenReply() {
        notifier.show(questionEvent)

        val actions = posted(notificationId(host, pane)).actions.orEmpty()
        assertEquals(4, actions.size)
        listOf("1", "2", "3").forEachIndexed { index, key ->
            val pending = actions[index].actionIntent
            val intent = intentOf(pending)
            assertEquals(ACTION_KEY, intent.action)
            assertEquals(context.packageName, intent.`package`)
            assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
            assertEquals(key, intent.getStringExtra(EXTRA_KEY))
            assertEquals(questionEvent.questionHash, intent.getStringExtra(EXTRA_QUESTION_HASH))
            assertEquals(notificationId(host, pane), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
            assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
        }
        assertEquals(1, actions[3].remoteInputs.size)
        assertEquals(REMOTE_INPUT_KEY, actions[3].remoteInputs[0].resultKey)
    }

    @Test
    fun withoutQuestionOnlyReplyIsOffered() {
        notifier.show(idleEvent)

        val actions = posted(notificationId(host, pane)).actions.orEmpty()
        assertEquals(1, actions.size)
        assertEquals(REMOTE_INPUT_KEY, actions[0].remoteInputs[0].resultKey)
        val pending = actions[0].actionIntent
        val intent = intentOf(pending)
        assertEquals(ACTION_REPLY, intent.action)
        assertEquals(context.packageName, intent.`package`)
        assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
        assertEquals(notificationId(host, pane), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
        assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_MUTABLE != 0)
    }

    @Test
    fun requestCodesAreDistinctPerPaneAndKey() {
        notifier.show(questionEvent)
        notifier.show(questionEvent.copy(pane = "%8"))

        val codes = listOf(pane, "%8").flatMap { p ->
            val notification = posted(notificationId(host, p))
            listOf(notification.contentIntent) + notification.actions.orEmpty().map { it.actionIntent }
        }.map { shadowOf(it).requestCode }

        assertEquals(codes.size, codes.toSet().size)
    }

    @Test
    fun laterPushForSamePaneReplacesNotificationAndCarriesNewestHash() {
        notifier.show(questionEvent)
        notifier.show(questionEvent.copy(questionHash = "qh-synthetic-2"))

        assertEquals(1, shadowOf(manager).allNotifications.size)
        val firstKey = posted(notificationId(host, pane)).actions.orEmpty()[0].actionIntent
        assertEquals("qh-synthetic-2", intentOf(firstKey).getStringExtra(EXTRA_QUESTION_HASH))
    }

    @Test
    fun showResultReplacesTextAndDropsActions() {
        notifier.show(questionEvent)

        notifier.showResult(notificationId(host, pane), "Answered: 2")

        val notification = posted(notificationId(host, pane))
        assertEquals("Answered: 2", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals("orrery-demo asks you a question", notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals(MainActivity::class.java.name, intentOf(notification.contentIntent).component?.className)
        assertTrue(notification.actions.isNullOrEmpty())
        assertEquals(1, shadowOf(manager).allNotifications.size)
    }

    @Test
    fun secondEventForSameHostAndPaneLeavesOneNotificationShowingSecond() {
        notifier.show(idleEvent)
        notifier.show(idleEvent.copy(summary = "Second summary"))

        assertEquals(1, shadowOf(manager).allNotifications.size)
        val notification = posted(notificationId(host, pane))
        assertEquals("Second summary", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
    }

    @Test
    fun doneReplacementIsSilent() {
        notifier.show(idleEvent.copy(state = "done"))
        notifier.show(idleEvent.copy(state = "done", summary = "Second"))

        assertTrue(posted(notificationId(host, pane)).flags and Notification.FLAG_ONLY_ALERT_ONCE != 0)
    }

    @Test
    fun firstDoneEventAlerts() {
        notifier.show(idleEvent.copy(state = "done"))

        assertEquals(0, posted(notificationId(host, pane)).flags and Notification.FLAG_ONLY_ALERT_ONCE)
    }

    @Test
    fun silentDoneEventPostsOnSilentChannelWithTitleAndText() {
        notifier.show(idleEvent.copy(state = "done", silent = true))

        assertEquals(1, shadowOf(manager).allNotifications.size)
        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo · done", notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals("Build finished", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(SILENT_CHANNEL_ID, notification.channelId)
    }

    @Test
    fun silentChannelHasLowImportance() {
        notifier.show(idleEvent.copy(state = "done", silent = true))

        val channel = manager.getNotificationChannel(SILENT_CHANNEL_ID)
        assertNotNull(channel)
        assertEquals(NotificationManager.IMPORTANCE_LOW, channel.importance)
    }

    @Test
    fun doneEventWithoutSilentPostsOnSilentChannel() {
        notifier.show(idleEvent.copy(state = "done"))

        assertEquals(SILENT_CHANNEL_ID, posted(notificationId(host, pane)).channelId)
    }

    @Test
    fun pushMarkedSilentPostsOnSilentChannel() {
        notifier.show(idleEvent.copy(silent = true))

        assertEquals(SILENT_CHANNEL_ID, posted(notificationId(host, pane)).channelId)
    }

    @Test
    fun paneQuestionTitleCarriesItsQuestionAndPostsOnAlertingChannel() {
        val stored = """{"questions":[{"question":"Which fix should I make?","options":[{"label":"One query per week"}]}]}"""
        notifier.show(idleEvent.copy(question = stored, questionHash = "qh-synthetic-1"))

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo asks: Which fix should I make?", titleOf(notification))
        assertEquals(CHANNEL_ID, notification.channelId)
    }

    @Test
    fun paneToolPermissionTitleNamesTheTool() {
        val stored = """{"tool":"Bash","input":{"command":"ls"}}"""
        notifier.show(idleEvent.copy(question = stored, questionHash = "qh-synthetic-1"))

        assertEquals("orrery-demo asks to run Bash", titleOf(posted(notificationId(host, pane))))
    }

    @Test
    fun paneUnreadableQuestionTitleSaysItIsAQuestion() {
        notifier.show(questionEvent)

        assertEquals("orrery-demo asks you a question", titleOf(posted(notificationId(host, pane))))
    }

    @Test
    fun sessionQuestionTitleCarriesItsPlainTextQuestion() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", question = "Which fix should I make?"))

        val notification = posted(notificationId(host, "sess-1"))
        assertEquals("orrery-demo asks: Which fix should I make?", titleOf(notification))
        assertEquals(CHANNEL_ID, notification.channelId)
    }

    @Test
    fun sessionWithoutQuestionTitleSaysItIsAQuestion() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1"))

        assertEquals("orrery-demo asks you a question", titleOf(posted(notificationId(host, "sess-1"))))
    }

    @Test
    fun failedTitleSaysFailedAndPostsOnAlertingChannel() {
        notifier.show(idleEvent.copy(state = "failed"))

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo · failed", titleOf(notification))
        assertEquals(CHANNEL_ID, notification.channelId)
    }

    @Test
    fun interruptedTitleSaysCutOffAndPostsOnAlertingChannel() {
        notifier.show(idleEvent.copy(state = "interrupted"))

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo · cut off", titleOf(notification))
        assertEquals(CHANNEL_ID, notification.channelId)
    }

    @Test
    fun otherStateTitleNamesTheStateOnSilentChannel() {
        notifier.show(idleEvent.copy(state = "running"))

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo · running", titleOf(notification))
        assertEquals(SILENT_CHANNEL_ID, notification.channelId)
    }

    @Test
    fun epicNoticesAreWordedBySlugAndBlockOnTheirChannel() {
        val cases = listOf(
            EpicCase("rota-export", "question", "rota-export asks you a question", CHANNEL_ID),
            EpicCase("offline-tables", "release_step", "offline-tables: release step 2 failed", CHANNEL_ID),
            EpicCase("tide-alerts", "", "tide-alerts needs you", CHANNEL_ID),
            EpicCase("lighthouse", "contract", "lighthouse: a contract is ready to read", SILENT_CHANNEL_ID),
            EpicCase("holder-scopes", "report", "holder-scopes: its report is ready", SILENT_CHANNEL_ID),
        )
        cases.forEach { case ->
            notifier.show(
                idleEvent.copy(
                    pane = "",
                    project = "Work board",
                    epic = "work/${case.slug}",
                    block = case.block,
                    blockRef = "2",
                    summary = "Summary for ${case.slug}",
                ),
            )

            val notification = posted(notificationId(host, "work/${case.slug}"))
            assertEquals(case.title, titleOf(notification))
            assertEquals("Summary for ${case.slug}", textOf(notification))
            assertEquals(case.channelId, notification.channelId)
        }
    }

    @Test
    fun limitNoticeIsTitledByProjectOnSilentChannel() {
        notifier.show(idleEvent.copy(pane = "", project = "Weekly usage limit reached", summary = "Resets 15:10.", limit = "weekly"))

        val notification = posted(notificationId(host, "weekly"))
        assertEquals("Weekly usage limit reached", titleOf(notification))
        assertEquals("Resets 15:10.", textOf(notification))
        assertEquals(SILENT_CHANNEL_ID, notification.channelId)
    }

    @Test
    fun alertNoticeIsTitledByProjectOnAlertingChannel() {
        notifier.show(idleEvent.copy(pane = "", project = "tide-alerts", summary = "Severe care", state = "done", alert = "severe"))

        val notification = posted(notificationId(host, "alert:severe"))
        assertEquals("tide-alerts", titleOf(notification))
        assertEquals(CHANNEL_ID, notification.channelId)
    }

    @Test
    fun channelsAreNamedQuestionsAndFailuresAndEverythingElse() {
        notifier.show(idleEvent)
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", state = "done"))

        assertEquals("Questions and failures", manager.getNotificationChannel(CHANNEL_ID).name.toString())
        assertEquals("Everything else", manager.getNotificationChannel(SILENT_CHANNEL_ID).name.toString())
    }

    @Test
    fun smallIconIsTheOrreryMark() {
        notifier.show(idleEvent)

        assertEquals(R.drawable.ic_notification, posted(notificationId(host, pane)).smallIcon.resId)
    }

    @Test
    fun waitingAndFailedReplacementsAlert() {
        notifier.show(idleEvent.copy(state = "done"))

        notifier.show(idleEvent.copy(state = "waiting"))
        assertEquals(0, posted(notificationId(host, pane)).flags and Notification.FLAG_ONLY_ALERT_ONCE)

        notifier.show(idleEvent.copy(state = "failed"))
        assertEquals(0, posted(notificationId(host, pane)).flags and Notification.FLAG_ONLY_ALERT_ONCE)
    }

    @Test
    fun onePaneOnTwoHostsGivesTwoNotifications() {
        notifier.show(idleEvent)
        notifier.show(idleEvent.copy(host = "otherbox"))

        assertEquals(2, shadowOf(manager).allNotifications.size)
        val first = posted(notificationId(host, pane))
        val second = posted(notificationId("otherbox", pane))
        assertNotEquals(shadowOf(first.contentIntent).requestCode, shadowOf(second.contentIntent).requestCode)
    }

    @Test
    fun sessionEventWithEmptyPaneIsKeyedByHostAndSession() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1"))
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", summary = "Second"))

        assertEquals(1, shadowOf(manager).allNotifications.size)
        val notification = posted(notificationId(host, "sess-1"))
        assertEquals("Second", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
    }

    @Test
    fun clearAtNoticeTimeRemovesIt() {
        notifier.show(idleEvent.copy(time = 100L))

        notifier.clear(host, pane, "", 100L)

        assertNull(shadowOf(manager).getNotification(null, notificationId(host, pane)))
    }

    @Test
    fun clearAfterNoticeTimeRemovesIt() {
        notifier.show(idleEvent.copy(time = 100L))

        notifier.clear(host, pane, "", 150L)

        assertEquals(0, shadowOf(manager).allNotifications.size)
    }

    @Test
    fun clearBeforeNoticeTimeLeavesIt() {
        notifier.show(idleEvent.copy(time = 100L))

        notifier.clear(host, pane, "", 99L)

        posted(notificationId(host, pane))
        assertEquals(1, shadowOf(manager).allNotifications.size)
    }

    @Test
    fun clearForAnotherHostOrPaneLeavesNotice() {
        notifier.show(idleEvent.copy(time = 100L))

        notifier.clear("otherbox", pane, "", 200L)
        notifier.clear(host, "%8", "", 200L)

        posted(notificationId(host, pane))
        assertEquals(1, shadowOf(manager).allNotifications.size)
    }

    @Test
    fun sessionClearRemovesSessionNoticeAndLeavesPaneNotice() {
        notifier.show(idleEvent.copy(time = 100L))
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", time = 100L))

        notifier.clear(host, "", "sess-1", 200L)

        posted(notificationId(host, pane))
        assertEquals(1, shadowOf(manager).allNotifications.size)
    }

    @Test
    fun answeredNoticeKeepsItsTimeForClear() {
        notifier.show(questionEvent.copy(time = 100L))
        notifier.showResult(notificationId(host, pane), "Answered: 2")

        notifier.clear(host, pane, "", 99L)

        posted(notificationId(host, pane))
    }

    @Test
    fun deniedPermissionPostsNothing() {
        shadowOf(RuntimeEnvironment.getApplication()).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)

        notifier.show(questionEvent)
        notifier.showResult(notificationId(host, pane), "Answered: 2")

        assertEquals(0, shadowOf(manager).allNotifications.size)
        assertNull(shadowOf(manager).getNotification(null, notificationId(host, pane)))
    }
}

private data class EpicCase(val slug: String, val block: String, val title: String, val channelId: String)
