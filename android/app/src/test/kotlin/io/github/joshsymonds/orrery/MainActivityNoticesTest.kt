package io.github.joshsymonds.orrery

import android.content.Context
import android.content.Intent
import android.provider.Settings
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDisplayed
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListedNotice
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeBlock
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesReadAllResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import org.junit.After
import org.junit.Assert.assertFalse
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
private const val HEADLESS_PATH = "/orrery.v1.Sessions/ListHeadless"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val EPICS_LIST_PATH = "/orrery.v1.Epics/List"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val EPICS_HISTORY_PATH = "/orrery.v1.Epics/History"
private const val NOTICES_LIST_PATH = "/orrery.v1.Notices/List"
private const val NOTICES_SEEN_PATH = "/orrery.v1.Notices/Seen"
private const val NOTICES_READ_ALL_PATH = "/orrery.v1.Notices/ReadAll"
private const val NOTICES_WATCH_PATH = "/orrery.v1.Notices/Watch"
private const val BELL = "open-notices"
private const val NOTICES_BACK = "notices-back"
private const val EPIC_ID = "example-repo/example-epic"
private const val EPIC_SLUG = "example-epic"
private const val SESSION_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
private const val PANE_ID = "%1"
private const val SIXTY_SECONDS = 60_000L

// A summary of sixty lines is taller than the screen, so the epic's now section starts below the screen's top.
private val TALL_SUMMARY = (1..60).joinToString("\n") { "Synthetic summary line $it." }

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class MainActivityNoticesTest {
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
        server.respond(PANES_PATH, panesOf(paneProto(PANE_ID, "alpha-build"), paneProto("%2", "alpha-review")))
        server.respond(EPICS_LIST_PATH, EpicsListResponse.newBuilder().addEpics(liveEpic()).build())
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder().setEpic(liveEpic()).addSteps(epicStep()).build(),
        )
        server.respond(EPICS_HISTORY_PATH, EpicsHistoryResponse.getDefaultInstance())
        server.respond(NOTICES_SEEN_PATH, NoticesSeenResponse.getDefaultInstance())
        server.respond(NOTICES_READ_ALL_PATH, NoticesReadAllResponse.getDefaultInstance())
        val session = HeadlessSession.newBuilder()
            .setId(SESSION_ID)
            .setDir("/home/user/beta")
            .setLabel("beta-agent")
            .setState("done")
            .setChanged(NOW - 900)
            .build()
        server.respond(HEADLESS_PATH, ListHeadlessResponse.newBuilder().addSessions(session).build())
        // The session screen shows its head once its conversation streams in, so the stream carries the session's state.
        val state = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .setState(ConversationState.newBuilder().setOrigin("synthetic origin").setSession(session))
            .build()
        server.route(WATCH_PATH) { Reply.Stream(listOf(state), StreamEnd.Open) }
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

    private fun panesOf(vararg panes: PaneProto): PanesListResponse =
        PanesListResponse.newBuilder().addAllPanes(panes.toList()).build()

    private fun epicStep(): EpicStep = EpicStep.newBuilder()
        .setId("step-synthetic-alpha")
        .setTask("synthetic-task")
        .setAgent("synthetic-agent")
        .setEnd("")
        .setStatus("working")
        .setHeld(false)
        .setStarted(NOW - 60)
        .setEnded(0L)
        .setDoing("Synthetic work.")
        .setReason("")
        .build()

    private fun liveEpic(): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository("example-repo")
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setSource("orreryd")
        .addRunningSteps(epicStep())
        .setEndedSteps(0)
        .build()

    private fun endedEpic(): Epic = Epic.newBuilder(liveEpic()).setLive(false).setOutcome("released").build()

    /** The question the epic waits on, asked two minutes before [NOW]. */
    private fun openQuestion(): EpicQuestion = EpicQuestion.newBuilder()
        .setId("Q1")
        .setText("Synthetic question text?")
        .setTurn(1L)
        .setAsked(NOW - 120L)
        .build()

    /** Serves the epic's detail with its summary, its contract, its [questions] and its [parked] reason, as [epic]. */
    private fun serveEpicDetail(
        epic: Epic = liveEpic(),
        questions: List<EpicQuestion> = listOf(openQuestion()),
        parked: String = "",
    ) {
        val contract = EpicContract.newBuilder()
            .setTitle("Synthetic contract title")
            .setIntent("Synthetic intent text.")
            .build()
        server.respond(
            EPICS_GET_PATH,
            EpicsGetResponse.newBuilder()
                .setEpic(epic)
                .addSteps(epicStep())
                .setSummary(TALL_SUMMARY)
                .setContract(contract)
                .addAllQuestions(questions)
                .setParked(parked)
                .build(),
        )
    }

    /** Serves the notice history as [rows], the list the bell counts and the Notices screen shows. */
    private fun serveNotices(vararg rows: ListedNotice) {
        server.respond(NOTICES_LIST_PATH, NoticesListResponse.newBuilder().addAllNotices(rows.toList()).build())
    }

    private fun paneNotice(): ListedNotice = notice(NoticeKey.newBuilder().setPane(PANE_ID), "waiting", "Waiting", NOW - 600)

    private fun sessionNotice(): ListedNotice =
        notice(NoticeKey.newBuilder().setSession(SESSION_ID), "done", "Done", NOW - 900)

    private fun epicNotice(block: String): ListedNotice = notice(
        NoticeKey.newBuilder().setEpic(EPIC_ID),
        "waiting",
        "Waiting",
        NOW - 300,
        NoticeBlock.newBuilder().setKind(block).setRef("Q1").build(),
    )

    private fun limitNotice(): ListedNotice = notice(
        NoticeKey.newBuilder().setLimit("five_hour@1700000000"),
        "limited",
        "Limit reached",
        NOW - 100,
    )

    private fun alertNotice(): ListedNotice = notice(
        NoticeKey.newBuilder().setAlert("DiskFilling"),
        "firing",
        "Disk filling",
        NOW - 100,
    )

    private fun notice(
        key: NoticeKey.Builder,
        state: String,
        title: String,
        time: Long,
        block: NoticeBlock = NoticeBlock.getDefaultInstance(),
    ): ListedNotice = ListedNotice.newBuilder()
        .setKey(key)
        .setState(state)
        .setTitle(title)
        .setBody("")
        .setBlock(block)
        .setTime(time)
        .setRead(false)
        .build()

    private fun launch(): ActivityController<MainActivity> {
        val intent = Intent(context, MainActivity::class.java)
        return Robolectric.buildActivity(MainActivity::class.java, intent).setup().also { launched += it }
    }

    /** Launches the app and opens its Notices from the bell on the home screen. */
    private fun openNoticesFromHome(): ActivityController<MainActivity> {
        val controller = launch()
        awaitNode(hasTestTag(BELL))
        composeRule.onNodeWithTag(BELL).performClick()
        awaitNode(hasTestTag(NOTICES_BACK))
        return controller
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

    /** Whether the node with [tag] is composed and drawn on the screen, at least in part. */
    private fun isShown(tag: String): Boolean {
        val nodes = composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true)
        return nodes.fetchSemanticsNodes().isNotEmpty() && nodes[0].isDisplayed()
    }

    private fun awaitShown(tag: String) {
        composeRule.awaitUntil { isShown(tag) }
    }

    /** Lets the main thread and every request it started finish, so a Seen the row sent is on the server. */
    private fun settle() {
        Quiescence.awaitUntil("the screen's work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    @Test
    fun theBellOpensNoticesAndTheTitleBackReturnsHome() {
        serveNotices(limitNotice())
        launch()
        awaitNode(hasTestTag(BELL))

        composeRule.onNodeWithTag(BELL).performClick()
        awaitNode(hasTestTag(NOTICES_BACK))
        composeRule.onNodeWithTag(NOTICES_BACK).performClick()

        awaitNode(hasTestTag(BELL))
        awaitNone(hasTestTag(NOTICES_BACK))
    }

    @Test
    fun theSystemBackOnNoticesReturnsHome() {
        serveNotices(limitNotice())
        val controller = openNoticesFromHome()

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(BELL))
        awaitNone(hasTestTag(NOTICES_BACK))
    }

    @Test
    fun aPaneRowOpensItsPaneAndBackReturnsToNotices() {
        serveNotices(paneNotice())
        val controller = openNoticesFromHome()
        awaitNode(hasText("alpha"))

        composeRule.onNodeWithText("alpha").performClick()
        awaitNode(hasTestTag("head-back"))
        awaitNone(hasTestTag(NOTICES_BACK))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(NOTICES_BACK))
        awaitNone(hasTestTag("head-back"))
    }

    @Test
    fun aSessionRowOpensItsSessionAndBackReturnsToNotices() {
        serveNotices(sessionNotice())
        val controller = openNoticesFromHome()
        awaitNode(hasText("beta"))

        composeRule.onNodeWithText("beta").performClick()
        awaitNode(hasTestTag("head-back"))
        awaitNone(hasTestTag(NOTICES_BACK))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(NOTICES_BACK))
        awaitNone(hasTestTag("head-back"))
    }

    @Test
    fun anEpicRowAtItsQuestionOpensTheEpicAtItsNowSectionAndBackReturnsToNotices() {
        serveEpicDetail()
        serveNotices(epicNotice(block = "question"))
        val controller = openNoticesFromHome()
        awaitNode(hasText(EPIC_SLUG))

        composeRule.onNodeWithText(EPIC_SLUG).performClick()
        awaitShown("ask-question")
        awaitNone(hasTestTag(NOTICES_BACK))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(NOTICES_BACK))
        assertAbsent(hasTestTag("ask-question"))
    }

    @Test
    fun anEpicRowAtItsContractOpensTheContractAndBackReturnsToNotices() {
        serveEpicDetail()
        serveNotices(epicNotice(block = "contract"))
        val controller = openNoticesFromHome()
        awaitNode(hasText(EPIC_SLUG))

        composeRule.onNodeWithText(EPIC_SLUG).performClick()
        awaitNode(hasTestTag("contract-body"))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(NOTICES_BACK))
        awaitNone(hasTestTag("contract-body"))
    }

    @Test
    fun aReportRowOpensTheEpicAtItsTopAndBackReturnsToNotices() {
        serveEpicDetail(epic = endedEpic(), questions = emptyList())
        serveNotices(epicNotice(block = "report"))
        val controller = openNoticesFromHome()
        awaitNode(hasText(EPIC_SLUG))

        composeRule.onNodeWithText(EPIC_SLUG).performClick()
        awaitShown("epic-summary")
        awaitNone(hasTestTag(NOTICES_BACK))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(NOTICES_BACK))
        awaitNone(hasTestTag("epic-summary"))
    }

    @Test
    fun aReleaseStepRowOpensTheEpicAtItsNowSectionAndBackReturnsToNotices() {
        serveEpicDetail(questions = emptyList(), parked = "synthetic park")
        serveNotices(epicNotice(block = "release_step"))
        val controller = openNoticesFromHome()
        awaitNode(hasText(EPIC_SLUG))

        composeRule.onNodeWithText(EPIC_SLUG).performClick()
        awaitShown("ask-option:1")
        awaitShown("epic-park-stop")
        awaitNone(hasTestTag(NOTICES_BACK))

        controller.get().onBackPressedDispatcher.onBackPressed()

        awaitNode(hasTestTag(NOTICES_BACK))
        awaitNone(hasTestTag("epic-park-stop"))
    }

    @Test
    fun aLimitRowOpensNothing() {
        serveNotices(limitNotice())
        openNoticesFromHome()
        awaitNode(hasText("Limit reached"))

        composeRule.onNodeWithText("Limit reached").performClick()
        composeRule.awaitUntil("the row is marked seen") { server.requests.count { it.rawPath == NOTICES_SEEN_PATH } == 1 }
        settle()

        assertTrue(isShown(NOTICES_BACK))
        assertFalse(isShown("head-back"))
        assertFalse(isShown("title-back"))
    }

    @Test
    fun anAlertRowSendsSeenAndOpensNothing() {
        serveNotices(alertNotice())
        openNoticesFromHome()
        awaitNode(hasText("Disk filling"))

        composeRule.onNodeWithText("Disk filling").performClick()
        composeRule.awaitUntil("the row is marked seen") { server.requests.count { it.rawPath == NOTICES_SEEN_PATH } == 1 }
        settle()

        assertTrue(isShown(NOTICES_BACK))
        assertFalse(isShown("head-back"))
        assertFalse(isShown("title-back"))
    }

    @Test
    fun theAppHoldsNoNoticesWatchStream() {
        serveNotices(paneNotice(), sessionNotice())
        val controller = openNoticesFromHome()
        awaitNode(hasText("alpha"))

        composeRule.onNodeWithText("alpha").performClick()
        awaitNode(hasTestTag("head-back"))
        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitNode(hasTestTag(NOTICES_BACK))
        // The page comes back with a fresh list whose read lands after its head draws, and Mark all read waits for it.
        awaitNode(hasText("Mark all read"))

        composeRule.onNodeWithText("Mark all read").performClick()
        composeRule.awaitUntil("ReadAll is sent") { server.requests.any { it.rawPath == NOTICES_READ_ALL_PATH } }
        settle()

        controller.get().onBackPressedDispatcher.onBackPressed()
        awaitNode(hasTestTag(BELL))
        awaitNone(hasTestTag(NOTICES_BACK))

        // The home list reads the history again once a minute while started, so the clock moves past one such read.
        settle()
        val listedBefore = server.requests.count { it.rawPath == NOTICES_LIST_PATH }
        composeRule.mainClock.autoAdvance = false
        composeRule.mainClock.advanceTimeBy(2 * SIXTY_SECONDS, ignoreFrameDuration = true)
        composeRule.awaitUntil("the home list reads the notices again") {
            server.requests.count { it.rawPath == NOTICES_LIST_PATH } > listedBefore
        }
        settle()

        assertTrue(
            "the app opened a Notices.Watch stream",
            server.requests.none { it.rawPath == NOTICES_WATCH_PATH },
        )
    }
}
