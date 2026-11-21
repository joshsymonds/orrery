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
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStat as HostStatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.QueuedMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
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

private const val NOW = 1_700_000_000L
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val PANE_ROW_TAG = "convo:pane:example-host:%1"
private const val HEAD_TAG = "head"
private const val HEAD_TITLE_TAG = "head-title"
private const val HEAD_TEXT_TAG = "head-text"
private const val BAND_GOAL_TAG = "band-goal"
private const val BAND_HIDE_TAG = "band-hide"
private const val HEAD_METERS_TAG = "head-meters"
private const val TILE_STRIP_TAG = "tile-strip"
private const val COMPACT_HEAD_TAG = "compact-head"
private const val HOST_PATH = "/orrery.v1.Host/Status"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val PANE_ORRERY_ID = "synthetic-orrery-id"
private const val EPIC_ID = "example-repo/rota-export"
private const val EPIC_LINE_TAG = "band-line:$EPIC_ID"
private const val STATUS_LINE_TAG = "dock-state"
private const val DOCK_TAG = "dock"
private const val DOCK_TRAY_TAG = "dock-tray"
private const val COMPOSER_TAG = "composer"
private const val CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val HEADLESS_LIST_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val SESSION_ID = "session-synthetic"
private const val SESSION_ROW_TAG = "convo:session:$SESSION_ID"
private const val SESSION_LABEL = "synthetic session"
private const val SESSION_GOAL = "synthetic goal"
private const val REPLY_TEXT = "synthetic reply"
private const val WAITING_TEXT = "synthetic waiting message"
private const val QUEUED_TEXT = "synthetic queued prompt"
/** The open header's parts: the title row, the meters, the goal, an ask line and the tiles. Each draws with the keyboard down. */
private val OPEN_HEADER = listOf(
    HEAD_TAG,
    HEAD_TITLE_TAG,
    HEAD_TEXT_TAG,
    HEAD_METERS_TAG,
    BAND_GOAL_TAG,
    EPIC_LINE_TAG,
    TILE_STRIP_TAG,
)
/** The header folded to its line: the title row and the folded line. Each draws with the keyboard down. */
private val FOLDED_HEADER = listOf(HEAD_TAG, HEAD_TITLE_TAG, HEAD_TEXT_TAG, COMPACT_HEAD_TAG)
private const val PANE_GOAL = "synthetic pane goal"
private val CONVERSATION_RESPONSE: ConversationResponse = ConversationResponse.newBuilder()
    .setOrigin("synthetic origin")
    .setGoal(PANE_GOAL)
    .addItems(ConversationItem.newBuilder().setKind("reply").setTime(0).setText(REPLY_TEXT))
    .addWaiting(WaitingMessage.newBuilder().setText(WAITING_TEXT).setTime(0L).setCanceled(false))
    .build()
