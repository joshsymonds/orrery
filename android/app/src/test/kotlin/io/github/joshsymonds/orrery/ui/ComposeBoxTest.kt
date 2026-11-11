package io.github.joshsymonds.orrery.ui

import android.text.InputType
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.content.res.ResourcesCompat
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import java.net.InetAddress
import java.net.ServerSocket
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val TEXT_PATH = "/v1/panes/%253/text"
private const val OTHER_PANE_ID = "%2"
private const val OTHER_PANE_TEXT_PATH = "/v1/panes/%252/text"
private const val WAIT_MS = 5_000L
private const val DRAFT = "first line\nsecond line"
private const val OTHER_DRAFT = "text for the other pane"
private const val HINT = "synthetic hint"
private const val STATUS = "synthetic status line"
private const val COMPOSER_TAG = "composer"
private const val SEND_ALIGNMENT_TOLERANCE_PX = 1f
private const val LONG_DRAFT_LINES = 12
private const val NOT_SENT_LABEL = "not sent"
private const val NO_ANSWER_LABEL = "no answer — check before resending"
private const val COMPOSE_TIMEOUT_MS = 200
private const val SLOW_ANSWER_MS = 2_000L
private val CAPPED_FIELD_HEIGHT = 60.dp

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ComposeBoxTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
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
        server.respond("POST", TEXT_PATH, 204)
        setBox()
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) { editField().text.isEmpty() }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(TEXT_PATH, request.rawPath)
        assertEquals(DRAFT, JSONObject(request.body).getString("text"))
    }

    @Test
    fun failedSendKeepsDraftAndShowsNotSent() {
        server.respond("POST", TEXT_PATH, 500)
        setBox()
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText("not sent").fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(DRAFT, editField().text.toString())
        composeRule.onNodeWithText("Send").assertIsEnabled()
    }

    @Test
    fun answeredSendShowsNeitherLabel() {
        server.respond("POST", TEXT_PATH, 204)
        setBox()
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) { editField().text.isEmpty() }
        composeRule.waitForIdle()

        assertNoOutcomeLabel()
    }

    @Test
    fun refusedConnectionKeepsDraftAndShowsNotSent() {
        val closedPort = ServerSocket(0, 0, InetAddress.getByName("127.0.0.1")).use { it.localPort }
        setBox(client = OrreryClient("http://127.0.0.1:$closedPort"))
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(NOT_SENT_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(DRAFT, editField().text.toString())
        assertTrue(composeRule.onAllNodesWithText(NO_ANSWER_LABEL).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun slowSendKeepsDraftAndShowsNoAnswerNotNotSent() {
        server.respond("POST", TEXT_PATH, 204, delayMillis = SLOW_ANSWER_MS)
        setBox(client = slowClient())
        setDraft(DRAFT)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(NO_ANSWER_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(DRAFT, editField().text.toString())
        assertTrue(composeRule.onAllNodesWithText(NOT_SENT_LABEL).fetchSemanticsNodes().isEmpty())
        composeRule.onNodeWithText("Send").assertIsEnabled()
    }

    @Test
    fun nextSendClearsNoAnswerLabel() {
        server.respond("POST", TEXT_PATH, 204, delayMillis = SLOW_ANSWER_MS)
        setBox(client = slowClient())
        setDraft(DRAFT)
        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(NO_ANSWER_LABEL).fetchSemanticsNodes().isNotEmpty()
        }

        // FakeOrreryServer answers one request at a time, so the second request would queue behind the
        // slow answer and miss the compose timeout. Let that answer finish first.
        Thread.sleep(SLOW_ANSWER_MS)
        server.respond("POST", TEXT_PATH, 204)
        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) { editField().text.isEmpty() }
        composeRule.waitForIdle()

        assertEquals(2, server.requests.size)
        assertNoOutcomeLabel()
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
        server.respond("POST", OTHER_PANE_TEXT_PATH, 204)
        val paneId = mutableStateOf(PANE_ID)
        setBox(paneId)
        setDraft(DRAFT)

        composeRule.runOnIdle { paneId.value = OTHER_PANE_ID }
        composeRule.waitForIdle()

        assertEquals("", editField().text.toString())

        setDraft(OTHER_DRAFT)
        composeRule.onNodeWithText("Send").performClick()
        composeRule.waitUntil(WAIT_MS) { editField().text.isEmpty() }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals(OTHER_PANE_TEXT_PATH, request.rawPath)
        assertEquals(OTHER_DRAFT, JSONObject(request.body).getString("text"))
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

    private fun slowClient(): OrreryClient = OrreryClient(server.baseUrl, composeReadTimeoutMillis = COMPOSE_TIMEOUT_MS)

    private fun assertNoOutcomeLabel() {
        assertTrue(composeRule.onAllNodesWithText(NOT_SENT_LABEL).fetchSemanticsNodes().isEmpty())
        assertTrue(composeRule.onAllNodesWithText(NO_ANSWER_LABEL).fetchSemanticsNodes().isEmpty())
    }

    private fun setBox(
        paneId: MutableState<String> = mutableStateOf(PANE_ID),
        hint: String = "",
        status: (@Composable () -> Unit)? = null,
        onDraftPresent: (Boolean) -> Unit = {},
        maxFieldHeight: Dp = Dp.Unspecified,
        client: OrreryClient = OrreryClient(server.baseUrl),
    ) {
        composeRule.setContent {
            ComposeBox(
                paneId = paneId.value,
                client = client,
                maxFieldHeight = maxFieldHeight,
                hint = hint,
                onDraftPresent = onDraftPresent,
                status = status,
            )
        }
        composeRule.waitForIdle()
    }

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
            "ComposeBox must host an EditText"
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
