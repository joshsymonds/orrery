package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask as EpicBuildTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep as EpicStepProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsOlderRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsOlderResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "example-epic"
private const val TASK = "T1"
private const val OTHER_TASK = "T2"
private const val NOW = 1_700_000_000L
private const val BRIEF = "Synthetic brief text."
private const val EPIC_REPOSITORY = "example-repo"
private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val EPICS_GET = "/orrery.v1.Epics/Get"
private const val SESSION_WATCH_CONVERSATION = "/orrery.v1.Sessions/WatchConversation"
private const val SESSION_OLDER = "/orrery.v1.Sessions/Older"
// The reset window holds RESET_ITEMS replies, none the brief, with its oldest item at OLDEST_SEQ; the older page holds
// the brief and ends at BRIEF_PAGE_OLDEST.
private const val RESET_ITEMS = 51
private const val OLDEST_SEQ = 100L
private const val BRIEF_PAGE_OLDEST = 40L

// The record's band, title row, requirement lines and brief, driven as its stateless body over synthetic epic details.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w600dp-h1200dp")
class TaskBandRecordTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun theBandDrawsThePinAndOneChipPerTaskWithThisOneSelected() {
        showBody(detail(buildTasks = listOf(buildTask(TASK), buildTask(OTHER_TASK, status = "gated"))))

