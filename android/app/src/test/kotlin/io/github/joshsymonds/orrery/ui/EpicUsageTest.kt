package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicUsageSession
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.UsageTotal
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageEpicSession
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageEpicTotalsResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotal
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.math.abs

// The Usage section is read through the Compose node tree, from invented rows and from the epic fixture of
// web/src/fixtures/usage.ts, restated here with the expected strings of web/src/epicusage.test.tsx. The epic screen's reads
// are answered by the fake server, each read counted as it arrives, and each wait is for the screen's state, never a clock.

private const val USAGE_TAG = "epic-usage"
private const val ROW_TAG = "epic-usage-row"
private const val NAME_TAG = "epic-usage-who"
private const val SHARED_TAG = "epic-usage-shared"
private const val MODEL_TAG = "epic-usage-model"
private const val REQUESTS_TAG = "epic-usage-requests"
private const val CACHE_TAG = "epic-usage-cache"
private const val LIST_TAG = "epic-usage-list"
private const val BAR_TAG = "epic-usage-bar"
private const val BAR_FILL_TAG = "epic-usage-bar-fill"
private const val SHARED_NOTE = "shared · not in total"
private const val HEADER_LABEL = "USAGE"
private const val HEADER_SUMS = "\$2.70 at list · billed \$2.01"
private const val BAR_TOLERANCE = 0.01f

// RowFigures is one row of the section as the screen draws it: its name, model and figures.
private data class RowFigures(val name: String, val model: String, val requests: String, val cache: String, val list: String)

// The rows the web test expects of the fixture, largest list cost first.
private val FIXTURE_ROWS = listOf(
    RowFigures("steering", "opus-5-5", "1,204", "48M", "\$11.40"),
    RowFigures("T3 implement", "haiku-5-5", "408", "890k", "\$1.21"),
    RowFigures("T3 review", "sonnet-5-5", "96", "2.4M", "\$1.05"),
    RowFigures("steelman", "opus-5-5", "44", "900k", "\$0.23"),
    RowFigures("R4 judge", "sonnet-5-5", "31", "300k", "\$0.21"),
)

// The bars the web test expects, each row's list cost as a fraction of the largest row's, the steerer's.
private val FIXTURE_BARS = listOf(1.0f, 0.10614f, 0.09211f, 0.02018f, 0.01842f)

// The fixture's steps: the implement step writes T3, the review step reviews it read-only, the judge is an evidence step
// whose task names run r-judge1, and the agent step has no task.
private val fixtureSteps = listOf(
    step("3b9a4e10-build", task = "T3", agent = "builder", role = "implementer"),
    step("8f2e6c71-review", task = "T3", agent = "task-reviewer", role = "reviewer", readOnly = true),
    step("d4a07b53-judge", task = "evidence-r-judge1", agent = "judge", role = "evidence", readOnly = true),
    step("e61f9a2c-agent", agent = "steelman"),
)

// The fixture's requirements: R4's run r-judge1 is the one the judge step ran for.
private val fixtureRequirements = listOf(
    requirement("R4", runs = listOf("r-judge1")),
    requirement("R5", runs = listOf("r-other")),
)

