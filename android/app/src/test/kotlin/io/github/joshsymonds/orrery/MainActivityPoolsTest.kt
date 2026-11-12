package io.github.joshsymonds.orrery

import android.content.Context
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolMemberStatus
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolStatus
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsStatusResponse
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController

private const val TIMEOUT_MS = 5_000L
private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val POOLS_STATUS_PATH = "/orrery.v1.Pools/Status"
private const val POOL = "example-pool"
private const val MEMBER = "example-member-1"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityPoolsTest {
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
        server.respond(
            PANES_PATH,
            PanesListResponse.newBuilder().addPanes(paneProto("%1", "alpha-build")).build(),
        )
        server.respond(
            POOLS_STATUS_PATH,
            PoolsStatusResponse.newBuilder()
                .addPools(
                    PoolStatus.newBuilder()
                        .setName(POOL)
                        .setCap(1)
                        .addMembers(PoolMemberStatus.newBuilder().setName(MEMBER).setState("awake")),
                )
                .build(),
        )
    }

    @After
    fun closeServer() {
        server.close()
    }

    private fun paneProto(id: String, label: String): PaneProto = PaneProto.newBuilder()
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

    private fun launch(): ActivityController<MainActivity> =
        Robolectric.buildActivity(MainActivity::class.java).setup()

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

    private fun openPools() {
        awaitNode(hasTestTag("pane-row:%1"))
        composeRule.onNodeWithTag("open-pools").performClick()
        awaitNode(hasTestTag("pool:$POOL"))
    }

    @Test
    fun tappingPoolsShowsThePoolsScreen() {
        launch()

        openPools()

        assertEquals(1, server.requests.count { it.rawPath == POOLS_STATUS_PATH })
        assertAbsent(hasTestTag("pane-row:%1"))
    }

    @Test
    fun systemBackReturnsFromPoolsToTheList() {
        val controller = launch()
        openPools()

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("pane-row:%1"))
        assertAbsent(hasTestTag("pool:$POOL"))
    }

    @Test
    fun poolsBackButtonReturnsToTheList() {
        launch()
        openPools()

        composeRule.onNodeWithText("Back").performClick()

        awaitNode(hasTestTag("pane-row:%1"))
        assertAbsent(hasTestTag("pool:$POOL"))
    }
}
