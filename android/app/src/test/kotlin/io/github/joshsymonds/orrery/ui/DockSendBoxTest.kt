package io.github.joshsymonds.orrery.ui

import android.os.Looper
import android.text.InputType
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.content.res.ResourcesCompat
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val TEXT_PATH = "/orrery.v1.Panes/SendText"
private const val OTHER_PANE_ID = "%2"
private const val DRAFT = "first line\nsecond line"
private const val OTHER_DRAFT = "text for the other pane"
private const val HINT = "synthetic hint"
private const val STATUS = "synthetic status line"
private const val COMPOSER_TAG = "composer"
private const val SEND_ALIGNMENT_TOLERANCE_PX = 1f
private const val LONG_DRAFT_LINES = 12
private val SEND_REFUSED_LABEL = DockLines.sendRefused
private val SEND_UNKNOWN_LABEL = DockLines.sendUnknown
private const val APPENDED_TEXT = "appended by the dock"
private const val HANG_GUARD_SECONDS = 10L
private const val COMPOSE_TIMEOUT_MS = 200
private const val SLOW_ANSWER_MS = 2_000L
private val CAPPED_FIELD_HEIGHT = 60.dp
private const val LIST_ROWS = 40

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockSendBoxTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val server = FakeOrreryServer()
    private val heldSendReply = CountDownLatch(1)
    private lateinit var boxDraft: DockDraft

    @After
    fun tearDown() {
        heldSendReply.countDown()
        server.close()
    }

    @Test
    fun editorKeepsAutocorrectCapitalisationAndSuggestions() {
        setBox()

        val info = EditorInfo()
        assertNotNull(editField().onCreateInputConnection(info))

        assertEquals(InputType.TYPE_CLASS_TEXT, info.inputType and InputType.TYPE_MASK_CLASS)
        assertTrue(info.inputType and InputType.TYPE_TEXT_FLAG_MULTI_LINE != 0)
        assertTrue(info.inputType and InputType.TYPE_TEXT_FLAG_AUTO_CORRECT != 0)
        assertTrue(info.inputType and InputType.TYPE_TEXT_FLAG_CAP_SENTENCES != 0)
        assertEquals(0, info.inputType and InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS)
    }

    @Test
    fun editorDrawsInOrreryMono() {
        setBox()

        val orreryMono = ResourcesCompat.getFont(composeRule.activity, R.font.orrery_mono_regular)
        assertNotNull(orreryMono)
        assertSame(orreryMono, editField().typeface)
    }

    @Test
    fun sendPostsWholeDraftInOneCallAndClearsOnSuccess() {
        server.respond(TEXT_PATH, SendTextResponse.getDefaultInstance())
        setBox()
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil { editField().text.isEmpty() }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(TEXT_PATH, request.rawPath)
        assertEquals(
            SendTextRequest.newBuilder().setPane(PANE_ID).setText(DRAFT).build(),
            SendTextRequest.parseFrom(request.body),
        )
    }

    @Test
    fun failedSendKeepsDraftAndShowsSendRefused() {
        server.fail(TEXT_PATH, Code.INTERNAL_ERROR, "boom")
        setBox()
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(SEND_REFUSED_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(DRAFT, editField().text.toString())
        composeRule.onNodeWithText("Send").assertIsEnabled()
    }

    @Test
    fun answeredSendShowsNeitherLabel() {
        server.respond(TEXT_PATH, SendTextResponse.getDefaultInstance())
        setBox()
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil { editField().text.isEmpty() }
        composeRule.waitForIdle()

        assertNoOutcomeLabel()
    }

    @Test
    fun refusedConnectionKeepsDraftAndShowsSendRefused() {
        val closedPort = ServerSocket(0, 0, InetAddress.getByName("127.0.0.1")).use { it.localPort }
        setBox(client = OrreryClient("http://127.0.0.1:$closedPort"))
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(SEND_REFUSED_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(DRAFT, editField().text.toString())
        assertTrue(composeRule.onAllNodesWithText(SEND_UNKNOWN_LABEL).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun slowSendKeepsDraftAndShowsSendUnknownNotSendRefused() {
        server.respond(TEXT_PATH, SendTextResponse.getDefaultInstance(), delayMillis = SLOW_ANSWER_MS)
        setBox(client = slowClient())
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(SEND_UNKNOWN_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(DRAFT, editField().text.toString())
        assertTrue(composeRule.onAllNodesWithText(SEND_REFUSED_LABEL).fetchSemanticsNodes().isEmpty())
        composeRule.onNodeWithText("Send").assertIsEnabled()
    }

    @Test
    fun nextSendClearsSendUnknownLabel() {
        server.route(TEXT_PATH) {
            heldSendReply.await()
            Reply.Message(SendTextResponse.getDefaultInstance(), 0L)
        }
        setBox(client = slowClient())
        setDraft(DRAFT)
        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(SEND_UNKNOWN_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        // FakeOrreryServer answers one request at a time, so the second request would queue behind the
        // held answer and miss the compose timeout. Release that answer and wait until nothing is in flight.
        heldSendReply.countDown()
        composeRule.awaitUntil("the held answer is sent") { Quiescence.inFlight() == null }
        server.respond(TEXT_PATH, SendTextResponse.getDefaultInstance())
        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil { editField().text.isEmpty() }
        composeRule.waitForIdle()

        assertEquals(2, server.requests.size)
        assertNoOutcomeLabel()
    }

    // The field's enabled change is posted out of Compose's apply (DockBox's update), because on a phone a focused
    // field's change restarts the keyboard's input mid-apply and crashes. Robolectric's keyboard never runs that
    // restart, so this pins what the post must keep: a focused field under a list is disabled while its send is out
    // and enabled once it lands.
    @Test
    fun focusedFieldUnderAListIsDisabledWhileItsSendIsOut() {
        server.route(TEXT_PATH) {
            heldSendReply.await()
            Reply.Message(SendTextResponse.getDefaultInstance(), 0L)
        }
        val client = slowClient()
        composeRule.setContent {
            val box = rememberDockDraft(PANE_ID)
            boxDraft = box
            Column(Modifier.fillMaxSize()) {
                LazyColumn(Modifier.weight(1f)) {
                    items(LIST_ROWS) { row -> Text("row $row") }
                }
                DockBox(draft = box, paneId = PANE_ID, client = client, edge = ComposeEdge.IDLE, hint = HINT)
            }
        }
        composeRule.waitForIdle()
        setDraft(DRAFT)
        composeRule.runOnUiThread {
            val field = editField()
            assertTrue(field.requestFocus())
            val keyboard = field.context.getSystemService(InputMethodManager::class.java)
            keyboard.showSoftInput(field, 0)
            keyboard.restartInput(field)
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitForIdle()

        composeRule.runOnIdle { assertTrue(!editField().isEnabled) }
        heldSendReply.countDown()
        composeRule.awaitUntil { editField().text.isEmpty() }
        composeRule.runOnIdle { assertTrue(editField().isEnabled) }
    }

    @Test
    fun sendIsDisabledWhileDraftIsBlank() {
        setBox()

        composeRule.onNodeWithText("Send").assertIsNotEnabled()

        setDraft("   ")
        composeRule.onNodeWithText("Send").assertIsNotEnabled()
    }

    @Test
    fun draftDoesNotFollowPaneChange() {
        server.respond(TEXT_PATH, SendTextResponse.getDefaultInstance())
        val paneId = mutableStateOf(PANE_ID)
        setBox(paneId)
        setDraft(DRAFT)

        composeRule.runOnIdle { paneId.value = OTHER_PANE_ID }
        composeRule.waitForIdle()

        assertEquals("", editField().text.toString())

        setDraft(OTHER_DRAFT)
        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil { editField().text.isEmpty() }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals(TEXT_PATH, request.rawPath)
        assertEquals(
            SendTextRequest.newBuilder().setPane(OTHER_PANE_ID).setText(OTHER_DRAFT).build(),
            SendTextRequest.parseFrom(request.body),
        )
    }

    @Test
    fun sendSitsInTheFieldRowAtItsTrailingEdge() {
        setBox()
        setDraft(DRAFT)

        val field = fieldBoundsInWindow()
        val send = composeRule.onNodeWithText("Send").fetchSemanticsNode().boundsInWindow
        assertTrue("Send left ${send.left} should be right of field right ${field.right}", send.left > field.right)
        assertEquals(field.bottom, send.bottom, SEND_ALIGNMENT_TOLERANCE_PX)
    }

    @Test
    fun hintShowsInTheEmptyField() {
        setBox(hint = HINT)

        assertEquals(HINT, editField().hint.toString())
    }

    @Test
    fun statusDrawsInsideComposerAboveField() {
        setBox(status = { Text(STATUS) })

        composeRule.onNodeWithText(STATUS).assertIsDisplayed()
        val composer = composeRule.onNodeWithTag(COMPOSER_TAG).fetchSemanticsNode().boundsInWindow
        val status = composeRule.onNodeWithText(STATUS).fetchSemanticsNode().boundsInWindow
        val field = fieldBoundsInWindow()
        assertTrue(
            "status $status should sit inside composer $composer",
            status.left >= composer.left && status.top >= composer.top &&
                status.right <= composer.right && status.bottom <= composer.bottom,
        )
        assertTrue("status bottom ${status.bottom} should be above field top ${field.top}", status.bottom <= field.top)
    }

    @Test
    fun heldEdgeTagsTheFieldHeld() {
        setBox(edge = ComposeEdge.HELD)

        assertEquals(1, edgeTagCount("held"))
    }

    @Test
    fun boxGivenNoEdgeTagsTheFieldIdle() {
        composeRule.setContent {
            DockBox(draft = rememberDockDraft(PANE_ID), client = OrreryClient(server.baseUrl), paneId = PANE_ID)
        }
        composeRule.waitForIdle()

        assertEquals(1, edgeTagCount("idle"))
    }

    @Test
    fun composeEdgeOfMapsEmptyStateToIdle() {
        assertEquals(ComposeEdge.IDLE, composeEdgeOf(""))
    }

    @Test
    fun editorIsCappedAtComposeMaxLines() {
        setBox()

        assertEquals(COMPOSE_MAX_LINES, editField().maxLines)
    }

    @Test
    fun fieldGrowsWithItsDraftButStopsGrowingAtTheLineCap() {
        setBox()
        val emptyHeight = fieldBoundsInWindow().height

        setDraft(lines(COMPOSE_MAX_LINES))
        val fiveLineHeight = fieldBoundsInWindow().height
        assertTrue(
            "five-line field $fiveLineHeight should be taller than empty field $emptyHeight",
            fiveLineHeight > emptyHeight,
        )

        setDraft(lines(COMPOSE_MAX_LINES + 1))
        val firstOverCapHeight = fieldBoundsInWindow().height
        setDraft(lines(LONG_DRAFT_LINES))
        val longHeight = fieldBoundsInWindow().height
        assertTrue(
            "$LONG_DRAFT_LINES-line field $longHeight should match " +
                "${COMPOSE_MAX_LINES + 1}-line field $firstOverCapHeight",
            longHeight == firstOverCapHeight,
        )
    }

    @Test
    fun fieldNeverGrowsPastItsMaxHeight() {
        setBox(maxFieldHeight = CAPPED_FIELD_HEIGHT)

        setDraft(lines(LONG_DRAFT_LINES))

        val capPx = composeRule.density.run { CAPPED_FIELD_HEIGHT.roundToPx() }
        val height = fieldBoundsInWindow().height
        assertTrue("field height $height should be at most $capPx", height <= capPx)
    }

    @Test
    fun longDraftScrollsSoTheCursorLineStaysInView() {
        setBox()
        val draft = lines(LONG_DRAFT_LINES)
        composeRule.runOnUiThread {
            editField().apply {
                setText(draft)
                setSelection(draft.length)
            }
        }
        composeRule.waitForIdle()

        val field = editField()
        val textLayout = checkNotNull(field.layout)
        val line = textLayout.getLineForOffset(field.selectionEnd)
        val visible = field.height - field.extendedPaddingTop - field.extendedPaddingBottom
        assertTrue("scrollY ${field.scrollY} should be above 0", field.scrollY > 0)
        assertTrue(
            "cursor line bottom ${textLayout.getLineBottom(line)} should sit within the visible window",
            textLayout.getLineBottom(line) <= field.scrollY + visible,
        )
        assertTrue(
            "cursor line top ${textLayout.getLineTop(line)} should not be scrolled above the field",
            textLayout.getLineTop(line) >= field.scrollY,
        )
    }

    @Test
    fun draftPresenceReportsWhenTextGainsAndLosesIt() {
        val reports = mutableListOf<Boolean>()
        setBox(onDraftPresent = { reports.add(it) })

        setDraft(DRAFT)
        setDraft("")

        assertEquals(listOf(false, true, false), reports)
    }

    @Test
    fun givenSendReceivesWholeDraftOffTheMainThreadAndClearsOnReturn() {
        val sent = CopyOnWriteArrayList<Pair<String, Thread>>()
        setBox(send = { text -> sent.add(text to Thread.currentThread()) })
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil { editField().text.isEmpty() }

        assertEquals(DRAFT, sent.single().first)
        assertNotSame(Looper.getMainLooper().thread, sent.single().second)
        assertEquals(0, server.requests.size)
    }

    @Test
    fun givenSendThatThrowsCallExceptionKeepsDraftAndShowsSendRefused() {
        setBox(send = { throw OrreryCallException(Code.INTERNAL_ERROR, "boom") })
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(SEND_REFUSED_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(DRAFT, editField().text.toString())
        assertEquals(0, server.requests.size)
        composeRule.onNodeWithText("Send").assertIsEnabled()
    }

    @Test
    fun givenSendThatThrowsNoAnswerShowsSendUnknownLabel() {
        setBox(send = { throw NoAnswerException(IOException("no answer in time")) })
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(SEND_UNKNOWN_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(DRAFT, editField().text.toString())
        assertTrue(composeRule.onAllNodesWithText(SEND_REFUSED_LABEL).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun textAppendedDuringASendSurvivesItsSuccess() {
        val release = CountDownLatch(1)
        val sent = CopyOnWriteArrayList<String>()
        setBox(send = { text ->
            sent.add(text)
            release.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)
        })
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.runOnIdle { boxDraft.append(listOf(APPENDED_TEXT)) }
        release.countDown()
        composeRule.awaitUntil { editField().text.toString() == APPENDED_TEXT }

        assertEquals(listOf(DRAFT), sent)
    }

    @Test
    fun appendPutsTextsAfterTheDraftInOrder() {
        setBox()
        setDraft(DRAFT)

        composeRule.runOnIdle { boxDraft.append(listOf(OTHER_DRAFT, APPENDED_TEXT)) }
        composeRule.waitForIdle()

        assertEquals("$DRAFT\n\n$OTHER_DRAFT\n\n$APPENDED_TEXT", editField().text.toString())
    }

    @Test
    fun appendToAnEmptyDraftHasNoLeadingBlankLine() {
        setBox()

        composeRule.runOnIdle { boxDraft.append(listOf(OTHER_DRAFT, APPENDED_TEXT)) }
        composeRule.waitForIdle()

        assertEquals("$OTHER_DRAFT\n\n$APPENDED_TEXT", editField().text.toString())
    }

    @Test
    fun appendOfNoTextsChangesNothing() {
        setBox()
        setDraft(DRAFT)

        composeRule.runOnIdle { boxDraft.append(emptyList()) }
        composeRule.waitForIdle()

        assertEquals(DRAFT, editField().text.toString())
    }

    private fun slowClient(): OrreryClient = OrreryClient(server.baseUrl, composeReadTimeoutMillis = COMPOSE_TIMEOUT_MS)

    private fun assertNoOutcomeLabel() {
        assertTrue(composeRule.onAllNodesWithText(SEND_REFUSED_LABEL).fetchSemanticsNodes().isEmpty())
        assertTrue(composeRule.onAllNodesWithText(SEND_UNKNOWN_LABEL).fetchSemanticsNodes().isEmpty())
    }

    private fun setBox(
        paneId: MutableState<String> = mutableStateOf(PANE_ID),
        hint: String = "",
        status: (@Composable () -> Unit)? = null,
        onDraftPresent: (Boolean) -> Unit = {},
        maxFieldHeight: Dp = Dp.Unspecified,
        client: OrreryClient = OrreryClient(server.baseUrl),
        send: ((String) -> Unit)? = null,
        edge: ComposeEdge = ComposeEdge.IDLE,
    ) {
        composeRule.setContent {
            val sender = send
            val box = rememberDockDraft(paneId.value)
            boxDraft = box
            if (sender == null) {
                DockBox(
                    draft = box,
                    paneId = paneId.value,
                    client = client,
                    edge = edge,
                    maxFieldHeight = maxFieldHeight,
                    hint = hint,
                    onDraftPresent = onDraftPresent,
                    status = status,
                )
            } else {
                DockBox(
                    draft = box,
                    paneId = paneId.value,
                    client = client,
                    edge = edge,
                    maxFieldHeight = maxFieldHeight,
                    hint = hint,
                    onDraftPresent = onDraftPresent,
                    status = status,
                    send = sender,
                )
            }
        }
        composeRule.waitForIdle()
    }

    private fun edgeTagCount(edgeName: String): Int =
        composeRule.onAllNodesWithTag("composer-edge:$edgeName").fetchSemanticsNodes().size

    private fun lines(count: Int): String = (1..count).joinToString("\n") { "line $it" }

    private fun fieldBoundsInWindow(): Rect {
        val field = editField()
        val origin = IntArray(2).also { field.getLocationInWindow(it) }
        return Rect(
            origin[0].toFloat(),
            origin[1].toFloat(),
            (origin[0] + field.width).toFloat(),
            (origin[1] + field.height).toFloat(),
        )
    }

    private fun setDraft(text: String) {
        composeRule.runOnUiThread { editField().setText(text) }
        composeRule.waitForIdle()
    }

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
