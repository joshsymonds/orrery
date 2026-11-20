package io.github.joshsymonds.orrery.push

import android.app.Notification
import android.app.NotificationManager
import android.app.RemoteInput
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import com.connectrpc.Code
import io.github.joshsymonds.orrery.MainActivity
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextResponse
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.ACTION_REPLY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_ANSWER
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_DIR
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_NOTIFICATION_ID
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_PANE
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_QUESTION_HASH
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_REQUEST
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.EXTRA_SESSION
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.REMOTE_INPUT_KEY
import io.github.joshsymonds.orrery.push.PushNotifier.Companion.notificationId
import java.net.ServerSocket
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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
class ReplyReceiverTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val manager = context.getSystemService(NotificationManager::class.java)
    private val server = FakeOrreryServer()

    private val dir = "orrery-demo"
    private val pane = "%3"
    private val session = "sess-1"
    private val requestId = "req-1"
    private val id = notificationId("devbox", pane)
    private val questionHash = "h"
    private val keysPath = "/orrery.v1.Panes/SendKeys"
    private val textPath = "/orrery.v1.Panes/SendText"
    private val answerPath = "/orrery.v1.Control/Answer"
    private val sayPath = "/orrery.v1.Control/Say"
    private val seenPath = "/orrery.v1.Notices/Seen"

    private val promptChangedText = "The question changed before your answer arrived. Open it to see what it asks now."
    private val refusedAnswerText = "The server refused it. Open it to see why."
    private val refusedReplyText = "The server refused your reply."
    private val noAnswerText = "The server did not answer in time; it may have landed. Open it to see."
    private val answerNotReachedText = "Your answer did not reach the server."
    private val replyNotReachedText = "Your reply did not reach the server."

    @Before
    fun setUp() {
        ServerSettings(context).saveServerUrl(server.baseUrl)
        shadowOf(RuntimeEnvironment.getApplication()).grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        PushNotifier(context).show(
            PushEvent(
                pane = pane,
                host = "devbox",
                project = dir,
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
    fun keyActionPostsOneKeysRequestThenMarksPaneSeenAndCancels() {
        server.respond(keysPath, SendKeysResponse.getDefaultInstance())
        server.respond(seenPath, NoticesSeenResponse.getDefaultInstance())

        deliverKey("2")
        awaitDelivered { paths().contains(seenPath) && !isShowing() }

        assertEquals(listOf(keysPath, seenPath), paths())
        val request = requestTo(keysPath)
        assertEquals("POST", request.method)
        assertEquals(
            SendKeysRequest.newBuilder().setPane(pane).addKeys("2").setExpectQuestion(questionHash).build(),
            SendKeysRequest.parseFrom(request.body),
        )
        assertEquals(
            NoticesSeenRequest.newBuilder().setKey(NoticeKey.newBuilder().setPane(pane)).build(),
            NoticesSeenRequest.parseFrom(requestTo(seenPath).body),
        )
        assertFalse(isShowing())
    }

    @Test
    fun keyActionOnPromptChangedShowsNotAnsweredAndKeepsNotification() {
        server.fail(keysPath, Code.ABORTED, "question refused")

        deliverKey("2")
        awaitDelivered { shownTitle() == "$dir: not answered" }

        assertEquals(listOf(keysPath), paths())
        assertEquals("$dir: not answered", shownTitle())
        assertEquals(promptChangedText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun keyActionWithNoAnswerShowsNotConfirmedAndKeepsNotification() {
        server.drop(keysPath)

        deliverKey("2")
        awaitDelivered { shownTitle() == "$dir: not confirmed" }

        assertEquals(listOf(keysPath), paths())
        assertEquals(noAnswerText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun keyActionThatNeverReachedTheServerShowsNotSent() {
        pointAtClosedPort()

        deliverKey("2")
        awaitDelivered { shownTitle() == "$dir: not sent" }

        assertEquals(answerNotReachedText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun keyActionWithoutItsQuestionHashSendsNothingAndShowsNotSent() {
        deliverKey("2", hash = "")
        awaitDelivered { shownTitle() == "$dir: not sent" }

        assertTrue(server.requests.isEmpty())
        assertEquals(answerNotReachedText, shownText())
    }

    @Test
    fun failedSeenAfterLandedAnswerShowsNothing() {
        server.respond(keysPath, SendKeysResponse.getDefaultInstance())
        server.fail(seenPath, Code.INTERNAL_ERROR, "boom")

        deliverKey("2")
        awaitDelivered { paths().contains(seenPath) && !isShowing() }

        assertEquals(listOf(keysPath, seenPath), paths())
        assertNull(shown())
    }

    @Test
    fun replyPostsTypedTextOnceThenMarksPaneSeenAndCancels() {
        server.respond(textPath, SendTextResponse.getDefaultInstance())
        server.respond(seenPath, NoticesSeenResponse.getDefaultInstance())

        deliverPaneReply("synthetic reply")
        awaitDelivered { paths().contains(seenPath) && !isShowing() }

        assertEquals(listOf(textPath, seenPath), paths())
        val request = requestTo(textPath)
        assertEquals("POST", request.method)
        assertEquals(
            SendTextRequest.newBuilder().setPane(pane).setText("synthetic reply").build(),
            SendTextRequest.parseFrom(request.body),
        )
        assertEquals(
            NoticesSeenRequest.newBuilder().setKey(NoticeKey.newBuilder().setPane(pane)).build(),
            NoticesSeenRequest.parseFrom(requestTo(seenPath).body),
        )
        assertFalse(isShowing())
    }

    @Test
    fun replyRefusedShowsNotSentAndKeepsNotification() {
        server.fail(textPath, Code.INTERNAL_ERROR, "boom")

        deliverPaneReply("synthetic reply")
        awaitDelivered { shownTitle() == "$dir: not sent" }

        assertEquals(listOf(textPath), paths())
        assertEquals(refusedReplyText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun replyThatNeverReachedTheServerShowsNotSent() {
        pointAtClosedPort()

        deliverPaneReply("synthetic reply")
        awaitDelivered { shownTitle() == "$dir: not sent" }

        assertEquals(replyNotReachedText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun blankReplySendsNothingAndShowsNotSent() {
        deliverPaneReply("   ")
        awaitDelivered { shownTitle() == "$dir: not sent" }

        assertTrue(server.requests.isEmpty())
        assertEquals(replyNotReachedText, shownText())
    }

    @Test
    fun sessionOptionAnswersItsRequestWithItsLabelThenMarksSessionSeen() {
        server.respond(answerPath, AnswerResponse.getDefaultInstance())
        server.respond(seenPath, NoticesSeenResponse.getDefaultInstance())

        deliverSessionOption(label = "Yes")
        awaitDelivered { paths().contains(seenPath) && !isShowing() }

        assertEquals(listOf(answerPath, seenPath), paths())
        assertEquals(
            AnswerRequest.newBuilder().setId(session).setRequestId(requestId).setAnswer("Yes").build(),
            AnswerRequest.parseFrom(requestTo(answerPath).body),
        )
        assertEquals(
            NoticesSeenRequest.newBuilder().setKey(NoticeKey.newBuilder().setSession(session)).build(),
            NoticesSeenRequest.parseFrom(requestTo(seenPath).body),
        )
        assertFalse(isShowing())
    }

    @Test
    fun sessionOptionForAGoneRequestShowsNotAnsweredAndKeepsNotification() {
        server.fail(answerPath, Code.NOT_FOUND, "")

        deliverSessionOption(label = "Yes")
        awaitDelivered { shownTitle() == "$dir: not answered" }

        assertEquals(listOf(answerPath), paths())
        assertEquals(promptChangedText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun sessionOptionRefusedShowsNotAnsweredAndKeepsNotification() {
        server.fail(answerPath, Code.FAILED_PRECONDITION, "not waiting")

        deliverSessionOption(label = "Yes")
        awaitDelivered { shownTitle() == "$dir: not answered" }

        assertEquals(listOf(answerPath), paths())
        assertEquals(refusedAnswerText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun sessionOptionWithNoAnswerShowsNotConfirmed() {
        server.drop(answerPath)

        deliverSessionOption(label = "Yes")
        awaitDelivered { shownTitle() == "$dir: not confirmed" }

        assertEquals(noAnswerText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun sessionOptionNotReachedShowsNotSent() {
        pointAtClosedPort()

        deliverSessionOption(label = "Yes")
        awaitDelivered { shownTitle() == "$dir: not sent" }

        assertEquals(answerNotReachedText, shownText())
        assertOpensThisNotice()
    }

    @Test
    fun replyToSessionSaysWithFreshPromptIdThenMarksSessionSeen() {
        server.respond(sayPath, SayResponse.getDefaultInstance())
        server.respond(seenPath, NoticesSeenResponse.getDefaultInstance())

        deliverSessionReply("synthetic reply")
        awaitDelivered { paths().contains(seenPath) && !isShowing() }

        assertEquals(listOf(sayPath, seenPath), paths())
        val say = SayRequest.parseFrom(requestTo(sayPath).body)
        assertTrue(say.promptId.isNotEmpty())
        assertEquals(
            SayRequest.newBuilder().setId(session).setText("synthetic reply").setPromptId(say.promptId).build(),
            say,
        )
        assertEquals(
            NoticesSeenRequest.newBuilder().setKey(NoticeKey.newBuilder().setSession(session)).build(),
            NoticesSeenRequest.parseFrom(requestTo(seenPath).body),
        )
        assertFalse(isShowing())
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

    private fun deliverKey(key: String, hash: String = questionHash) = send(
        Intent(ACTION_KEY)
            .putExtra(EXTRA_PANE, pane)
            .putExtra(EXTRA_KEY, key)
            .putExtra(EXTRA_QUESTION_HASH, hash),
    )

    private fun deliverSessionOption(label: String) = send(
        Intent(ACTION_KEY)
            .putExtra(EXTRA_SESSION, session)
            .putExtra(EXTRA_REQUEST, requestId)
            .putExtra(EXTRA_ANSWER, label),
    )

    private fun deliverPaneReply(text: String) = deliverReply(text, Intent(ACTION_REPLY).putExtra(EXTRA_PANE, pane))

    private fun deliverSessionReply(text: String) = deliverReply(text, Intent(ACTION_REPLY).putExtra(EXTRA_SESSION, session))

    private fun deliverReply(text: String, intent: Intent) {
        val results = Bundle().apply { putCharSequence(REMOTE_INPUT_KEY, text) }
        RemoteInput.addResultsToIntent(arrayOf(RemoteInput.Builder(REMOTE_INPUT_KEY).build()), intent, results)
        send(intent)
    }

    private fun send(intent: Intent) {
        context.sendBroadcast(
            intent.setPackage(context.packageName)
                .putExtra(EXTRA_NOTIFICATION_ID, id)
                .putExtra(EXTRA_DIR, dir),
        )
    }

    /** Points the app at an address nothing listens on, so a request never reaches a server. */
    private fun pointAtClosedPort() {
        val port = ServerSocket(0).use { it.localPort }
        ServerSettings(context).saveServerUrl("http://127.0.0.1:$port")
    }

    private fun paths(): List<String> = server.requests.map { it.rawPath }

    private fun requestTo(path: String) = server.requests.single { it.rawPath == path }

    private fun isShowing(): Boolean = manager.activeNotifications.any { it.id == id }

    private fun shown(): Notification? = manager.activeNotifications.firstOrNull { it.id == id }?.notification

    private fun shownTitle(): String? = shown()?.extras?.getCharSequence(Notification.EXTRA_TITLE)?.toString()

    private fun shownText(): String? = shown()?.extras?.getCharSequence(Notification.EXTRA_TEXT)?.toString()

    /** The shown result keeps the notice's Open action, which opens the notice's own activity. */
    private fun assertOpensThisNotice() {
        val actions = checkNotNull(shown()).actions.orEmpty()
        assertEquals(listOf("Open"), actions.map { it.title.toString() })
        val opened = shadowOf(actions.single().actionIntent).savedIntent
        assertEquals(MainActivity::class.java.name, opened.component?.className)
        assertEquals(id, opened.getIntExtra(EXTRA_NOTIFICATION_ID, -1))
    }

    /**
     * Idles the main looper until [delivered] holds and the receiver's work has finished. The receiver's call runs on
     * other threads; the wait ends on that work, not on a clock.
     */
    private fun awaitDelivered(delivered: () -> Boolean) {
        Quiescence.awaitUntil(
            "the action delivered",
            step = { shadowOf(Looper.getMainLooper()).idle() },
            condition = { delivered() && Quiescence.inFlight() == null },
        )
    }
}