// The fixture's sessions as Usage.EpicTotals answers them. The steerer is shared, and its row is outside the header's sums.
private val fixtureUsage = listOf(
    session(
        "3b9a4e10-build",
        total("sonnet-5-5", requests = 20L, cacheWrite = 40_000L, billed = 60_000_000L, list = 90_000_000L),
        total("haiku-5-5", agentType = "builder", requests = 388L, cacheWrite = 850_000L, billed = 900_000_000L, list = 1_120_000_000L),
    ),
    session(
        "7c1d0e2f-steering",
        total("opus-5-5", requests = 1_204L, cacheWrite = 48_000_000L, list = 11_400_000_000L),
        shared = true,
    ),
    session(
        "8f2e6c71-review",
        total("sonnet-5-5", agentType = "task-reviewer", requests = 96L, cacheWrite = 2_400_000L, billed = 800_000_000L, list = 1_050_000_000L),
    ),
    session(
        "d4a07b53-judge",
        total("sonnet-5-5", agentType = "judge", requests = 31L, cacheWrite = 300_000L, billed = 150_000_000L, list = 210_000_000L),
    ),
    session(
        "e61f9a2c-agent",
        total("opus-5-5", agentType = "steelman", requests = 44L, cacheWrite = 900_000L, billed = 100_000_000L, list = 230_000_000L),
    ),
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EpicUsageTest {
    private companion object {
        const val EPIC_ID = "example-repo/example-epic"
        const val NOW = 1_800_000_000L
        const val CHANGED = 4_102_444_800L
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EPICS_HISTORY = "/orrery.v1.Epics/History"
        const val EPICS_PAUSE = "/orrery.v1.Epics/Pause"
        const val USAGE_EPIC_TOTALS = "/orrery.v1.Usage/EpicTotals"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun rowsReadLargestFirstEachNamedByItsFixtureRule() {
        drawSection(fixtureUsage, fixtureSteps, fixtureRequirements)

        assertEquals(FIXTURE_ROWS.map { it.name }, figuresOfRows().map { it.name })
    }

    @Test
    fun eachNamingRuleNamesItsRow() {
        val steps = listOf(
            step("s-write", task = "T1", agent = "builder", role = "implementer"),
            step("s-read", task = "T1", agent = "reviewer", role = "reviewer", readOnly = true),
            step("s-judge", task = "evidence-r-7", agent = "judge", role = "evidence", readOnly = true),
            step("s-orphan", task = "evidence-r-gone", agent = "judge-agent", role = "evidence", readOnly = true),
            step("s-agent", agent = "steelman"),
        )
        // The sessions come in no order, so the rows must be sorted by their list cost, the steerer's first.
        val sessions = listOf(
            session("s-agent", total("opus-5-5", list = 500_000_000L)),
            session("d1d1d1d1-unheld", total("opus-5-5", list = 250_000_000L)),
            session("s-judge", total("sonnet-5-5", list = 2_000_000_000L)),
            session("s-steer", total("opus-5-5", list = 5_000_000_000L), shared = true),
            session("s-orphan", total("haiku-5-5", list = 1_000_000_000L)),
            session("s-write", total("haiku-5-5", list = 4_000_000_000L)),
            session("s-read", total("sonnet-5-5", list = 3_000_000_000L)),
        )

        drawSection(sessions, steps, listOf(requirement("R7", runs = listOf("r-7"))))

        assertEquals(
            listOf("steering", "T1 implement", "T1 review", "R7 judge", "judge-agent", "steelman", "d1d1d1d1"),
            figuresOfRows().map { it.name },
        )
    }

    @Test
    fun theSteererRowIsMarkedSharedAndOnlyItAndTheHeaderSumsLeaveItOut() {
        drawSection(fixtureUsage, fixtureSteps, fixtureRequirements)

        assertEquals(listOf(SHARED_NOTE, null, null, null, null), rowCells().map { it[SHARED_TAG] })
        composeRule.onNodeWithText(HEADER_LABEL).assertIsDisplayed()
        composeRule.onNodeWithText(HEADER_SUMS).assertIsDisplayed()
    }

    @Test
    fun eachRowReadsItsModelRequestsCacheWriteTokensAndListCost() {
        drawSection(fixtureUsage, fixtureSteps, fixtureRequirements)

        assertEquals(
            FIXTURE_ROWS.map { listOf(it.model, it.requests, it.cache, it.list) },
            figuresOfRows().map { listOf(it.model, it.requests, it.cache, it.list) },
        )
    }

    @Test
    fun barsAreRelativeToTheLargestRowWhichDrawsTheWholeTrack() {
        drawSection(fixtureUsage, fixtureSteps, fixtureRequirements)

        assertFractions(barFractions(), FIXTURE_BARS)
    }

    @Test
    fun aPhoneRowHasNoBarButReadsItsNameModelAndFigures() {
        drawSection(fixtureUsage, fixtureSteps, fixtureRequirements, narrow = true)

        assertEquals(0, countNodes(BAR_TAG))
        assertEquals(FIXTURE_ROWS, figuresOfRows())
    }

    @Test
    fun aSectionWithNoSessionDrawsNothing() {
        drawSection(emptyList())

        assertEquals(0, countNodes(USAGE_TAG))
    }

    @Test
    fun theUsageIsReadWhenTheEpicOpensAndAgainWhenAnEndedStepGrows() {
        FakeOrreryServer().use { server ->
            val ended = AtomicBoolean(false)
            val reads = AtomicInteger()
            serveEpic(
                server,
                ended,
                reads,
                listOf(
                    Reply.Message(usageAnswer(usageSession("s-run", 1_000_000_000L)), 0L),
                    Reply.Message(
                        usageAnswer(usageSession("s-run", 1_000_000_000L), usageSession("s-rev", 500_000_000L)),
                        0L,
                    ),
                ),
            )
            drawEpicScreen(server)
            composeRule.awaitUntil("the usage is drawn") { countNodes(USAGE_TAG) > 0 }

            assertEquals(1, reads.get())
            assertEquals(listOf("T2 implement"), figuresOfRows().map { it.name })

            ended.set(true)
            pauseEpic()
            composeRule.awaitUntil("the usage is read again") { reads.get() == 2 }
            composeRule.awaitUntil("the later answer is drawn") { countNodes(ROW_TAG) == 2 }

            assertEquals(listOf("T2 implement", "T2 review"), figuresOfRows().map { it.name })
        }
    }

    @Test
    fun aFailedReadDrawsNoSection() {
        FakeOrreryServer().use { server ->
            val ended = AtomicBoolean(false)
            val reads = AtomicInteger()
            serveEpic(
                server,
                ended,
                reads,
                listOf(
                    Reply.Message(usageAnswer(usageSession("s-run", 1_000_000_000L)), 0L),
                    Reply.Failure(Code.UNAVAILABLE, "usage is unavailable", 0L),
                ),
            )
            drawEpicScreen(server)
            composeRule.awaitUntil("the usage is drawn") { countNodes(USAGE_TAG) > 0 }

            ended.set(true)
            pauseEpic()
            composeRule.awaitUntil("the usage is read again") { reads.get() == 2 }
            composeRule.awaitNone(hasTestTag(USAGE_TAG))
        }
    }

    @Test
    fun anEmptyAnswerDrawsNoSection() {
        FakeOrreryServer().use { server ->
            val ended = AtomicBoolean(false)
            val reads = AtomicInteger()
            serveEpic(
                server,
                ended,
                reads,
                listOf(
                    Reply.Message(usageAnswer(usageSession("s-run", 1_000_000_000L)), 0L),
                    Reply.Message(UsageEpicTotalsResponse.getDefaultInstance(), 0L),
                ),
            )
            drawEpicScreen(server)
            composeRule.awaitUntil("the usage is drawn") { countNodes(USAGE_TAG) > 0 }

            ended.set(true)
            pauseEpic()
            composeRule.awaitUntil("the usage is read again") { reads.get() == 2 }
            composeRule.awaitNone(hasTestTag(USAGE_TAG))
        }
    }

    // drawSection draws the Usage section for the sessions, from the steps and requirements the epic holds, narrow or not.
    private fun drawSection(
        sessions: List<EpicUsageSession>,
        steps: List<StepRow> = emptyList(),
        requirements: List<EpicRequirementRow> = emptyList(),
        narrow: Boolean = false,
    ) {
        composeRule.setContent {
            EpicUsageSection(sessions = sessions, steps = steps, requirements = requirements, narrow = narrow)
        }
    }

    private fun countNodes(tag: String): Int = composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).fetchSemanticsNodes().size

    /** Each row's tagged texts, in the order the rows are drawn. */
    private fun rowCells(): List<Map<String, String>> =
        composeRule.onAllNodesWithTag(ROW_TAG, useUnmergedTree = true).fetchSemanticsNodes().map { textsUnder(it) }

    private fun figuresOfRows(): List<RowFigures> =
        rowCells().map { cells ->
            RowFigures(
                name = cells.getValue(NAME_TAG),
                model = cells.getValue(MODEL_TAG),
                requests = cells.getValue(REQUESTS_TAG),
                cache = cells.getValue(CACHE_TAG),
                list = cells.getValue(LIST_TAG),
            )
        }

    /** The fraction of its track each bar fill is drawn at, row by row: the fill's width over its track's. */
    private fun barFractions(): List<Float> {
        val tracks = composeRule.onAllNodesWithTag(BAR_TAG, useUnmergedTree = true).fetchSemanticsNodes()
        val fills = composeRule.onAllNodesWithTag(BAR_FILL_TAG, useUnmergedTree = true).fetchSemanticsNodes()
        assertEquals("each bar track has one fill", tracks.size, fills.size)
        return tracks.indices.map { fills[it].size.width.toFloat() / tracks[it].size.width.toFloat() }
    }

    private fun assertFractions(actual: List<Float>, expected: List<Float>) {
        assertEquals("drew ${actual.size} bars, expected ${expected.size}", expected.size, actual.size)
        expected.indices.forEach { i ->
            assertTrue(
                "bar $i is ${actual[i]}, expected ${expected[i]} within $BAR_TOLERANCE",
                abs(actual[i] - expected[i]) <= BAR_TOLERANCE,
            )
        }
    }

    /** The tagged texts under [node], by their test tags. */
    private fun textsUnder(node: SemanticsNode): Map<String, String> {
        val texts = mutableMapOf<String, String>()
        fun visit(current: SemanticsNode) {
            val tag = current.config.getOrNull(SemanticsProperties.TestTag)
            val text = current.config.getOrNull(SemanticsProperties.Text)
            if (tag != null && text != null) texts[tag] = text.joinToString("") { it.text }
            current.children.forEach(::visit)
        }
        node.children.forEach(::visit)
        return texts
    }

    /** Serves the epic, whose one step ends once [ended] is set, its history, its pause, and its usage, answered in turn. */
    private fun serveEpic(
        server: FakeOrreryServer,
        ended: AtomicBoolean,
        reads: AtomicInteger,
        answers: List<Reply>,
    ) {
        server.route(EPICS_GET) { Reply.Message(epicDetail(ended.get()), 0L) }
        server.respond(EPICS_HISTORY, EpicsHistoryResponse.getDefaultInstance())
        server.respond(EPICS_PAUSE, EpicsPauseResponse.getDefaultInstance())
        server.route(USAGE_EPIC_TOTALS) { answers[minOf(reads.incrementAndGet(), answers.size) - 1] }
    }

    private fun drawEpicScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            EpicScreen(
                client = client,
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = {},
                onOpenContract = {},
                onTalk = {},
                listedSteerer = { null },
                now = { NOW },
            )
        }
    }

    /** Pauses the epic from its More sheet, which makes the screen read the epic and its usage again. */
    private fun pauseEpic() {
        composeRule.awaitUntil("the epic's More is drawn") { countNodes("epic-more") > 0 }
        composeRule.onNodeWithTag("epic-more", useUnmergedTree = true).performClick()
        composeRule.awaitUntil("the pause is offered") { countNodes("epic-sheet-pause") > 0 }
        composeRule.onNodeWithTag("epic-sheet-pause", useUnmergedTree = true).performSemanticsAction(SemanticsActions.OnClick)
    }

    /** The epic's detail: a running implement step and a running review step, the implement step ended once [ended] is set. */
    private fun epicDetail(ended: Boolean): EpicsGetResponse {
        val steps = listOf(
            epicStep("s-run", "T2", "builder", "implementer", ended = ended),
            epicStep("s-rev", "T2", "task-reviewer", "reviewer", readOnly = true),
        )
        val epic = Epic.newBuilder()
            .setId(EPIC_ID)
            .setRepository("example-repo")
            .setSlug("example-epic")
            .setOutcome("built")
            .setChanged(CHANGED)
            .setLive(true)
            .setPhaseAt(NOW - 60)
            .setSource("orreryd")
            .addAllRunningSteps(steps.filter { it.end.isEmpty() })
            .setEndedSteps(steps.count { it.end.isNotEmpty() })
        return EpicsGetResponse.newBuilder().setEpic(epic).addAllSteps(steps).build()
    }

    private fun epicStep(
        id: String,
        task: String,
        agent: String,
        role: String,
        readOnly: Boolean = false,
        ended: Boolean = false,
    ): EpicStep {
        val builder = EpicStep.newBuilder()
            .setId(id)
            .setTask(task)
            .setAgent(agent)
            .setRole(role)
            .setReadOnly(readOnly)
            .setStatus("working")
            .setStarted(CHANGED - 600)
        if (ended) builder.setEnd("finished")
        return builder.build()
    }

    private fun usageAnswer(vararg sessions: UsageEpicSession): UsageEpicTotalsResponse =
        UsageEpicTotalsResponse.newBuilder().addAllSessions(sessions.toList()).build()

    /** A session on the wire, not shared, with one total of the list cost given. */
    private fun usageSession(session: String, list: Long): UsageEpicSession =
        UsageEpicSession.newBuilder()
            .setSession(session)
            .setShared(false)
            .addTotals(
                UsageSessionTotal.newBuilder()
                    .setModel("haiku-5-5")
                    .setRequests(1L)
                    .setListCostNanoUsd(list)
                    .build(),
            )
            .build()
}

