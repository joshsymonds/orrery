package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val TIMEOUT_MS = 5_000L
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/v1/panes"
private const val FIRST_SCREEN_PATH = "/v1/panes/%251/screen"
private const val SECOND_SCREEN_PATH = "/v1/panes/%252/screen"
private const val FIRST_SEEN_PATH = "/v1/panes/%251/seen"
private const val SECOND_SEEN_PATH = "/v1/panes/%252/seen"
private const val MOCHA_BASE_ARGB = 0xFF1E1E2E.toInt()

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class MainActivityThemeTest {
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
        server.respond("GET", PANES_PATH, 200, panesJson("%1" to "alpha-build", "%2" to "alpha-review"))
        server.respond("GET", FIRST_SCREEN_PATH, 200, "synthetic first body")
        server.respond("GET", SECOND_SCREEN_PATH, 200, "synthetic second body")
        server.respond("POST", FIRST_SEEN_PATH, 200)
        server.respond("POST", SECOND_SEEN_PATH, 200)
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

    @Test
    fun activityThemeIsDark() {
        val intent = Intent(context, MainActivity::class.java)
        val activity = Robolectric.buildActivity(MainActivity::class.java, intent).setup().get()

        val attributes = activity.theme.obtainStyledAttributes(intArrayOf(android.R.attr.isLightTheme))
        try {
            assertFalse("activity theme must be dark", attributes.getBoolean(0, true))
        } finally {
            attributes.recycle()
        }
    }

    @Test
    fun composedBackgroundIsMochaBase() {
        val intent = Intent(context, MainActivity::class.java)
        val activity = Robolectric.buildActivity(MainActivity::class.java, intent).setup().visible().get()
        composeRule.waitUntil(TIMEOUT_MS) {
            composeRule.onAllNodes(hasTestTag("pane-row:%1")).fetchSemanticsNodes().isNotEmpty()
        }

        val decor = activity.window.decorView
        val bitmap = Bitmap.createBitmap(decor.width, decor.height, Bitmap.Config.ARGB_8888)
        decor.draw(Canvas(bitmap))

        assertEquals(MOCHA_BASE_ARGB, bitmap.getPixel(bitmap.width - 5, bitmap.height - 5))
    }
}
