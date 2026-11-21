package io.github.joshsymonds.orrery.ui

import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.google.protobuf.MessageLite
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "fedcba9876543210fedcba9876543210"
private const val EPIC_ID = "example-repo/example-epic"
private const val EPIC_SLUG = "example-epic"
private const val EPIC_REPOSITORY = "example-repo"
private const val TASK = "T1"
private const val TITLE = "Parser T1"
private const val AGENT = "synthetic-agent"
private const val NOW = 1_700_000_000L
private const val DRAFT = "synthetic nudge draft"
private const val CHECK_NAME = "synthetic-check"
private const val CHECK_NOTE = "Synthetic check note."
private const val REPLY_TEXT = "Synthetic reply."
private const val SUMMARY = "Synthetic summary line."
private const val REFUSAL_REASON = "synthetic refusal"
private const val RUNNING_HINT = "Nudge $TITLE. It interrupts the turn, then gives it your words"
private const val HELD_HINT = "Nudge $TITLE. It resumes with your words"
private const val NOT_KNOWN_NOTE = "nudge not known: it may have landed; nothing was resent"
private const val EPICS_GET_PATH = "/orrery.v1.Epics/Get"
private const val WATCH_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val NUDGE_PATH = "/orrery.v1.Steps/Nudge"
private const val STEPS_STOP_PATH = "/orrery.v1.Steps/Stop"
private const val TITLE_MORE_TAG = "head-more"
private const val TITLE_MENU_FIND_TAG = "head-menu-find"
private const val FIND_QUERY = "synthetic match"
private const val FIRST_MATCH = "The first synthetic match."
private const val SECOND_MATCH = "The second synthetic match."

