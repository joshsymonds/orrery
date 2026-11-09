package io.github.joshsymonds.orrery.ui

import android.text.InputType
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.core.content.res.ResourcesCompat
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
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

    private fun setBox(paneId: MutableState<String> = mutableStateOf(PANE_ID)) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent { ComposeBox(paneId = paneId.value, client = client) }
        composeRule.waitForIdle()
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