private val HOST_BODY: HostStatusResponse = HostStatusResponse.newBuilder()
    .setHost("example-host")
    .addStats(HostStatProto.newBuilder().setLabel("cpu").setValue("12%").setPercent(12.0).setLevel("ok"))
    .build()
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
        server.respond(PANES_PATH, panesProto("%1" to "alpha-build"))
        server.respond(EPICS_LIST_PATH, EpicsListResponse.getDefaultInstance())
    }

    @After
    fun closeServer() {
        launched.forEach { it.pause().stop().destroy() }
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
                    .setOrreryId(PANE_ORRERY_ID)
                    .setLabel(label)
                    .setChanged(NOW - 30)
                    .setSeen(NOW - 10)
                    .setQuestion("")
                    .setQuestionHash("")
                    .build()
            },
        ).build()

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
        ).setup().also { launched += it }
        composeRule.awaitUntil {
            composeRule.onAllNodes(hasTestTag("convo:pane:example-host:%1"), useUnmergedTree = true)
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

    /** Launches the activity, opens the row [rowTag] and returns once its conversation shows its reply. */
    private fun openRow(rowTag: String): ActivityController<MainActivity> {
        val controller = Robolectric.buildActivity(
            MainActivity::class.java,
            Intent(context, MainActivity::class.java),
        ).setup().also { launched += it }
        composeRule.awaitUntil {
            composeRule.onAllNodes(hasTestTag(rowTag), useUnmergedTree = true)
                .fetchSemanticsNodes().isNotEmpty()
        }
        composeRule.onNodeWithTag(rowTag, useUnmergedTree = true).performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodes(hasText(REPLY_TEXT), useUnmergedTree = true)
                .fetchSemanticsNodes().isNotEmpty()
        }
        return controller
    }

    /** Launches the activity and opens the Claude pane %1, returning once its conversation shows. */
    private fun openClaudePane(): ActivityController<MainActivity> {
        server.respond(CONVERSATION_PATH, CONVERSATION_RESPONSE)
        server.respond(SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        serveHost()
        serveAskingEpic(PANE_ORRERY_ID)
        return openRow(PANE_ROW_TAG)
    }

    /** Launches the activity and opens the headless session [SESSION_ID], returning once its conversation shows. */
    private fun openHeadlessSession(): ActivityController<MainActivity> {
        server.respond(
            HEADLESS_LIST_PATH,
            ListHeadlessResponse.newBuilder().addSessions(headlessSession()).build(),
        )
        server.route(WATCH_PATH) { Reply.Stream(listOf(sessionDelta()), StreamEnd.Open) }
        serveHost()
        serveAskingEpic(SESSION_ID)
        return openRow(SESSION_ROW_TAG)
    }

    /** This machine's host status: the header's meters draw, and the pane or session is on this host. */
    private fun serveHost() {
        server.respond(HOST_PATH, HOST_BODY)
    }

    /** One live epic, [EPIC_ID], steered by [steering] with an open question: its band line asks and its tile draws. */
    private fun serveAskingEpic(steering: String) {
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("rota-export")
            .setLive(true)
            .setSteering(steering)
            .build()
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(epic).build())
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder()
                .setEpic(epic)
                .addQuestions(EpicQuestionProto.newBuilder().setId("Q1").setText("Bump the embedded tz database?"))
                .build(),
        )
    }

    private fun headlessSession(): HeadlessSession = HeadlessSession.newBuilder()
        .setId(SESSION_ID)
        .setDir("/home/user/alpha")
        .setLabel(SESSION_LABEL)
        .setState("working")
        .setChanged(NOW - 30)
        .setSeen(NOW - 10)
        .build()

    /** The session's stream: its reply, its goal and one queued message, as its first delta. */
    private fun sessionDelta(): SessionsWatchConversationResponse = SessionsWatchConversationResponse.newBuilder()
        .setTranscript(SESSION_ID)
        .setSeq(1L)
        .setReset(true)
        .addItems(ConversationItem.newBuilder().setKind("reply").setTime(0).setText(REPLY_TEXT))
        .setState(
            ConversationState.newBuilder()
                .setOrigin("synthetic origin")
                .setGoal(SESSION_GOAL)
                .setSession(headlessSession()),
        )
        .addQueued(
            QueuedMessage.newBuilder()
                .setPromptId("prompt-1")
                .setText(QUEUED_TEXT)
                .setState("queued")
                .setWaits("turn"),
        )
        .build()

    /** Each of [tags] is drawn and displayed, once the reads that draw it have landed. */
    private fun assertDrawn(tags: List<String>) {
        tags.forEach { tag ->
            composeRule.awaitUntil {
                composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
            }
            composeRule.onNodeWithTag(tag, useUnmergedTree = true).assertIsDisplayed()
        }
    }

    /** None of [tags] is drawn. */
    private fun assertAbsent(tags: List<String>) {
        tags.forEach { tag ->
            composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).assertCountEquals(0)
        }
    }

    /** Folds the open header with the hide control at the end of its strip, once the strip has drawn its tiles. */
    private fun foldHeader() {
        assertDrawn(listOf(TILE_STRIP_TAG))
        composeRule.onNodeWithTag(BAND_HIDE_TAG, useUnmergedTree = true).performClick()
        composeRule.waitForIdle()
    }

    /** The dock's state line, its queue tray and its composer are drawn, and each sits above the keyboard. */
    private fun assertDockAboveKeyboard(controller: ActivityController<MainActivity>) {
        assertStatusAboveDraft(controller)
        val keyboardTop = composeRule.onNodeWithTag(ROOT_CONTENT_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInRoot.bottom
        listOf(DOCK_TRAY_TAG, COMPOSER_TAG).forEach { tag ->
            assertDrawn(listOf(tag))
            val bounds = composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            assertTrue(
                "$tag bottom ${bounds.bottom} should be above the keyboard top $keyboardTop",
                bounds.bottom <= keyboardTop,
            )
        }
    }

    /**
     * Typing folds the header and keeps the dock above the keyboard: with the keyboard down each of [drawn] draws, with it
     * up none of them draws and the dock stays, and with it down again they draw.
     */
    private fun assertKeyboardFoldsHeader(controller: ActivityController<MainActivity>, drawn: List<String>) {
        dispatchInsets(controller, keyboardShown = false)
        assertDrawn(drawn)

        dispatchInsets(controller, keyboardShown = true)
        assertAbsent(drawn)
        assertDockAboveKeyboard(controller)

        dispatchInsets(controller, keyboardShown = false)
        assertDrawn(drawn)
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
            "DockBox must host an EditText"
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

    /** The dock's state line sits inside the dock's bounds and its bottom is at or above the draft field's top. */
    private fun assertStatusAboveDraft(controller: ActivityController<MainActivity>) {
        composeRule.onNodeWithTag(STATUS_LINE_TAG, useUnmergedTree = true).assertIsDisplayed()
        val status = composeRule.onNodeWithTag(STATUS_LINE_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInWindow
        val composer = composeRule.onNodeWithTag(DOCK_TAG, useUnmergedTree = true)
            .fetchSemanticsNode().boundsInWindow
        val fieldOrigin = IntArray(2).also { editField(controller).getLocationInWindow(it) }
        assertTrue(
            "status $status should be inside the dock $composer",
            status.top >= composer.top && status.bottom <= composer.bottom,
        )
        assertTrue(
            "status bottom ${status.bottom} should be at or above field top ${fieldOrigin[1]}",
            status.bottom <= fieldOrigin[1],
        )
    }

    @Test
    fun keyboardUpFoldsHeaderAndKeepsStatusAboveDraft() {
        assertKeyboardFoldsHeader(openClaudePane(), OPEN_HEADER)
    }

    @Test
    fun headlessKeyboardUpFoldsHeaderAndKeepsDockAboveKeyboard() {
        assertKeyboardFoldsHeader(openHeadlessSession(), OPEN_HEADER)
    }

    @Test
    fun keyboardUpFoldsTheFoldedLineAndKeepsStatusAboveDraft() {
        val controller = openClaudePane()
        foldHeader()
        assertKeyboardFoldsHeader(controller, FOLDED_HEADER)
    }

    @Test
    fun headlessKeyboardUpFoldsTheFoldedLineAndKeepsDockAboveKeyboard() {
        val controller = openHeadlessSession()
        foldHeader()
        assertKeyboardFoldsHeader(controller, FOLDED_HEADER)
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
