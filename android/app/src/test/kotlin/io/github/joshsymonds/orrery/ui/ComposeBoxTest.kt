package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.lifecycle.Lifecycle
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.push.KeptReplies
import java.io.IOException
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val OTHER_PANE_ID = "%9"
private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val OTHER_SESSION_ID = "fedcba9876543210fedcba9876543210"
private const val HOST = "example-host"
private const val OTHER_HOST = "other-host"
private const val PANE = "pane"
private const val SESSION = "session"
private const val FIRST_KEPT = "synthetic first kept reply"
private const val SECOND_KEPT = "synthetic second kept reply"
private const val TYPED = "synthetic typed draft"
private const val OTHER_TEXT = "synthetic reply kept for another conversation"
private const val HOST_PATH = "/orrery.v1.Host/Status"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private val HOST_BODY: HostStatusResponse = HostStatusResponse.newBuilder().setHost(HOST).build()

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ComposeBoxTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun paneTakesOnlyItsOwnKeptReplyWhenItOpens() {
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        keep(HOST, PANE, OTHER_PANE_ID, OTHER_TEXT)
        keep(HOST, SESSION, PANE_ID, OTHER_TEXT)
        keep(OTHER_HOST, PANE, PANE_ID, OTHER_TEXT)
        server.respond(HOST_PATH, HOST_BODY)
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client) }
        }

        composeRule.awaitUntil("the pane's dock takes its kept reply") { fieldText().isNotEmpty() }
        assertEquals(FIRST_KEPT, fieldText())
    }

    @Test
    fun sessionTakesOnlyItsOwnKeptReplyWhenItOpens() {
        keep(HOST, SESSION, SESSION_ID, FIRST_KEPT)
        keep(HOST, SESSION, OTHER_SESSION_ID, OTHER_TEXT)
        keep(HOST, PANE, SESSION_ID, OTHER_TEXT)
        keep(OTHER_HOST, SESSION, SESSION_ID, OTHER_TEXT)
        server.route(WATCH_PATH) { Reply.Stream(emptyList(), StreamEnd.Open) }
        server.respond(HOST_PATH, HOST_BODY)
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client) }
        }

        composeRule.awaitUntil("the session's dock takes its kept reply") { fieldText().isNotEmpty() }
        assertEquals(FIRST_KEPT, fieldText())
    }

    @Test
    fun dockJoinsKeptRepliesAfterTextAndAfterEarlierKeptReplies() {
        val owner = HandLifecycleOwner()
        val shown = mutableStateOf(true)
        setDock(owner, shown)
        composeRule.runOnUiThread { editField().setText(TYPED) }
        composeRule.waitForIdle()
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        keep(HOST, PANE, PANE_ID, SECOND_KEPT)

        moveLifecycle(owner, Lifecycle.State.STARTED)

        composeRule.awaitUntil("the kept replies join the draft") { fieldText() != TYPED }
        assertEquals("$TYPED\n$FIRST_KEPT\n$SECOND_KEPT", fieldText())
    }

    @Test
    fun dockTakesNewKeptReplyWhenTheAppReturnsWhileTheConversationIsOnScreen() {
        val owner = HandLifecycleOwner()
        moveLifecycle(owner, Lifecycle.State.STARTED)
        setDock(owner, mutableStateOf(true))
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        moveLifecycle(owner, Lifecycle.State.CREATED)
        moveLifecycle(owner, Lifecycle.State.STARTED)
        composeRule.awaitUntil("the first return takes the kept reply") { fieldText() == FIRST_KEPT }

        keep(HOST, PANE, PANE_ID, SECOND_KEPT)
        moveLifecycle(owner, Lifecycle.State.CREATED)
        moveLifecycle(owner, Lifecycle.State.STARTED)

        composeRule.awaitUntil("the second return takes the new kept reply") { fieldText() != FIRST_KEPT }
        assertEquals("$FIRST_KEPT\n$SECOND_KEPT", fieldText())
    }

    @Test
    fun dockDoesNotJoinAKeptReplyTwiceWhenTheAppReturnsTwice() {
        val owner = HandLifecycleOwner()
        moveLifecycle(owner, Lifecycle.State.STARTED)
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        setDock(owner, mutableStateOf(true))
        composeRule.awaitUntil("the page open takes the kept reply") { fieldText() == FIRST_KEPT }

        moveLifecycle(owner, Lifecycle.State.CREATED)
        moveLifecycle(owner, Lifecycle.State.STARTED)
        moveLifecycle(owner, Lifecycle.State.CREATED)
        moveLifecycle(owner, Lifecycle.State.STARTED)
        keep(HOST, PANE, PANE_ID, SECOND_KEPT)
        moveLifecycle(owner, Lifecycle.State.CREATED)
        moveLifecycle(owner, Lifecycle.State.STARTED)

        composeRule.awaitUntil("the last return takes the new kept reply") { fieldText() != FIRST_KEPT }
        assertEquals("$FIRST_KEPT\n$SECOND_KEPT", fieldText())
    }

    @Test
    fun reopenedPageTakesAKeptReplyLeftUnsent() {
        val owner = HandLifecycleOwner()
        moveLifecycle(owner, Lifecycle.State.STARTED)
        val shown = mutableStateOf(true)
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        setDock(owner, shown)
        composeRule.awaitUntil("the page open takes the kept reply") { fieldText() == FIRST_KEPT }

        composeRule.runOnIdle { shown.value = false }
        composeRule.runOnIdle { shown.value = true }

        composeRule.awaitUntil("the reopened page takes the kept reply") { fieldText() == FIRST_KEPT }
        assertEquals(listOf(FIRST_KEPT), storedTexts())
    }

    @Test
    fun sentKeptReplyIsLetGoOnlyOnceTheDraftIsSent() {
        val owner = HandLifecycleOwner()
        moveLifecycle(owner, Lifecycle.State.STARTED)
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        setDock(owner, mutableStateOf(true))
        composeRule.awaitUntil("the page open takes the kept reply") { fieldText() == FIRST_KEPT }
        assertEquals(listOf(FIRST_KEPT), storedTexts())

        composeRule.onNodeWithText("Send").performClick()

        composeRule.awaitUntil("the sent draft is cleared and the store lets go") {
            fieldText().isEmpty() && storedTexts().isEmpty()
        }
    }

    @Test
    fun failedSendKeepsTheKeptReplyInTheStore() {
        val owner = HandLifecycleOwner()
        moveLifecycle(owner, Lifecycle.State.STARTED)
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        setDock(owner, mutableStateOf(true), send = { throw IOException("synthetic refusal") })
        composeRule.awaitUntil("the page open takes the kept reply") { fieldText() == FIRST_KEPT }

        composeRule.onNodeWithText("Send").performClick()

        composeRule.awaitUntil("the send refusal shows") {
            composeRule.onAllNodesWithText(DockLines.sendRefused).fetchSemanticsNodes().isNotEmpty()
        }
        assertEquals(FIRST_KEPT, fieldText())
        assertEquals(listOf(FIRST_KEPT), storedTexts())
    }

    @Test
    fun clearedDraftLetsTheKeptReplyGo() {
        val owner = HandLifecycleOwner()
        moveLifecycle(owner, Lifecycle.State.STARTED)
        keep(HOST, PANE, PANE_ID, FIRST_KEPT)
        setDock(owner, mutableStateOf(true))
        composeRule.awaitUntil("the page open takes the kept reply") { fieldText() == FIRST_KEPT }
        assertEquals(listOf(FIRST_KEPT), storedTexts())

        composeRule.runOnUiThread { editField().setText("") }

        composeRule.awaitUntil("the cleared draft lets the store go") { storedTexts().isEmpty() }
    }

    private fun setDock(owner: HandLifecycleOwner, shown: MutableState<Boolean>, send: (String) -> Unit = {}) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides owner) {
                if (shown.value) {
                    val draft = rememberDockDraft(PANE_ID)
                    JoinKeptReplies(draft = draft, host = HOST, kind = PANE, id = PANE_ID)
                    DockBox(draft = draft, client = client, paneId = PANE_ID, send = send)
                }
            }
        }
        composeRule.waitForIdle()
    }

    private fun moveLifecycle(owner: HandLifecycleOwner, state: Lifecycle.State) {
        composeRule.runOnIdle { owner.lifecycle.currentState = state }
    }

    private fun keep(host: String, kind: String, id: String, text: String) {
        KeptReplies(composeRule.activity).keep(host, kind, id, text)
    }

    private fun storedTexts(): List<String> =
        KeptReplies(composeRule.activity).kept(HOST, PANE, PANE_ID).map { it.text }

    private fun fieldText(): String = findEditText(composeRule.activity.window.decorView)?.text?.toString().orEmpty()

    private fun editField(): EditText {
        return checkNotNull(findEditText(composeRule.activity.window.decorView)) {
            "DockBox must host an EditText"
        }
    }

    private fun findEditText(view: View): EditText? {
        if (view is EditText) {
            return view
        }
        if (view is ViewGroup) {
            for (index in 0 until view.childCount) {
                findEditText(view.getChildAt(index))?.let { return it }
            }
        }
        return null
    }
}