private fun step(
    id: String,
    task: String = "",
    agent: String = "",
    role: String = "",
    readOnly: Boolean = false,
): StepRow =
    StepRow(
        id = id,
        task = task,
        agent = agent,
        end = "",
        status = "",
        held = false,
        started = 0L,
        ended = 0L,
        doing = "",
        checks = emptyList(),
        reason = "",
        role = role,
        readOnly = readOnly,
    )

private fun requirement(id: String, runs: List<String>): EpicRequirementRow =
    EpicRequirementRow(
        id = id,
        text = "The invented requirement $id",
        evidence = "the invented check",
        kind = "check",
        runs = runs.map { evidenceRun(it) },
    )

private fun evidenceRun(id: String): EvidenceRunRow =
    EvidenceRunRow(
        id = id,
        phase = "green",
        source = "captured",
        state = "ended",
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = "holds",
        judgeReason = "",
        passed = true,
        revision = "",
        created = 0L,
        ended = 0L,
        logBytes = 0L,
        stderrBytes = 0L,
        artifacts = emptyList(),
    )

private fun total(
    model: String,
    agentType: String = "",
    requests: Long = 1L,
    cacheWrite: Long = 0L,
    billed: Long = 0L,
    list: Long,
): UsageTotal =
    UsageTotal(
        model = model,
        agentType = agentType,
        requests = requests,
        inputTokens = 0L,
        outputTokens = 0L,
        cacheReadTokens = 0L,
        cacheWriteTokens = cacheWrite,
        billedNanoUsd = billed,
        listNanoUsd = list,
        listInputNanoUsd = null,
        listOutputNanoUsd = null,
        listCacheReadNanoUsd = null,
        listCacheWriteNanoUsd = null,
    )

private fun session(session: String, vararg totals: UsageTotal, shared: Boolean = false): EpicUsageSession =
    EpicUsageSession(session = session, shared = shared, totals = totals.toList())
