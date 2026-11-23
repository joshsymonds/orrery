package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.android.controller.ActivityController

private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
private const val PROJECT_KEY = "example-host|/home/user/alpha"
private const val PROJECT_DIR = "/home/user/alpha"
private const val CONVO_KEY = "pane:example-host:%1"
private const val CONVO_LABEL = "alpha-build"
private const val CONVO_ORRERY_ID = "synthetic-orrery-id"
private const val EPIC_ID = "example-repo/example-epic"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityProjectPageTest {
    @get:Rule
    val composeRule = mainThreadEmptyComposeRule()

    private lateinit var context: Context
    private lateinit var server: FakeOrreryServer

    /** Every activity a test launched, destroyed after it so no screen keeps composing into the next test. */
    private val launched = mutableListOf<ActivityController<MainActivity>>()

    @Before
    fun pointSettingsAtFakeServer() {
        context = ApplicationProvider.getApplicationContext()
        context.getSharedPreferences("orrery-settings", Context.MODE_PRIVATE).edit().clear().commit()
        server = FakeOrreryServer()
        ServerSettings(context).saveServerUrl(server.baseUrl)
        server.respond(PANES_PATH, panesOf(paneProto("%1", CONVO_LABEL), paneProto("%2", "alpha-review")))
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        Settings.Global.putFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f)
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
        server.close()
    }

    private fun paneProto(id: String, label: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost("example-host")
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(PROJECT_DIR)
        .setCommand("claude")
        .setClaudeSession("synthetic-claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel(label)
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .setQuestion("")
        .setQuestionHash("")
        .build()

    private fun panesOf(vararg panes: PaneProto): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(panes.toList()).build()

    private fun launch(): ActivityController<MainActivity> {
        val intent = Intent(context, MainActivity::class.java)
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup().also { launched += it }
    }

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        }
    }

    private fun awaitNone(matcher: SemanticsMatcher) {
        composeRule.awaitUntil {
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty()
        }
    }

    private fun assertAbsent(matcher: SemanticsMatcher) {
        assertTrue(
            "expected no node matching $matcher",
            composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().isEmpty(),
        )
    }

    private fun titled(label: String): SemanticsMatcher = hasTestTag("head-text").and(hasText(label))

    /** Launches the app and opens the project page from its name in the home tree. */
    private fun openProjectFromHome(): ActivityController<MainActivity> {
        val controller = launch()
        awaitNode(hasTestTag("project-open:$PROJECT_KEY"))
        composeRule.onNodeWithTag("project-open:$PROJECT_KEY").performClick()
        awaitNode(hasTestTag("project-page"))
        return controller
    }

    @Test
    fun tappingAProjectNameOpensItsPageAndTheTreeIsGone() {
        launch()
        awaitNode(hasTestTag("project-open:$PROJECT_KEY"))

        composeRule.onNodeWithTag("project-open:$PROJECT_KEY").performClick()

        awaitNode(hasTestTag("project-page"))
        awaitNone(hasTestTag("project-open:$PROJECT_KEY"))
    }

    @Test
    fun theTitleRowsBackReturnsToTheTree() {
        openProjectFromHome()
        awaitNode(hasTestTag("title-back"))

        composeRule.onNodeWithTag("title-back").performClick()

        awaitNode(hasTestTag("project-open:$PROJECT_KEY"))
        awaitNone(hasTestTag("project-page"))
    }

    @Test
    fun theSystemBackReturnsToTheTree() {
        val controller = openProjectFromHome()

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("project-open:$PROJECT_KEY"))
        awaitNone(hasTestTag("project-page"))
    }

    /** Steers the conversation %1 with an epic, so the project page hangs that epic under it. */
    private fun serveSteeredEpic() {
        server.respond(
            PANES_PATH,
            panesOf(PaneProto.newBuilder(paneProto("%1", CONVO_LABEL)).setOrreryId(CONVO_ORRERY_ID).build()),
        )
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome("")
            .setChanged(NOW - 30)
            .setLive(true)
            .setSource("orreryd")
            .setSteering(CONVO_ORRERY_ID)
            .setEndedSteps(0)
            .setProblem("")
            .build()
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(EPICS_GET_PATH, EpicsGetResponse.newBuilder().setEpic(epic).build())
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
    }

    @Test
    fun conversationFromProjectPageSystemBackReturnsToIt() {
        val controller = openProjectFromHome()
        awaitNode(hasTestTag("project-convo:$CONVO_KEY"))
        composeRule.onNodeWithTag("project-convo:$CONVO_KEY").performClick()
        awaitNode(titled(CONVO_LABEL))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("project-page"))
        awaitNone(titled(CONVO_LABEL))
    }

    @Test
    fun conversationFromProjectPageTitleBackReturnsToIt() {
        openProjectFromHome()
        awaitNode(hasTestTag("project-convo:$CONVO_KEY"))
        composeRule.onNodeWithTag("project-convo:$CONVO_KEY").performClick()
        awaitNode(hasTestTag("head-back"))

        composeRule.onNodeWithTag("head-back").performClick()

        awaitNode(hasTestTag("project-page"))
        awaitNone(titled(CONVO_LABEL))
    }

    @Test
    fun epicFromProjectPageSystemBackReturnsToIt() {
        serveSteeredEpic()
        val controller = openProjectFromHome()
        awaitNode(hasTestTag("card-open:epic:$EPIC_ID"))
        composeRule.onNodeWithTag("card-open:epic:$EPIC_ID").performClick()
        awaitNone(hasTestTag("project-page"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("project-page"))
    }

    @Test
    fun epicFromProjectPageTitleBackReturnsToIt() {
        serveSteeredEpic()
        openProjectFromHome()
        awaitNode(hasTestTag("card-open:epic:$EPIC_ID"))
        composeRule.onNodeWithTag("card-open:epic:$EPIC_ID").performClick()
        awaitNone(hasTestTag("project-page"))
        awaitNode(hasTestTag("title-back"))

        composeRule.onNodeWithTag("title-back").performClick()

        awaitNode(hasTestTag("project-page"))
    }

    @Test
    fun conversationFromHomeTreeSystemBackReturnsToTheTree() {
        val controller = launch()
        awaitNode(hasTestTag("convo:$CONVO_KEY"))
        composeRule.onNodeWithTag("convo:$CONVO_KEY").performClick()
        awaitNode(titled(CONVO_LABEL))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag("project-open:$PROJECT_KEY"))
        awaitNone(titled(CONVO_LABEL))
        assertAbsent(hasTestTag("project-page"))
    }

    @Test
    fun newSessionHereOpensTheStartFormInTheProjectsDirectory() {
        openProjectFromHome()
        awaitNode(hasTestTag("project-new-here"))

        composeRule.onNodeWithTag("project-new-here").performClick()

        awaitNode(hasTestTag("start-form"))
        composeRule.onNodeWithTag("start-dir").assertTextEquals(PROJECT_DIR)
    }

    @Test
    fun tappingAConversationOfThePageOpensItsScreen() {
        openProjectFromHome()
        awaitNode(hasTestTag("project-convo:$CONVO_KEY"))

        composeRule.onNodeWithTag("project-convo:$CONVO_KEY").performClick()

        awaitNode(titled(CONVO_LABEL))
        awaitNone(hasTestTag("project-page"))
    }

    @Test
    fun theChevronFoldsWithoutOpeningThePage() {
        launch()
        awaitNode(hasTestTag("fold:$PROJECT_KEY"))

        composeRule.onNodeWithTag("fold:$PROJECT_KEY").performClick()

        awaitNone(hasTestTag("convo:$CONVO_KEY"))
        assertAbsent(hasTestTag("project-page"))
    }
}
