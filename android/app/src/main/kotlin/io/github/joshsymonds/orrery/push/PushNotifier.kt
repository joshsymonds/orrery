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

data class PushEvent(
    val pane: String,
    val host: String,
    val project: String,
    val state: String,
    val summary: String,
    val label: String,
    val question: String,
    val questionHash: String,
    val session: String = "",
    val epic: String = "",
    val limit: String = "",
    val time: Long = 0L,
    val silent: Boolean = false,
)

class PushNotifier(private val context: Context) {
    private val manager = context.getSystemService(NotificationManager::class.java)

    fun show(event: PushEvent) {
        if (context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        ensureChannels()
        val tag = tagOf(event.epic, event.session, event.pane, event.limit)
        val id = notificationId(event.host, tag)
        val replacedChannelId = manager.activeNotifications.firstOrNull { it.id == id }?.notification?.channelId
        val channelId = if (event.silent) SILENT_CHANNEL_ID else CHANNEL_ID
        val builder = Notification.Builder(context, channelId)
            .setSmallIcon(SMALL_ICON)
            .setContentTitle("${event.project} · ${event.state}")
            .setContentText(event.summary.ifEmpty { event.label })
            .setContentIntent(contentIntent(event, tag))
            .setOnlyAlertOnce(replacedChannelId == channelId && event.state !in ALERTING_STATES)
            .setExtras(timeExtras(event.time))
        if (event.session.isEmpty() && event.pane.isNotEmpty()) {
            if (event.question.isNotEmpty()) {
                KEYS.forEach { key -> builder.addAction(keyAction(event, tag, key, id)) }
            }
            builder.addAction(replyAction(event, tag, id))
        }
        manager.notify(id, builder.build())
    }

    fun showResult(notificationId: Int, text: String) {
        if (context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) return
        ensureChannels()
        val existing = manager.activeNotifications.firstOrNull { it.id == notificationId }?.notification
        val builder = Notification.Builder(context, CHANNEL_ID)
            .setSmallIcon(SMALL_ICON)
            .setContentText(text)
            .setExtras(timeExtras(existing?.extras?.getLong(EXTRA_TIME) ?: 0L))
        if (existing != null) {
            builder.setContentTitle(existing.extras.getCharSequence(Notification.EXTRA_TITLE))
            existing.contentIntent?.let { builder.setContentIntent(it) }
        }
        manager.notify(notificationId, builder.build())
    }

    fun clear(host: String, pane: String, session: String, seen: Long, epic: String = "", limit: String = "") {
        val id = notificationId(host, tagOf(epic, session, pane, limit))
        val notice = manager.activeNotifications.firstOrNull { it.id == id } ?: return
        if (notice.notification.extras.getLong(EXTRA_TIME) <= seen) manager.cancel(id)
    }

    private fun ensureChannels() {
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "Panes", NotificationManager.IMPORTANCE_DEFAULT),
        )
        manager.createNotificationChannel(
            NotificationChannel(SILENT_CHANNEL_ID, "Panes (silent)", NotificationManager.IMPORTANCE_LOW),
        )
    }

    private fun tagOf(epic: String, session: String, pane: String, limit: String): String =
        epic.ifEmpty { session.ifEmpty { pane.ifEmpty { limit } } }

    private fun contentIntent(event: PushEvent, tag: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
        when {
            event.epic.isNotEmpty() -> intent.putExtra(EXTRA_EPIC, event.epic)
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

    private fun keyAction(event: PushEvent, tag: String, key: String, id: Int): Notification.Action {
        val intent = Intent(ACTION_KEY)
            .setPackage(context.packageName)
            .putExtra(EXTRA_PANE, event.pane)
            .putExtra(EXTRA_KEY, key)
            .putExtra(EXTRA_QUESTION_HASH, event.questionHash)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
        val pending = PendingIntent.getBroadcast(
            context,
            requestCode(event.host, tag, key),
            intent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return Notification.Action.Builder(Icon.createWithResource(context, SMALL_ICON), key, pending).build()
    }

    private fun replyAction(event: PushEvent, tag: String, id: Int): Notification.Action {
        val intent = Intent(ACTION_REPLY)
            .setPackage(context.packageName)
            .putExtra(EXTRA_PANE, event.pane)
            .putExtra(EXTRA_NOTIFICATION_ID, id)
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
        const val ACTION_KEY = "io.github.joshsymonds.orrery.action.KEY"
        const val ACTION_REPLY = "io.github.joshsymonds.orrery.action.REPLY"
        const val EXTRA_PANE = "io.github.joshsymonds.orrery.extra.PANE"
        const val EXTRA_SESSION = "io.github.joshsymonds.orrery.extra.SESSION"
        const val EXTRA_EPIC = "io.github.joshsymonds.orrery.extra.EPIC"
        const val EXTRA_KEY = "io.github.joshsymonds.orrery.extra.KEY"
        const val EXTRA_QUESTION_HASH = "io.github.joshsymonds.orrery.extra.QUESTION_HASH"
        const val EXTRA_NOTIFICATION_ID = "io.github.joshsymonds.orrery.extra.NOTIFICATION_ID"
        const val EXTRA_TIME = "io.github.joshsymonds.orrery.extra.TIME"
        const val REMOTE_INPUT_KEY = "reply"
        private val KEYS = listOf("1", "2", "3")
        private val ALERTING_STATES = setOf("waiting", "failed")
        private val SMALL_ICON = android.R.drawable.ic_dialog_info

        fun notificationId(host: String, key: String): Int = "$host|$key".hashCode()
    }
}
