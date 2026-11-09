package io.github.joshsymonds.orrery

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.view.View
import android.view.WindowManager
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val TIMEOUT_MS = 5_000L
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/v1/panes"
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

    /** Launches the activity, dispatches the synthetic insets and returns the root and content bounds. */
    private fun boundsWithInsets(keyboardShown: Boolean): Pair<Rect, Rect> {
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

        val root = composeRule.onRoot(useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        val content = composeRule.onNodeWithTag(ROOT_CONTENT_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInRoot
        return root to content
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