// A worker's session screen in step mode, driven over a fake server that serves the epic's Get and the session's stream.
@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class WorkerSessionTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val epicsOpened = CopyOnWriteArrayList<String>()
    private val tasksOpened = CopyOnWriteArrayList<Pair<String, Phase>>()

    @Test
    fun theCrumbsAndTheBandSitAtTheTopAndTheDockUnderThem() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            showSession(server, detail(step()))

            composeRule.awaitExactlyOne(hasTestTag("epic-pin"))
            composeRule.onNodeWithTag("crumb:0").assertTextEquals(EPIC_SLUG)
            composeRule.onNodeWithTag("crumb:1").assertTextEquals(TITLE)
            composeRule.onNodeWithTag("crumb:2").assertTextEquals("Worker session")
            composeRule.onNodeWithTag("task-chip:$TASK").assertIsSelected()
            composeRule.awaitExactlyOne(hasTestTag("dock"))

            val crumbs = nodeTop("crumb:2")
            val band = nodeTop("epic-pin")
            val strip = nodeTop("task-strip")
            val dock = nodeTop("dock")
            assertTrue("crumbs $crumbs, band $band, strip $strip, dock $dock", crumbs < band && band < strip && strip < dock)
        }
    }

    @Test
    fun theEpicAndTheTaskCrumbsOpenTheirRecords() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            showSession(server, detail(step()))
            composeRule.awaitExactlyOne(hasTestTag("epic-pin"))

            composeRule.onNodeWithTag("crumb:0").performClick()
            composeRule.onNodeWithTag("crumb:1").performClick()

            assertEquals(listOf(EPIC_ID), epicsOpened.toList())
            assertEquals(listOf(TASK to Phase.IMPLEMENTING), tasksOpened.toList())
        }
    }

    @Test
    fun checkRoundsOfTheStepDrawAsCheckBlocksWithItsChecks() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working", items = listOf(checkRound(round = 1)))
            showSession(server, detail(step(checks = listOf(check(round = 1, ok = false)))))

            // The block draws from the stream before the epic's Get hands it the step's checks, so the chip is awaited.
            composeRule.awaitExactlyOne(hasText(CHECK_NAME) and hasAnyAncestor(hasTestTag("block:checks")))
            composeRule.onNodeWithText("round 1 from the engine").assertExists()
            composeRule.onNode(hasText(CHECK_NOTE) and hasAnyAncestor(hasTestTag("block:checks")), useUnmergedTree = true)
                .assertExists()
            composeRule.onNodeWithTag("block:checks").assert(
                hasAnyDescendant(hasTestTag("rail") and SemanticsMatcher.expectValue(ShapeToneKey, "bad")),
            )
        }
    }

    @Test
    fun aRunningStepsSendIsOneNudgeWithTheRunningPlaceholder() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            server.respond(NUDGE_PATH, StepsNudgeResponse.getDefaultInstance())
            showSession(server, detail(step()))
            composeRule.awaitExactlyOne(hasTestTag("composer"))
            assertEquals(RUNNING_HINT, editField().hint.toString())

            setDraft(DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitExactlyOne(hasText("nudge landed"))

            val nudges = server.requests.filter { it.rawPath == NUDGE_PATH }
            assertEquals(1, nudges.size)
            val sent = StepsNudgeRequest.parseFrom(nudges.single().body)
            assertEquals(SESSION_ID, sent.id)
            assertEquals(DRAFT, sent.text)
        }
    }

    @Test
    fun aHeldStepIsPeachAndItsPlaceholderSaysItResumes() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            showSession(server, detail(step(held = true)))

            composeRule.awaitExactlyOne(hasTestTag("composer-edge:held"))
            composeRule.onNodeWithTag("dock-word").assertTextEquals("held")
            assertEquals(HELD_HINT, editField().hint.toString())
        }
    }

    @Test
    fun stopAsksFirstAndThenPostsOneStepsStop() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            server.respond(STEPS_STOP_PATH, StepsStopResponse.getDefaultInstance())
            showSession(server, detail(step()))
            composeRule.awaitExactlyOne(hasTestTag("dock-more"))

            composeRule.onNodeWithTag("dock-more").performClick()
            composeRule.onNodeWithText("Stop $TITLE?").assertExists()
            assertEquals(0, countPosts(server, STEPS_STOP_PATH))

            composeRule.onNodeWithTag("dock-stop-confirm").performClick()
            composeRule.awaitUntil("the stop call") { countPosts(server, STEPS_STOP_PATH) == 1 }
            assertEquals(SESSION_ID, StepsStopRequest.parseFrom(server.requests.single { it.rawPath == STEPS_STOP_PATH }.body).id)
        }
    }

    @Test
    fun anEndedStepHasNoComposeBoxAndItsEndIsTheLastBlock() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "done", items = listOf(reply(REPLY_TEXT)))
            showSession(server, detail(step(end = "done", summary = SUMMARY)))

            composeRule.awaitExactlyOne(hasTestTag("block:stepend"))
            composeRule.onAllNodesWithTag("composer").assertCountEquals(0)
            composeRule.onAllNodesWithTag("dock").assertCountEquals(0)
            composeRule.onNode(hasText("STEP ENDED") and hasAnyAncestor(hasTestTag("block:stepend")), useUnmergedTree = true)
                .assertExists()
            composeRule.onNode(hasText(AGENT) and hasAnyAncestor(hasTestTag("block:stepend")), useUnmergedTree = true)
                .assertExists()
            composeRule.onNode(hasText("done") and hasAnyAncestor(hasTestTag("block:stepend")), useUnmergedTree = true)
                .assertExists()
            composeRule.onNode(hasText(SUMMARY) and hasAnyAncestor(hasTestTag("block:stepend")), useUnmergedTree = true)
                .assertExists()
            composeRule.onNodeWithTag("block:stepend").assert(
                hasAnyDescendant(hasTestTag("rail") and SemanticsMatcher.expectValue(ShapeToneKey, "ok")),
            )
            assertTrue(nodeTop("block:stepend") > topOf(hasText(REPLY_TEXT)))
        }
    }

    @Test
    fun aNudgeWithNoAnswerShowsTheNoteAndKeepsTheDraft() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            server.drop(NUDGE_PATH)
            showSession(server, detail(step()))
            composeRule.awaitExactlyOne(hasTestTag("composer"))

            setDraft(DRAFT)
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitExactlyOne(hasText(NOT_KNOWN_NOTE))

            assertEquals(DRAFT, editField().text.toString())
            assertEquals(1, countPosts(server, NUDGE_PATH))

            // The same text is not sent twice: a second Send of it posts nothing, and only a changed draft is sent.
            composeRule.onNodeWithText("Send").performClick()
            setDraft("$DRAFT, changed")
            composeRule.onNodeWithText("Send").performClick()
            composeRule.awaitUntil("the changed draft is nudged") { nudgedTexts(server).lastOrNull() == "$DRAFT, changed" }
            assertEquals(listOf(DRAFT, "$DRAFT, changed"), nudgedTexts(server))
        }
    }

    @Test
    fun stepSessionFindShowsMoreInTheTitleRowWithFindInItsMenu() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working")
            showSession(server, detail(step()))
            composeRule.awaitExactlyOne(hasTestTag("epic-pin"))

            // More is the title row's: on the crumbs' line, above the band that ties the step to its epic.
            composeRule.onNodeWithTag(TITLE_MORE_TAG)
                .assert(SemanticsMatcher.expectValue(SemanticsProperties.ContentDescription, listOf("More")))
            assertTrue(nodeTop(TITLE_MORE_TAG) < nodeTop("epic-pin"))

            composeRule.onNodeWithTag(TITLE_MORE_TAG).performClick()

            composeRule.onNodeWithTag(TITLE_MENU_FIND_TAG).assertTextEquals("Find")
        }
    }

    @Test
    fun stepSessionFindOpensFindOnTheStepsTranscriptAndStepsThroughItsMatches() {
        FakeOrreryServer().use { server ->
            serveStream(server, state = "working", items = listOf(reply(FIRST_MATCH), reply(SECOND_MATCH)))
            showSession(server, detail(step()))
            composeRule.awaitExactlyOne(hasText(SECOND_MATCH))

            composeRule.onNodeWithTag(TITLE_MORE_TAG).performClick()
            composeRule.onNodeWithTag(TITLE_MENU_FIND_TAG).performClick()
            composeRule.onNode(hasSetTextAction() and hasAnyAncestor(hasTestTag("find-bar"))).performTextInput(FIND_QUERY)

            composeRule.onNodeWithTag("find-count").assertTextEquals("1 of 2")
            composeRule.onNodeWithTag("find-next").performClick()
            composeRule.onNodeWithTag("find-count").assertTextEquals("2 of 2")
            composeRule.onNodeWithTag("find-next").performClick()
            composeRule.onNodeWithTag("find-count").assertTextEquals("1 of 2")
        }
    }

    private fun showSession(server: FakeOrreryServer, detail: EpicsGetResponse) {
        server.respond(EPICS_GET_PATH, detail)
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionScreen(
                    sessionId = SESSION_ID,
                    client = client,
                    step = StepMode(held = false, label = TASK, epic = EPIC_ID, task = TASK),
                    onOpenEpic = { epicsOpened.add(it) },
                    onOpenTask = { task, phase -> tasksOpened.add(task to phase) },
                    clock = { NOW },
                )
            }
        }
    }

    /** Scripts the watch stream: the first request gets one delta with [items] and [state], later ones a quiet stream. */
    private fun serveStream(server: FakeOrreryServer, state: String, items: List<ConversationItemProto> = emptyList()) {
        val delta = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(1L)
            .addAllItems(items)
            .setState(stateOf(state))
            .build()
        val requests = AtomicInteger()
        server.route(WATCH_PATH) { _ ->
            if (requests.getAndIncrement() == 0) Reply.Stream(listOf<MessageLite>(delta), StreamEnd.Open)
            else Reply.Stream(emptyList(), StreamEnd.Open)
        }
    }

    private fun stateOf(state: String): ConversationState = ConversationState.newBuilder()
        .setOrigin("synthetic origin")
        .setSession(
            HeadlessSession.newBuilder()
                .setId(SESSION_ID)
                .setDir("/home/user/project")
                .setLabel("example-label")
                .setState(state)
                .setChanged(4102444800L)
                .setSeen(4102444900L),
        )
        .build()

    private fun reply(text: String): ConversationItemProto =
        ConversationItemProto.newBuilder().setKind("reply").setTime(NOW).setText(text).build()

    // The engine's send-back of check round 1: a prompt from the engine, which the conversation draws as its check block.
    private fun checkRound(round: Int): ConversationItemProto = ConversationItemProto.newBuilder()
        .setKind("prompt")
        .setTime(NOW)
        .setText(CHECK_NOTE)
        .setSender("engine")
        .setRound(round)
        .build()

    private fun check(round: Int, ok: Boolean): EpicStepCheck = EpicStepCheck.newBuilder()
        .setRound(round)
        .setName(CHECK_NAME)
        .setOk(ok)
        .build()

    private fun step(
        end: String = "",
        held: Boolean = false,
        summary: String = "",
        checks: List<EpicStepCheck> = emptyList(),
    ): EpicStep = EpicStep.newBuilder()
        .setId(SESSION_ID)
        .setTask(TASK)
        .setAgent(AGENT)
        .setEnd(end)
        .setStatus(if (held) "held" else "working")
        .setHeld(held)
        .setStage("implement")
        .setStarted(NOW - 60)
        .setEnded(if (end.isEmpty()) 0L else NOW - 30)
        .setDoing("Synthetic work.")
        .setReason("")
        .setRole("implementer")
        .setSummary(summary)
        .addAllChecks(checks)
        .build()

    private fun detail(step: EpicStep): EpicsGetResponse = EpicsGetResponse.newBuilder()
        .setEpic(epic())
        .addSteps(step)
        .addBuildTasks(
            EpicBuildTask.newBuilder()
                .setId(TASK)
                .setTitle(TITLE)
                .setStatus("dispatched")
                .setStage("implement")
                .addRequirements("R1")
                .build(),
        )
        .build()

    private fun epic(): Epic = Epic.newBuilder()
        .setId(EPIC_ID)
        .setRepository(EPIC_REPOSITORY)
        .setSlug(EPIC_SLUG)
        .setOutcome("")
        .setChanged(NOW - 30)
        .setLive(true)
        .setSource("orreryd")
        .setEndedSteps(0)
        .setProblem("")
        .build()

    private fun nodeTop(tag: String): Float = topOf(hasTestTag(tag))

    private fun topOf(matcher: SemanticsMatcher): Float =
        composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode().positionInRoot.y

    private fun countPosts(server: FakeOrreryServer, path: String): Int =
        server.requests.count { it.method == "POST" && it.rawPath == path }

    private fun nudgedTexts(server: FakeOrreryServer): List<String> =
        server.requests.filter { it.method == "POST" && it.rawPath == NUDGE_PATH }
            .map { StepsNudgeRequest.parseFrom(it.body).text }

    private fun setDraft(text: String) {
        composeRule.runOnUiThread { editField().setText(text) }
        composeRule.waitForIdle()
    }

    private fun editField(): EditText =
        checkNotNull(findEditText(composeRule.activity.window.decorView)) {
            "SessionScreen must host the draft's EditText"
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
}
