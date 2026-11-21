package io.github.joshsymonds.orrery.push

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.RemoteInput
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.Icon
import android.os.Bundle
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.CHANNEL_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_TIME
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import org.json.JSONException
import org.json.JSONObject

data class PushEvent(
    val pane: String,
    val host: String,
    val project: String,
    val state: String,
    val summary: String,
    val label: String,
    val question: String,
    val questionHash: String,
    val options: List<String> = emptyList(),
    val request: String = "",
    val session: String = "",
    val epic: String = "",
    val limit: String = "",
    val alert: String = "",
    val block: String = "",
    val blockRef: String = "",
    val time: Long = 0L,
    val silent: Boolean = false,
)

private class PushNotice(val title: String, val text: String, val channelId: String)

class PushNotifier(private val context: Context) {
    private val manager = context.getSystemService(NotificationManager::class.java)
    private val preferences = PushPreferences(context)

    fun show(event: PushEvent) {
        if (context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        ensureChannels()
        val tag = tagOf(event.epic, event.session, event.pane, event.limit, event.alert)
        val id = notificationId(event.host, tag)
        val replacedChannelId = manager.activeNotifications.firstOrNull { it.id == id }?.notification?.channelId
        val notice = noticeOf(event)
        val channelId = if (event.silent) SILENT_CHANNEL_ID else notice.channelId
        if (!preferences.posts(channelId)) return
        val builder = Notification.Builder(context, channelId)
            .setSmallIcon(SMALL_ICON)
            .setContentTitle(notice.title)
            .setContentText(notice.text)
            .setContentIntent(contentIntent(event, tag, id))
            .setOnlyAlertOnce(replacedChannelId == channelId && event.state !in ALERTING_STATES)
            .setExtras(timeExtras(event.time))
        actionsOf(event, tag, id).forEach { builder.addAction(it) }
        manager.notify(id, builder.build())
    }

    fun showResult(notificationId: Int, title: String, text: String) {
        if (context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        ensureChannels()
        val existing = manager.activeNotifications.firstOrNull { it.id == notificationId }?.notification
        val builder = Notification.Builder(context, CHANNEL_ID)
            .setSmallIcon(SMALL_ICON)
            .setContentTitle(title)
            .setContentText(text)
            .setExtras(timeExtras(existing?.extras?.getLong(EXTRA_TIME) ?: 0L))
        existing?.contentIntent?.let { content ->
            builder.setContentIntent(content)
            builder.addAction(Notification.Action.Builder(Icon.createWithResource(context, SMALL_ICON), "Open", content).build())
        }
        manager.notify(notificationId, builder.build())
    }

    fun clear(
        host: String,
        pane: String,
        session: String,
        seen: Long,
        epic: String = "",
        limit: String = "",
        alert: String = "",
    ) {
        val id = notificationId(host, tagOf(epic, session, pane, limit, alert))
        val notice = manager.activeNotifications.firstOrNull { it.id == id } ?: return
        if (notice.notification.extras.getLong(EXTRA_TIME) <= seen) manager.cancel(id)
    }

    internal fun ensureChannels() {
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, QUESTIONS_CHANNEL_NAME, NotificationManager.IMPORTANCE_DEFAULT),
        )
        manager.createNotificationChannel(
            NotificationChannel(SILENT_CHANNEL_ID, REST_CHANNEL_NAME, NotificationManager.IMPORTANCE_LOW),
        )
    }

    private fun noticeOf(event: PushEvent): PushNotice = when {
        event.epic.isNotEmpty() -> epicNotice(event)
        event.limit.isNotEmpty() -> PushNotice(event.project, event.summary, SILENT_CHANNEL_ID)
        event.alert.isNotEmpty() -> PushNotice(event.project, event.summary, CHANNEL_ID)
        else -> paneNotice(event)
    }

    private fun epicNotice(event: PushEvent): PushNotice {
        val slug = event.epic.substringAfterLast("/")
        return when (event.block) {
            "question" -> PushNotice("$slug asks you a question", event.summary, CHANNEL_ID)
            "release_step" -> PushNotice("$slug: release step ${event.blockRef} failed", event.summary, CHANNEL_ID)
            "contract" -> PushNotice("$slug: a contract is ready to read", event.summary, SILENT_CHANNEL_ID)
            "report" -> PushNotice("$slug: its report is ready", event.summary, SILENT_CHANNEL_ID)
            else -> PushNotice("$slug needs you", event.summary, CHANNEL_ID)
        }
    }