        composeRule.onNodeWithTag("epic-pin", useUnmergedTree = true).assertExists()
        composeRule.onAllNodesWithTag("task-strip", useUnmergedTree = true).assertCountEquals(1)
        composeRule.onNodeWithTag("task-chip:$TASK", useUnmergedTree = true).assertIsSelected()
        composeRule.onNodeWithTag("task-chip:$OTHER_TASK", useUnmergedTree = true).assertIsNotSelected()
    }

    @Test
    fun aChipOpensItsTaskAtItsPhaseAndTheCurrentChipOpensNothing() {
        val opened = CopyOnWriteArrayList<Pair<String, Phase>>()
        showBody(
            detail(buildTasks = listOf(buildTask(TASK, stage = "red"), buildTask(OTHER_TASK))),
            onOpenTask = { task, phase -> opened.add(task to phase) },
        )

        composeRule.onNodeWithTag("task-chip:$TASK", useUnmergedTree = true).performClick()
        composeRule.onNodeWithTag("task-chip:$OTHER_TASK", useUnmergedTree = true).performClick()
        composeRule.waitForIdle()

        // A planned task opens at implementing; the current one is inert.
        assertEquals(listOf(OTHER_TASK to Phase.IMPLEMENTING), opened.toList())
    }

    @Test
    fun thePinOpensTheEpicAndTheCrumbsNameTheEpicAndTheTask() {
        val opened = AtomicInteger()
        showBody(detail(buildTasks = listOf(buildTask(TASK))), onOpenEpic = { opened.incrementAndGet() })

        composeRule.onNodeWithTag("crumb:0").assertTextEquals(EPIC)
        composeRule.onNodeWithTag("crumb:1").assertTextEquals("Parser $TASK")
        composeRule.onAllNodesWithTag("crumb:2").assertCountEquals(0)
        composeRule.onNodeWithTag("epic-pin", useUnmergedTree = true).performClick()
        composeRule.waitForIdle()

        assertEquals(1, opened.get())
    }

    @Test
    fun theCrumbAndBackOpenTheEpicRecord() {
        val opened = AtomicInteger()
        showBody(detail(buildTasks = listOf(buildTask(TASK))), onOpenEpic = { opened.incrementAndGet() })

        composeRule.onNodeWithTag("crumb:0").performClick()
        composeRule.onNodeWithTag("title-back").performClick()
        composeRule.waitForIdle()

        assertEquals(2, opened.get())
    }

    @Test
    fun aRequirementLineNamesItsRequirementAndItsIdAloneWhenUnlisted() {
        val epic = detail(
            buildTasks = listOf(buildTask(TASK, requirements = listOf("R1", "R2"))),
            requirements = listOf(requirement("R1", "Synthetic requirement text")),
        )

        showBody(epic)

        composeRule.onNodeWithTag("task-requirement:R1")
            .assertTextContains("Synthetic requirement text", substring = true)
        composeRule.onNodeWithTag("task-requirement:R2").assertTextEquals("R2")
    }

    @Test
    fun noBriefIsDrawnBeforeTheBriefIsRead() {
        showBody(detail(buildTasks = listOf(buildTask(TASK))), brief = null)

        composeRule.onAllNodesWithTag("task-brief", useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun theBriefIsDrawnUnderItsLabelOnceItIsRead() {
        showBody(detail(buildTasks = listOf(buildTask(TASK))), brief = BRIEF)

        composeRule.onNodeWithText("The planner’s brief", useUnmergedTree = true).assertExists()
        composeRule.onNodeWithTag("task-brief").assertTextContains(BRIEF, substring = true)
    }

    @Test
    fun theFootReadsTheTasksWordsWhileNoStepRuns() {
        showBody(detail(buildTasks = listOf(buildTask(TASK, status = "gated"))))

        composeRule.onNodeWithTag("task-state", useUnmergedTree = true).assertTextEquals("accepted · waits to integrate")
    }

    @Test
    fun theBriefIsPagedBackToWhenTheResetWindowHoldsNoneOfIt() {
        FakeOrreryServer().use { server ->
            server.respond(EPICS_GET, briefEpic())
            server.route(SESSION_WATCH_CONVERSATION) { Reply.Stream(listOf(resetWithoutBrief()), StreamEnd.Clean) }
            server.respond(SESSION_OLDER, olderPageWithBrief())
            setScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("task-brief"))

            composeRule.onNodeWithTag("task-brief").assertTextContains(BRIEF, substring = true)
            val older = SessionsOlderRequest.parseFrom(server.requests.single { it.rawPath == SESSION_OLDER }.body)
            assertEquals(SESSION_ID, older.id)
            assertEquals(OLDEST_SEQ, older.before)
        }
    }

    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                PhaseOutputScreen(
                    client = client,
                    epicId = "$EPIC_REPOSITORY/$EPIC",
                    epic = EPIC,
                    task = TASK,
                    onOpenEpic = {},
                    onOpenTask = { _, _ -> },
                    onOpenSession = {},
                    now = { NOW },
                )
            }
        }
    }

    // The epic with one planned task and its implementer step, whose session the brief is read from.
    private fun briefEpic(): EpicsGetResponse = EpicsGetResponse.newBuilder()
        .setEpic(Epic.newBuilder().setId("$EPIC_REPOSITORY/$EPIC").setRepository(EPIC_REPOSITORY).setSlug(EPIC).setLive(true))
        .addBuildTasks(
            EpicBuildTaskProto.newBuilder()
                .setId(TASK)
                .setTitle("Parser $TASK")
                .setStatus("dispatched")
                .addRequirements("R1"),
        )
        .addSteps(EpicStepProto.newBuilder().setId(SESSION_ID).setTask(TASK).setRole("implementer").setStarted(NOW - 600))
        .build()

    private fun resetWithoutBrief(): SessionsWatchConversationResponse {
        val reset = SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(OLDEST_SEQ + RESET_ITEMS)
            .setReset(true)
            .setOldest(OLDEST_SEQ)
        repeat(RESET_ITEMS) { index ->
            reset.addItems(
                ConversationItemProto.newBuilder()
                    .setKind("reply")
                    .setTime(NOW + index)
                    .setText("Synthetic reply $index."),
            )
        }
        return reset.build()
    }

    private fun olderPageWithBrief(): SessionsOlderResponse = SessionsOlderResponse.newBuilder()
        .addItems(
            ConversationItemProto.newBuilder()
                .setKind("prompt")
                .setTime(NOW - 600)
                .setText(BRIEF)
                .setSender("brief"),
        )
        .setOldest(BRIEF_PAGE_OLDEST)
        .setMore(true)
        .build()

    private fun showBody(
        detail: EpicDetail,
        task: String = TASK,
        brief: String? = null,
        onOpenEpic: () -> Unit = {},
        onOpenTask: (String, Phase) -> Unit = { _, _ -> },
    ) {
        composeRule.setContent {
            MaterialTheme {
                PhaseOutputBody(
                    detail = detail,
                    task = task,
                    brief = brief,
                    now = NOW,
                    onOpenEpic = onOpenEpic,
                    onOpenTask = onOpenTask,
                    onOpenSession = {},
                    hold = { StepCallResult.Landed },
                    stop = { StepCallResult.Landed },
                    onLanded = {},
                    loadArtifact = { _, _, _ -> null },
                )
            }
        }
    }

    private fun requirement(id: String, text: String): EpicRequirementRow =
        EpicRequirementRow(id = id, text = text, evidence = "", kind = "check")

    private fun buildTask(
        id: String = TASK,
        status: String = "dispatched",
        stage: String = "",
        requirements: List<String> = listOf("R1"),
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = id,
        title = "Parser $id",
        status = status,
        stage = stage,
        red = null,
        green = null,
        requirements = requirements,
    )

    private fun detail(
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
        requirements: List<EpicRequirementRow> = emptyList(),
    ): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/$EPIC",
            repository = "example-repo",
            slug = EPIC,
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
        ),
        tasks = emptyList(),
        slices = emptyList(),
        steps = steps,
        requirements = requirements,
        buildTasks = buildTasks,
    )
}
