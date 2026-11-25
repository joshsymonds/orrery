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
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_ANSWER
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_DIR
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_EPIC
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_REQUEST
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
    private val donePane = "%8"
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
    private val askStored = """{"questions":[{"question":"Run tests?"}]}"""
    private val permissionStored = """{"tool":"Bash","input":{"command":"ls"}}"""

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

    private fun titlesOf(actions: Array<out Notification.Action>): List<String> = actions.map { it.title.toString() }

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
    fun sessionQuestionWithoutOptionsOffersOpenThenReply() {
        notifier.show(questionEvent.copy(pane = "", session = "sess-1"))

        val actions = posted(notificationId(host, "sess-1")).actions.orEmpty()
        assertEquals(listOf("Open", "Reply"), titlesOf(actions))
        assertEquals("sess-1", intentOf(actions[0].actionIntent).getStringExtra(EXTRA_SESSION))
        assertEquals(REMOTE_INPUT_KEY, actions[1].remoteInputs[0].resultKey)
    }

    @Test
    fun paneOptionsAreTitledByLabelWithKeyNAndThenReply() {
        notifier.show(questionEvent.copy(question = askStored, options = listOf("Yes, once", "Always")))

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo asks", titleOf(notification))
        val actions = notification.actions.orEmpty()
        assertEquals(listOf("Yes, once", "Always", "Reply"), titlesOf(actions))
        listOf("1", "2").forEachIndexed { index, key ->
            val pending = actions[index].actionIntent
            val intent = intentOf(pending)
            assertEquals(ACTION_KEY, intent.action)
            assertEquals(context.packageName, intent.`package`)
            assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
            assertEquals(key, intent.getStringExtra(EXTRA_KEY))
            assertEquals(questionEvent.questionHash, intent.getStringExtra(EXTRA_QUESTION_HASH))
            assertEquals(notificationId(host, pane), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
            assertEquals(questionEvent.project, intent.getStringExtra(EXTRA_DIR))
            assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
        }
        assertEquals(REMOTE_INPUT_KEY, actions[2].remoteInputs[0].resultKey)
    }

    @Test
    fun threeOptionsOfferNoReply() {
        notifier.show(questionEvent.copy(question = askStored, options = listOf("One", "Two", "Three")))

        val actions = posted(notificationId(host, pane)).actions.orEmpty()
        assertEquals(listOf("One", "Two", "Three"), titlesOf(actions))
        assertTrue(actions.all { it.remoteInputs.isNullOrEmpty() })
    }

    @Test
    fun moreThanThreeOptionsOfferOpenThenReply() {
        notifier.show(questionEvent.copy(options = listOf("One", "Two", "Three", "Four")))

        val actions = posted(notificationId(host, pane)).actions.orEmpty()
        assertEquals(listOf("Open", "Reply"), titlesOf(actions))
        val open = intentOf(actions[0].actionIntent)
        assertEquals(MainActivity::class.java.name, open.component?.className)
        assertEquals(pane, open.getStringExtra(EXTRA_PANE))
        assertEquals(notificationId(host, pane), open.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
        assertEquals(questionEvent.project, open.getStringExtra(EXTRA_DIR))
    }

    @Test
    fun sessionOptionsCarryRequestAndTheirLabelAsAnswer() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", options = listOf("Allow", "Deny"), request = "req-synthetic"))

        val actions = posted(notificationId(host, "sess-1")).actions.orEmpty()
        assertEquals(listOf("Allow", "Deny", "Reply"), titlesOf(actions))
        listOf("Allow", "Deny").forEachIndexed { index, label ->
            val pending = actions[index].actionIntent
            val intent = intentOf(pending)
            assertEquals(ACTION_KEY, intent.action)
            assertEquals(context.packageName, intent.`package`)
            assertEquals("sess-1", intent.getStringExtra(EXTRA_SESSION))
            assertEquals("req-synthetic", intent.getStringExtra(EXTRA_REQUEST))
            assertEquals(label, intent.getStringExtra(EXTRA_ANSWER))
            assertEquals(notificationId(host, "sess-1"), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
            assertEquals(idleEvent.project, intent.getStringExtra(EXTRA_DIR))
            assertFalse(intent.hasExtra(EXTRA_PANE))
            assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
        }
        val reply = intentOf(actions[2].actionIntent)
        assertEquals(ACTION_REPLY, reply.action)
    }

    @Test
    fun sessionReplyCarriesTheSessionNotThePane() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", state = "done"))

        val actions = posted(notificationId(host, "sess-1")).actions.orEmpty()
        assertEquals(listOf("Reply"), titlesOf(actions))
        val intent = intentOf(actions[0].actionIntent)
        assertEquals(ACTION_REPLY, intent.action)
        assertEquals("sess-1", intent.getStringExtra(EXTRA_SESSION))
        assertFalse(intent.hasExtra(EXTRA_PANE))
    }

    @Test
    fun donePaneOffersReplyCarryingThePane() {
        notifier.show(idleEvent.copy(state = "done"))

        val actions = posted(notificationId(host, pane)).actions.orEmpty()
        assertEquals(listOf("Reply"), titlesOf(actions))
        val intent = intentOf(actions[0].actionIntent)
        assertEquals(ACTION_REPLY, intent.action)
        assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
        assertEquals(idleEvent.project, intent.getStringExtra(EXTRA_DIR))
        assertFalse(intent.hasExtra(EXTRA_SESSION))
    }

    @Test
    fun donePaneWithOptionsStillOffersOnlyReplyCarryingThePane() {
        notifier.show(idleEvent.copy(state = "done", options = listOf("A")))

        val actions = posted(notificationId(host, pane)).actions.orEmpty()
        assertEquals(listOf("Reply"), titlesOf(actions))
        val intent = intentOf(actions[0].actionIntent)
        assertEquals(ACTION_REPLY, intent.action)
        assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
        assertEquals(idleEvent.project, intent.getStringExtra(EXTRA_DIR))
        assertFalse(intent.hasExtra(EXTRA_SESSION))
    }

    @Test
    fun epicNoticeOffersOnlyOpen() {
        notifier.show(idleEvent.copy(pane = "", project = "Work board", epic = "work/rota-export", block = "question"))

        val actions = posted(notificationId(host, "work/rota-export")).actions.orEmpty()
        assertEquals(listOf("Open"), titlesOf(actions))
        val intent = intentOf(actions[0].actionIntent)
        assertEquals(MainActivity::class.java.name, intent.component?.className)
        assertEquals("work/rota-export", intent.getStringExtra(EXTRA_EPIC))
        assertEquals(notificationId(host, "work/rota-export"), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
        assertEquals("Work board", intent.getStringExtra(EXTRA_DIR))
    }

    @Test
    fun limitAndAlertNoticesOfferNoActions() {
        notifier.show(idleEvent.copy(pane = "", project = "Weekly usage limit reached", limit = "weekly"))
        notifier.show(idleEvent.copy(pane = "", project = "tide-alerts", state = "done", alert = "severe"))

        assertTrue(posted(notificationId(host, "weekly")).actions.isNullOrEmpty())
        assertTrue(posted(notificationId(host, "alert:severe")).actions.isNullOrEmpty())
    }

    @Test
    fun permissionOffersYesDontAskAgainNoAsKeysOneTwoThreeAndNoReply() {
        notifier.show(questionEvent.copy(question = permissionStored))

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo asks to run Bash", titleOf(notification))
        assertEquals("ls", textOf(notification))
        val actions = notification.actions.orEmpty()
        assertEquals(listOf("Yes", "Don't ask again", "No"), titlesOf(actions))
        assertTrue(actions.all { it.remoteInputs.isNullOrEmpty() })
        listOf("1", "2", "3").forEachIndexed { index, key ->
            val pending = actions[index].actionIntent
            val intent = intentOf(pending)
            assertEquals(ACTION_KEY, intent.action)
            assertEquals(context.packageName, intent.`package`)
            assertEquals(pane, intent.getStringExtra(EXTRA_PANE))
            assertEquals(key, intent.getStringExtra(EXTRA_KEY))
            assertEquals(questionEvent.questionHash, intent.getStringExtra(EXTRA_QUESTION_HASH))
            assertEquals(notificationId(host, pane), intent.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
            assertEquals(questionEvent.project, intent.getStringExtra(EXTRA_DIR))
            assertTrue(shadowOf(pending).flags and PendingIntent.FLAG_IMMUTABLE != 0)
        }
    }

    @Test
    fun permissionWithoutDetailTextsItsSummary() {
        notifier.show(questionEvent.copy(question = """{"tool":"WebSearch","input":{}}""", summary = "Searches the web"))

        assertEquals("Searches the web", textOf(posted(notificationId(host, pane))))
    }

    @Test
    fun askWithOptionsExpandsToQuestionThenNumberedOptions() {
        notifier.show(questionEvent.copy(question = askStored, options = listOf("Yes, once", "Always")))

        val notification = posted(notificationId(host, pane))
        assertEquals("Run tests?", textOf(notification))
        assertEquals(
            "Run tests?\n1. Yes, once\n2. Always",
            notification.extras.getCharSequence(Notification.EXTRA_BIG_TEXT).toString(),
        )
    }

    @Test
    fun plainOrUnparseablePromptOffersOpenThenReplyWithNoKeyButtons() {
        listOf("Run tests?", "{not json").forEach { stored ->
            notifier.show(questionEvent.copy(question = stored, options = listOf("One", "Two")))

            val notification = posted(notificationId(host, pane))
            assertEquals("orrery-demo asks you a question", titleOf(notification))
            assertEquals("Build finished", textOf(notification))
            val actions = notification.actions.orEmpty()
            assertEquals(listOf("Open", "Reply"), titlesOf(actions))
            assertEquals(REMOTE_INPUT_KEY, actions[1].remoteInputs[0].resultKey)
        }
    }

    @Test
    fun sessionQuestionWithOptionsIsTitledAndExpandedLikeAPaneAsk() {
        notifier.show(idleEvent.copy(pane = "", session = "sess-1", question = "Which fix?", options = listOf("Allow", "Deny")))

        val notification = posted(notificationId(host, "sess-1"))
        assertEquals("orrery-demo asks", titleOf(notification))
        assertEquals("Which fix?", textOf(notification))
        assertEquals(
            "Which fix?\n1. Allow\n2. Deny",
            notification.extras.getCharSequence(Notification.EXTRA_BIG_TEXT).toString(),
        )
    }

    @Test
    fun epicQuestionTextAndExpandedTextAreItsBody() {
        notifier.show(idleEvent.copy(pane = "", project = "Work board", epic = "work/rota-export", block = "question", summary = "Which rota?"))

        val notification = posted(notificationId(host, "work/rota-export"))
        assertEquals("rota-export asks you a question", titleOf(notification))
        assertEquals("Which rota?", textOf(notification))
        assertEquals("Which rota?", notification.extras.getCharSequence(Notification.EXTRA_BIG_TEXT).toString())
    }

    @Test
    fun noActionIsTitledByABareDigit() {
        val events = listOf(
            questionEvent.copy(question = askStored, options = listOf("Yes, once", "Always")),
            questionEvent.copy(question = permissionStored),
            questionEvent,
            questionEvent.copy(question = askStored, options = listOf("One", "Two", "Three", "Four")),
            idleEvent.copy(pane = "", session = "sess-1", question = "Which fix?", options = listOf("Allow", "Deny")),
            idleEvent.copy(pane = "", session = "sess-2", question = "Which fix?"),
            idleEvent.copy(pane = "", project = "Work board", epic = "work/rota-export", block = "question"),
        )

        val titles = events.flatMap { event ->
            notifier.show(event)
            shadowOf(manager).allNotifications.flatMap { titlesOf(it.actions.orEmpty()) }
        }

        assertTrue(titles.isNotEmpty())
        titles.forEach { assertFalse("action titled \"$it\"", it.matches(Regex("\\d+"))) }
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
        assertEquals(idleEvent.project, intent.getStringExtra(EXTRA_DIR))
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
        notifier.show(questionEvent.copy(question = permissionStored))
        notifier.show(questionEvent.copy(question = permissionStored, questionHash = "qh-synthetic-2"))

        assertEquals(1, shadowOf(manager).allNotifications.size)
        val firstKey = posted(notificationId(host, pane)).actions.orEmpty()[0].actionIntent
        assertEquals("qh-synthetic-2", intentOf(firstKey).getStringExtra(EXTRA_QUESTION_HASH))
    }

    @Test
    fun showResultReplacesTitleAndTextAndKeepsOnlyOpen() {
        notifier.show(questionEvent)

        notifier.showResult(notificationId(host, pane), "orrery-demo: not answered", "The server refused it. Open it to see why.")

        val notification = posted(notificationId(host, pane))
        assertEquals("orrery-demo: not answered", notification.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
        assertEquals("The server refused it. Open it to see why.", notification.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(MainActivity::class.java.name, intentOf(notification.contentIntent).component?.className)
        val actions = notification.actions.orEmpty()
        assertEquals(listOf("Open"), actions.map { it.title.toString() })
        assertEquals(MainActivity::class.java.name, intentOf(actions.single().actionIntent).component?.className)
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
        notifier.showResult(notificationId(host, pane), "orrery-demo: not sent", "Your reply did not reach the server.")

        notifier.clear(host, pane, "", 99L)

        posted(notificationId(host, pane))
    }

    @Test
    fun deniedPermissionPostsNothing() {
        shadowOf(RuntimeEnvironment.getApplication()).denyPermissions(Manifest.permission.POST_NOTIFICATIONS)

        notifier.show(questionEvent)
        notifier.showResult(notificationId(host, pane), "orrery-demo: not sent", "Your reply did not reach the server.")

        assertEquals(0, shadowOf(manager).allNotifications.size)
        assertNull(shadowOf(manager).getNotification(null, notificationId(host, pane)))
    }

    @Test
    fun panesSwitchOffPostsNothingOfWaitingPaneButStillPostsDonePane() {
        PushPreferences(context).setPosts(CHANNEL_ID, false)

        notifier.show(idleEvent)
        notifier.show(idleEvent.copy(pane = donePane, state = "done"))

        assertNull(shadowOf(manager).getNotification(null, notificationId(host, pane)))
        posted(notificationId(host, donePane))
    }

    @Test
    fun panesSilentSwitchOffPostsNothingOfDonePaneButStillPostsWaitingPane() {
        PushPreferences(context).setPosts(SILENT_CHANNEL_ID, false)

        notifier.show(idleEvent.copy(pane = donePane, state = "done"))
        notifier.show(idleEvent)

        assertNull(shadowOf(manager).getNotification(null, notificationId(host, donePane)))
        posted(notificationId(host, pane))
    }

    @Test
    fun bothSwitchesOnPostWaitingAndDonePanes() {
        PushPreferences(context).setPosts(CHANNEL_ID, true)
        PushPreferences(context).setPosts(SILENT_CHANNEL_ID, true)

        notifier.show(idleEvent)
        notifier.show(idleEvent.copy(pane = donePane, state = "done"))

        posted(notificationId(host, pane))
        posted(notificationId(host, donePane))
    }
}

private data class EpicCase(val slug: String, val block: String, val title: String, val channelId: String)
