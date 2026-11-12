package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.core.view.WindowCompat
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
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
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
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
        server.respond(PANES_PATH, panesProto("%1" to "alpha-build", "%2" to "alpha-review"))
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    @After
    fun closeServer() {
        server.close()
    }

    private fun panesProto(vararg panes: Pair<String, String>): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(
            panes.map { (id, label) ->
                PaneProto.newBuilder()
                    .setId(id)
                    .setHost("example-host")
                    .setSession("example-session")
                    .setWindow(1)
                    .setPane(0)
                    .setCwd("/home/user/alpha")
                    .setCommand("claude")
                    .setState("working")
                    .setSummary("Synthetic summary.")
                    .setLabel(label)
                    .setChanged(NOW - 30)
                    .setSeen(NOW - 10)
                    .setQuestion("")
                    .setQuestionHash("")
                    .build()
            },
        ).build()

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
    @Config(sdk = [35], qualifiers = "w400dp-h1200dp-notnight")
    fun systemBarIconsStayLightInLightMode() {
        assertSystemBarIconsAreLight()
    }

    @Test
    @Config(sdk = [35], qualifiers = "w400dp-h1200dp-night")
    fun systemBarIconsStayLightInNightMode() {
        assertSystemBarIconsAreLight()
    }

    private fun assertSystemBarIconsAreLight() {
        val intent = Intent(context, MainActivity::class.java)
        val activity = Robolectric.buildActivity(MainActivity::class.java, intent).setup().get()

        val controller = WindowCompat.getInsetsController(activity.window, activity.window.decorView)
        assertFalse("status bar icons must stay light", controller.isAppearanceLightStatusBars)
        assertFalse("navigation bar icons must stay light", controller.isAppearanceLightNavigationBars)
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