    private fun paneNotice(event: PushEvent): PushNotice {
        val text = event.summary.ifEmpty { event.label }
        val dir = event.project
        return when (event.state) {
            "waiting" -> PushNotice("$dir ${askOf(event)}", text, CHANNEL_ID)
            "failed" -> PushNotice("$dir · failed", text, CHANNEL_ID)
            "interrupted" -> PushNotice("$dir · cut off", text, CHANNEL_ID)
            "done" -> PushNotice("$dir · done", text, SILENT_CHANNEL_ID)
            else -> PushNotice("$dir · ${event.state}", text, SILENT_CHANNEL_ID)
        }
    }

    private fun askOf(event: PushEvent): String {
        if (event.session.isNotEmpty()) {
            return if (event.question.isEmpty()) UNREADABLE_ASK else "asks: ${event.question}"
        }
        return paneAsk(event.question) ?: UNREADABLE_ASK
    }

    // paneAsk reads a pane's stored question the way the Ask does: its first question's text, else its tool.
    private fun paneAsk(stored: String): String? {
        val json = try {
            JSONObject(stored)
        } catch (e: JSONException) {
            return null
        }
        val question = json.optJSONArray("questions")?.optJSONObject(0)?.optString("question").orEmpty()
        if (question.isNotEmpty()) return "asks: $question"
        return json.optString("tool").takeIf { it.isNotEmpty() }?.let { "asks to run $it" }
    }

    private fun tagOf(epic: String, session: String, pane: String, limit: String, alert: String): String = when {
        epic.isNotEmpty() -> epic
        session.isNotEmpty() -> session
        pane.isNotEmpty() -> pane
        limit.isNotEmpty() -> limit
        alert.isNotEmpty() -> "$ALERT_TAG_PREFIX$alert"
        else -> ""
    }

    private fun actionsOf(event: PushEvent, tag: String, id: Int): List<Notification.Action> = when {
        event.epic.isNotEmpty() -> listOf(openAction(event, tag, id))
        event.limit.isNotEmpty() || event.alert.isNotEmpty() -> emptyList()
        event.session.isEmpty() && event.pane.isEmpty() -> emptyList()
        else -> paneOrSessionActions(event, tag, id)
    }

    private fun paneOrSessionActions(event: PushEvent, tag: String, id: Int): List<Notification.Action> {
        val reply = replyAction(event, tag, id)
        val options = event.options
        val waiting = event.state == "waiting"
        return when {
            waiting && options.size in 1..MAX_ANSWER_ACTIONS -> {
                val answers = options.mapIndexed { index, label -> answerAction(event, tag, id, index, label) }
                if (options.size < MAX_ANSWER_ACTIONS) answers + reply else answers
            }
            waiting && (options.size > MAX_ANSWER_ACTIONS || event.session.isNotEmpty()) ->
                listOf(openAction(event, tag, id), reply)
            waiting && event.question.isNotEmpty() ->
                KEYS.mapIndexed { index, key -> answerAction(event, tag, id, index, key) } + reply
            else -> listOf(reply)
        }
    }

    private fun contentIntent(event: PushEvent, tag: String, id: Int): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
            .putExtra(EXTRA_DIR, event.project)
        when {
            event.epic.isNotEmpty() -> {
                intent.putExtra(EXTRA_EPIC, event.epic)
                if (event.block.isNotEmpty()) intent.putExtra(EXTRA_BLOCK, event.block)
                if (event.blockRef.isNotEmpty()) intent.putExtra(EXTRA_BLOCK_REF, event.blockRef)
            }
            event.session.isNotEmpty() -> intent.putExtra(EXTRA_SESSION, event.session)
            event.pane.isNotEmpty() -> intent.putExtra(EXTRA_PANE, event.pane)
        }
        return PendingIntent.getActivity(
            context,
            requestCode(event.host, tag, "content"),
            intent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
    }

    private fun openAction(event: PushEvent, tag: String, id: Int): Notification.Action =
        Notification.Action.Builder(Icon.createWithResource(context, SMALL_ICON), "Open", contentIntent(event, tag, id))
            .build()

