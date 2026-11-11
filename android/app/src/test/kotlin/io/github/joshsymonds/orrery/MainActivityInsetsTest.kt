package io.github.joshsymonds.orrery

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.widget.EditText
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.android.controller.ActivityController
import org.robolectric.annotation.Config

private const val TIMEOUT_MS = 5_000L
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/v1/panes"
private const val PANE_ROW_TAG = "pane-row:%1"
private const val PANE_TITLE_TAG = "pane-title"
private const val STATUS_LINE_TAG = "status-line"
private const val COMPOSER_TAG = "composer"
private const val CONVERSATION_PATH = "/v1/panes/%251/conversation"
private const val SEEN_PATH = "/v1/panes/%251/seen"
private const val REPLY_TEXT = "synthetic reply"
private const val CONVERSATION_BODY =
    """{"origin":"synthetic origin","items":[{"kind":"reply","time":0,"text":"$REPLY_TEXT","tool":"","input":"","id":"","error":false}]}"""
private const val ONE_LINE_DRAFT = "synthetic one-line draft"
private const val THREE_LINE_DRAFT = "synthetic line one\nsynthetic line two\nsynthetic line three"
private const val STATUS_BAR_PX = 60
private const val NAVIGATION_BAR_PX = 90
private const val CUTOUT_PX = 20
private const val IME_PX = 400

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityInsetsTest {
    @get:Rule
    val composeRule = createEmptyComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        server.respond("GET", PANES_PATH, 200, panesJson("%1" to "alpha-build"))
    }

    @After
    fun closeServer() {
        server.close()
    }

    private fun panesJson(vararg panes: Pair<String, String>): String = JSONArray(
        panes.map { (id, label) ->
            JSONObject()
                .put("id", id)
                .put("host", "example-host")
                .put("session", "example-session")
                .put("window", 1)
                .put("pane", 0)
                .put("cwd", "/home/user/alpha")
                .put("command", "claude")
                .put("state", "working")
                .put("summary", "Synthetic summary.")
                .put("label", label)
                .put("changed", NOW - 30)
                .put("seen", NOW - 10)
                .put("question", "")
                .put("question_hash", "")
        },
    ).toString()

    private fun syntheticInsets(keyboardShown: Boolean): WindowInsetsCompat {
        val builder = WindowInsetsCompat.Builder()
            .setInsets(WindowInsetsCompat.Type.statusBars(), Insets.of(0, STATUS_BAR_PX, 0, 0))
            .setInsets(WindowInsetsCompat.Type.navigationBars(), Insets.of(0, 0, 0, NAVIGATION_BAR_PX))
            .setInsets(WindowInsetsCompat.Type.displayCutout(), Insets.of(CUTOUT_PX, 0, 0, 0))
        if (keyboardShown) {
            builder
                .setInsets(WindowInsetsCompat.Type.ime(), Insets.of(0, 0, 0, IME_PX))
                .setVisible(WindowInsetsCompat.Type.ime(), true)
        }
        return builder.build()
    }

    /** Launches the activity and dispatches the synthetic insets, returning once the pane list shows. */
    private fun launchWithInsets(keyboardShown: Boolean) {
        val controller = Robolectric.buildActivity(
            MainActivity::class.java,
            Intent(context, MainActivity::class.java),
        ).setup()
        composeRule.waitUntil(TIMEOUT_MS) {
            composeRule.onAllNodes(hasTestTag("pane-row:%1"), useUnmergedTree = true)
                .fetchSemanticsNodes().isNotEmpty()
        }
        val contentView = controller.get().findViewById<View>(android.R.id.content)

        ViewCompat.dispatchApplyWindowInsets(contentView, syntheticInsets(keyboardShown))
        composeRule.waitForIdle()
    }

    /** Launches the activity, dispatches the synthetic insets and returns the root and content bounds. */
    private fun boundsWithInsets(keyboardShown: Boolean): Pair<Rect, Rect> {
        launchWithInsets(keyboardShown)
        val root = composeRule.onRoot(useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val content = composeRule.onNodeWithTag(ROOT_CONTENT_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInRoot
        return root to content
    }

    /** Launches the activity and opens the Claude pane %1, returning once its conversation shows. */
    private fun openClaudePane(): ActivityController<MainActivity> {
        server.respond("GET", CONVERSATION_PATH, 200, CONVERSATION_BODY)
        server.respond("POST", SEEN_PATH, 200)
        val controller = Robolectric.buildActivity(
            MainActivity::class.java,
            Intent(context, MainActivity::class.java),
        ).setup()
        composeRule.waitUntil(TIMEOUT_MS) {
            composeRule.onAllNodes(hasTestTag(PANE_ROW_TAG), useUnmergedTree = true)
                .fetchSemanticsNodes().isNotEmpty()
        }
        composeRule.onNodeWithTag(PANE_ROW_TAG, useUnmergedTree = true).performClick()
        composeRule.waitUntil(TIMEOUT_MS) {
            composeRule.onAllNodes(hasText(REPLY_TEXT), useUnmergedTree = true)
                .fetchSemanticsNodes().isNotEmpty()
        }
        return controller
    }

    private fun dispatchInsets(controller: ActivityController<MainActivity>, keyboardShown: Boolean) {
        val contentView = controller.get().findViewById<View>(android.R.id.content)
        ViewCompat.dispatchApplyWindowInsets(contentView, syntheticInsets(keyboardShown))
        composeRule.waitForIdle()
    }

    private fun setDraft(controller: ActivityController<MainActivity>, text: String) {
        composeRule.runOnUiThread { editField(controller).setText(text) }
        composeRule.waitForIdle()
    }

    private fun editField(controller: ActivityController<MainActivity>): EditText =
        checkNotNull(findEditText(controller.get().window.decorView)) {
            "ComposeBox must host an EditText"
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

    /** The status line sits inside the composer's bounds and its bottom is at or above the draft field's top. */
    private fun assertStatusAboveDraft(controller: ActivityController<MainActivity>) {
        composeRule.onNodeWithTag(STATUS_LINE_TAG, useUnmergedTree = true).assertIsDisplayed()
        val status = composeRule.onNodeWithTag(STATUS_LINE_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInWindow
        val composer = composeRule.onNodeWithTag(COMPOSER_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInWindow
        val fieldOrigin = IntArray(2).also { editField(controller).getLocationInWindow(it) }
        assertTrue(
            "status $status should be inside composer $composer",
            status.top >= composer.top && status.bottom <= composer.bottom,
        )
        assertTrue(
            "status bottom ${status.bottom} should be at or above field top ${fieldOrigin[1]}",
            status.bottom <= fieldOrigin[1],
        )
    }

    @Test
    fun keyboardUpFoldsHeaderAndKeepsStatusAboveDraft() {
        val controller = openClaudePane()

        dispatchInsets(controller, keyboardShown = false)
        composeRule.onNodeWithTag(PANE_TITLE_TAG, useUnmergedTree = true).assertIsDisplayed()

        dispatchInsets(controller, keyboardShown = true)
        composeRule.onAllNodes(hasTestTag(PANE_TITLE_TAG), useUnmergedTree = true).assertCountEquals(0)
        assertStatusAboveDraft(controller)
    }

    @Test
    fun statusLineStaysAboveDraftAsItGrows() {
        val controller = openClaudePane()
        dispatchInsets(controller, keyboardShown = true)

        setDraft(controller, ONE_LINE_DRAFT)
        assertStatusAboveDraft(controller)

        setDraft(controller, THREE_LINE_DRAFT)
        assertStatusAboveDraft(controller)
    }

    @Test
    fun rootContentIsInsetByBarsCutoutAndKeyboard() {
        val (root, content) = boundsWithInsets(keyboardShown = true)

        assertEquals(CUTOUT_PX.toFloat(), content.left, 0f)
        assertEquals(STATUS_BAR_PX.toFloat(), content.top, 0f)
        assertEquals(root.right, content.right, 0f)
        assertEquals(root.bottom - IME_PX, content.bottom, 0f)
    }

    @Test
    fun rootContentClearsTheNavigationBarWithoutKeyboard() {
        val (root, content) = boundsWithInsets(keyboardShown = false)

        assertEquals(CUTOUT_PX.toFloat(), content.left, 0f)
        assertEquals(STATUS_BAR_PX.toFloat(), content.top, 0f)
        assertEquals(root.right, content.right, 0f)
        assertEquals(root.bottom - NAVIGATION_BAR_PX, content.bottom, 0f)
    }

    @Test
    fun topBandCoversTheStatusBarInsetAcrossTheFullWidth() {
        launchWithInsets(keyboardShown = false)
        val root = composeRule.onRoot(useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val band = composeRule.onNodeWithTag(TOP_BAND_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInRoot

        assertEquals(0f, band.left, 0f)
        assertEquals(0f, band.top, 0f)
        assertEquals(root.right, band.right, 0f)
        assertEquals(STATUS_BAR_PX.toFloat(), band.bottom, 0f)
    }

    @Test
    fun topBandIsDrawnAfterRootContent() {
        launchWithInsets(keyboardShown = false)
        val band = composeRule.onNodeWithTag(TOP_BAND_TAG, useUnmergedTree = true).fetchSemanticsNode()
        val siblings = checkNotNull(band.parent) { "top band must have a parent in the semantics tree" }.children
        val bandIndex = siblings.indexOfFirst { it.id == band.id }
        val contentIndex = siblings.indexOfFirst { hasTestTag(ROOT_CONTENT_TAG).matches(it) }

        assertTrue(
            "root content index $contentIndex should precede top band index $bandIndex",
            contentIndex >= 0 && contentIndex < bandIndex,
        )
    }

    @Test
    fun activityResizesForTheKeyboard() {
        val info = context.packageManager.getActivityInfo(
            ComponentName(context, MainActivity::class.java),
            0,
        )

        assertEquals(
            WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE,
            info.softInputMode and WindowManager.LayoutParams.SOFT_INPUT_MASK_ADJUST,
        )
    }
}
