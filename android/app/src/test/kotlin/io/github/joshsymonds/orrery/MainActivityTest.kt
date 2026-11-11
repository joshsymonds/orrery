package io.github.joshsymonds.orrery

import android.Manifest
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.ActivityInfo
import android.content.pm.PackageManager
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.push.OrreryPushService
import io.github.joshsymonds.orrery.push.PushNotifier
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController

private const val TIMEOUT_MS = 5_000L
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/v1/panes"
private const val FIRST_SCREEN_PATH = "/v1/panes/%251/screen"
private const val SECOND_SCREEN_PATH = "/v1/panes/%252/screen"
private const val FIRST_SEEN_PATH = "/v1/panes/%251/seen"
private const val SECOND_SEEN_PATH = "/v1/panes/%252/seen"
private const val PUSH_EVENT_ACTION = "org.unifiedpush.android.connector.PUSH_EVENT"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityTest {
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

    private fun paneJson(
        id: String,
        label: String,
        cwd: String = "/home/user/alpha",
        state: String = "working",
    ): JSONObject = JSONObject()
        .put("id", id)
        .put("host", "example-host")
        .put("session", "example-session")
        .put("window", 1)
        .put("pane", 0)
        .put("cwd", cwd)
        .put("command", "claude")
        .put("state", state)
        .put("summary", "Synthetic summary.")
        .put("label", label)
        .put("changed", NOW - 30)
        .put("seen", NOW - 10)
        .put("question", "")
        .put("question_hash", "")

    private fun panesJson(vararg panes: Pair<String, String>): String =
        JSONArray(panes.map { (id, label) -> paneJson(id, label) }).toString()

    private fun launch(extraPane: String? = null): ActivityController<MainActivity> {
        val intent = Intent(context, MainActivity::class.java)
        if (extraPane != null) {
            intent.putExtra(PushNotifier.EXTRA_PANE, extraPane)
        }
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup()
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.waitUntil(TIMEOUT_MS) {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun assertAbsent(matcher: SemanticsMatcher) {
        assertTrue(
            "expected no node matching $matcher",
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty(),
        )
    }

    @Test
    fun launchListsTheFakePanes() {
        launch()

        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithText("alpha-build").assertExists()
        composeRule.onNodeWithText("alpha-review").assertExists()
    }

    @Test
    fun tappingARowShowsThatPanesScreen() {
        launch()
        awaitNode(hasTestTag("pane-row:%1"))

        composeRule.onNodeWithTag("pane-row:%1").performClick()

        awaitNode(hasText("synthetic first body"))
        assertAbsent(hasTestTag("pane-row:%2"))
    }

    @Test
    fun backReturnsFromAPaneToTheList() {
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("pane-row:%1").performClick()
        awaitNode(hasText("synthetic first body"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%2"))
        assertAbsent(hasText("synthetic first body"))
    }

    @Test
    fun backArrowReturnsFromAPaneToTheList() {
        launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("pane-row:%1").performClick()
        awaitNode(hasTestTag("pane-back"))

        composeRule.onNodeWithTag("pane-back").performClick()

        awaitNode(hasTestTag("pane-row:%2"))
        assertAbsent(hasText("synthetic first body"))
    }

    @Test
    fun theChosenViewSurvivesOpeningAPane() {
        server.respond(
            "GET",
            PANES_PATH,
            200,
            JSONArray(
                listOf(paneJson("%1", "alpha-build", state = "waiting"), paneJson("%2", "alpha-review")),
            ).toString(),
        )
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("filter-waiting").performClick()
        assertAbsent(hasTestTag("pane-row:%2"))
        composeRule.onNodeWithTag("pane-row:%1").performClick()
        awaitNode(hasText("synthetic first body"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%1"))
        assertAbsent(hasTestTag("pane-row:%2"))
    }

    @Test
    fun collapsedFoldersSurviveOpeningAPane() {
        server.respond(
            "GET",
            PANES_PATH,
            200,
            JSONArray(
                listOf(paneJson("%1", "alpha-build"), paneJson("%2", "beta-deploy", cwd = "/home/user/beta")),
            ).toString(),
        )
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("folder:/home/user/alpha").performClick()
        assertAbsent(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("pane-row:%2").performClick()
        awaitNode(hasText("synthetic second body"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%2"))
        assertAbsent(hasTestTag("pane-row:%1"))
    }

    @Test
    fun launchWithPaneExtraOpensThatPane() {
        launch(extraPane = "%2")

        awaitNode(hasText("synthetic second body"))
    }

    @Test
    fun newIntentWithAnotherPaneSwitchesToIt() {
        val controller = launch(extraPane = "%1")
        awaitNode(hasText("synthetic first body"))

        val next = Intent(context, MainActivity::class.java).putExtra(PushNotifier.EXTRA_PANE, "%2")
        controller.newIntent(next)

        awaitNode(hasText("synthetic second body"))
        assertAbsent(hasText("synthetic first body"))
    }

    @Test
    fun settingsBackReturnsToTheListWithoutSaving() {
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithText("Settings").performClick()
        awaitNode(hasTestTag("server-address"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%1"))
    }

    @Test
    fun settingsSaveReloadsTheListFromTheNewAddress() {
        val second = FakeOrreryServer()
        try {
            second.respond("GET", PANES_PATH, 200, panesJson("%9" to "beta-deploy"))
            launch()
            awaitNode(hasTestTag("pane-row:%1"))

            composeRule.onNodeWithText("Settings").performClick()
            awaitNode(hasTestTag("server-address"))
            composeRule.onNodeWithTag("server-address").performTextReplacement(second.baseUrl)
            composeRule.onNodeWithText("Save").performClick()

            awaitNode(hasTestTag("pane-row:%9"))
            assertAbsent(hasTestTag("pane-row:%1"))
            assertEquals(1, second.requests.count { it.rawPath == PANES_PATH })
            assertEquals(ServerSettings(context).serverUrl, second.baseUrl)
        } finally {
            second.close()
        }
    }

    @Test
    fun launchRequestsPostNotifications() {
        val controller = launch()

        val request = shadowOf(controller.get()).lastRequestedPermission
        assertNotNull("expected a permission request at launch", request)
        assertEquals(listOf(Manifest.permission.POST_NOTIFICATIONS), request.requestedPermissions.toList())
    }

    @Test
    fun pushEventResolvesToTheNonExportedPushService() {
        val packageManager = context.packageManager
        val intent = Intent(PUSH_EVENT_ACTION).setPackage(context.packageName)

        val services = packageManager.queryIntentServices(intent, 0)

        assertEquals(1, services.size)
        val service = services.single().serviceInfo
        assertEquals(OrreryPushService::class.java.name, service.name)
        assertFalse(service.exported)
    }

    @Test
    fun mainActivityIsSingleTop() {
        val info = context.packageManager.getActivityInfo(
            ComponentName(context, MainActivity::class.java),
            PackageManager.GET_META_DATA,
        )

        assertEquals(ActivityInfo.LAUNCH_SINGLE_TOP, info.launchMode)
    }
}