    // A pane's option n sends key n of its question; a session's option sends its label as the answer to its request.
    private fun answerAction(event: PushEvent, tag: String, id: Int, index: Int, label: String): Notification.Action {
        val number = (index + 1).toString()
        val intent = Intent(ACTION_KEY)
            .setPackage(context.packageName)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
            .putExtra(EXTRA_DIR, event.project)
        if (event.session.isNotEmpty()) {
            intent.putExtra(EXTRA_SESSION, event.session)
                .putExtra(EXTRA_REQUEST, event.request)
                .putExtra(EXTRA_ANSWER, label)
        } else {
            intent.putExtra(EXTRA_PANE, event.pane)
                .putExtra(EXTRA_KEY, number)
                .putExtra(EXTRA_QUESTION_HASH, event.questionHash)
        }
        val pending = PendingIntent.getBroadcast(
            context,
            requestCode(event.host, tag, "answer$number"),
            intent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return Notification.Action.Builder(Icon.createWithResource(context, SMALL_ICON), label, pending).build()
    }

    private fun replyAction(event: PushEvent, tag: String, id: Int): Notification.Action {
        val intent = Intent(ACTION_REPLY)
            .setPackage(context.packageName)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
            .putExtra(EXTRA_DIR, event.project)
            .putExtra(EXTRA_HOST, event.host)
        if (event.session.isNotEmpty()) {
            intent.putExtra(EXTRA_SESSION, event.session)
        } else {
            intent.putExtra(EXTRA_PANE, event.pane)
        }
        val pending = PendingIntent.getBroadcast(
            context,
            requestCode(event.host, tag, "reply"),
            intent,
            PendingIntent.FLAG_MUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val remoteInput = RemoteInput.Builder(REMOTE_INPUT_KEY).setLabel("Reply").build()
        return Notification.Action.Builder(Icon.createWithResource(context, SMALL_ICON), "Reply", pending)
            .addRemoteInput(remoteInput)
            .build()
    }

    private fun requestCode(host: String, tag: String, slot: String): Int = "$host|$tag|$slot".hashCode()

    private fun timeExtras(time: Long): Bundle = Bundle().apply { putLong(EXTRA_TIME, time) }

    companion object {
        const val CHANNEL_ID = "panes"
        const val SILENT_CHANNEL_ID = "panes-silent"
        const val QUESTIONS_CHANNEL_NAME = "Questions and failures"
        const val REST_CHANNEL_NAME = "Everything else"
        private const val UNREADABLE_ASK = "asks you a question"
        const val ACTION_KEY = "io.github.joshsymonds.orrery.action.KEY"
        const val ACTION_REPLY = "io.github.joshsymonds.orrery.action.REPLY"
        const val EXTRA_PANE = "io.github.joshsymonds.orrery.extra.PANE"
        const val EXTRA_SESSION = "io.github.joshsymonds.orrery.extra.SESSION"
        const val EXTRA_EPIC = "io.github.joshsymonds.orrery.extra.EPIC"
        const val EXTRA_BLOCK = "io.github.joshsymonds.orrery.extra.BLOCK"
        const val EXTRA_BLOCK_REF = "io.github.joshsymonds.orrery.extra.BLOCK_REF"
        const val EXTRA_KEY = "io.github.joshsymonds.orrery.extra.KEY"
        const val EXTRA_QUESTION_HASH = "io.github.joshsymonds.orrery.extra.QUESTION_HASH"
        const val EXTRA_NOTIFICATION_ID = "io.github.joshsymonds.orrery.extra.NOTIFICATION_ID"
        const val EXTRA_TIME = "io.github.joshsymonds.orrery.extra.TIME"
        const val EXTRA_DIR = "io.github.joshsymonds.orrery.extra.DIR"
        const val EXTRA_HOST = "io.github.joshsymonds.orrery.extra.HOST"
        const val EXTRA_REQUEST = "io.github.joshsymonds.orrery.extra.REQUEST"
        const val EXTRA_ANSWER = "io.github.joshsymonds.orrery.extra.ANSWER"
        const val REMOTE_INPUT_KEY = "reply"
        private const val ALERT_TAG_PREFIX = "alert:"
        private val KEYS = listOf("1", "2", "3")
        private const val MAX_ANSWER_ACTIONS = 3
        private val ALERTING_STATES = setOf("waiting", "failed")
        private val SMALL_ICON = R.drawable.ic_notification

        fun notificationId(host: String, key: String): Int = "$host|$key".hashCode()
    }
}
