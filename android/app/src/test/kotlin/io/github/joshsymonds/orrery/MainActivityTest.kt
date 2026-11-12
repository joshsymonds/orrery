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
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.push.OrreryPushService
import io.github.joshsymonds.orrery.push.PushNotifier
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
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
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
        server.respond(PANES_PATH, panesProto("%1" to "alpha-build", "%2" to "alpha-review"))
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
    }

    @After
    fun closeServer() {
        server.close()
    }

    private fun paneProto(
        id: String,
        label: String,
        cwd: String = "/home/user/alpha",
        state: String = "working",
    ): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState(state)
        .setSummary("Synthetic summary.")
        .setLabel(label)
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .setQuestion("")
        .setQuestionHash("")
        .build()

    private fun panesOf(vararg panes: PaneProto): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(panes.toList()).build()

    private fun panesProto(vararg panes: Pair<String, String>): PanesListResponse =
        panesOf(*panes.map { (id, label) -> paneProto(id, label) }.toTypedArray())

    /** The header of the open pane, by its label. */
    private fun titled(label: String): SemanticsMatcher = hasTestTag("pane-title").and(hasText(label))

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

        awaitNode(titled("alpha-build"))
        assertAbsent(hasTestTag("pane-row:%2"))
    }

    @Test
    fun backReturnsFromAPaneToTheList() {
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("pane-row:%1").performClick()
        awaitNode(titled("alpha-build"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%2"))
        assertAbsent(hasTestTag("pane-title"))
    }

    @Test
    fun backArrowReturnsFromAPaneToTheList() {
        launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("pane-row:%1").performClick()
        awaitNode(hasTestTag("pane-back"))

        composeRule.onNodeWithTag("pane-back").performClick()

        awaitNode(hasTestTag("pane-row:%2"))
        assertAbsent(hasTestTag("pane-title"))
    }

    @Test
    fun theChosenViewSurvivesOpeningAPane() {
        server.respond(
            PANES_PATH,
            panesOf(paneProto("%1", "alpha-build", state = "waiting"), paneProto("%2", "alpha-review")),
        )
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("filter-waiting").performClick()
        assertAbsent(hasTestTag("pane-row:%2"))
        composeRule.onNodeWithTag("pane-row:%1").performClick()
        awaitNode(titled("alpha-build"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%1"))
        assertAbsent(hasTestTag("pane-row:%2"))
    }

    @Test
    fun collapsedFoldersSurviveOpeningAPane() {
        server.respond(
            PANES_PATH,
            panesOf(paneProto("%1", "alpha-build"), paneProto("%2", "beta-deploy", cwd = "/home/user/beta")),
        )
        val controller = launch()
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("folder:/home/user/alpha").performClick()
        assertAbsent(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("pane-row:%2").performClick()
        awaitNode(titled("beta-deploy"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%2"))
        assertAbsent(hasTestTag("pane-row:%1"))
    }

    @Test
    fun launchWithPaneExtraOpensThatPane() {
        launch(extraPane = "%2")

        awaitNode(titled("alpha-review"))
    }

    @Test
    fun newIntentWithAnotherPaneSwitchesToIt() {
        val controller = launch(extraPane = "%1")
        awaitNode(titled("alpha-build"))

        val next = Intent(context, MainActivity::class.java).putExtra(PushNotifier.EXTRA_PANE, "%2")
        controller.newIntent(next)

        awaitNode(titled("alpha-review"))
        assertAbsent(titled("alpha-build"))
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
            second.respond(PANES_PATH, panesProto("%9" to "beta-deploy"))
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
