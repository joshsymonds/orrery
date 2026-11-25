package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StreamEnd
import io.github.joshsymonds.orrery.awaitExactlyOne
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotal as UsageSessionTotalProto
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotalsRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UsageSessionTotalsResponse
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.math.abs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val USAGE_PATH = "/orrery.v1.Usage/SessionTotals"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val PANE_CONVERSATION_PATH = "/orrery.v1.Panes/Conversation"
private const val PANE_SEEN_PATH = "/orrery.v1.Panes/MarkSeen"
private const val PANE_STREAM_PATH = "/orrery.v1.Panes/WatchConversation"
private const val SESSION_STREAM_PATH = "/orrery.v1.Sessions/WatchConversation"
private const val PANE_ID = "%3"
private const val PANE_ORRERY_ID = "0123456789abcdef0123456789abcdef"
private const val SESSION_ID = "fedcba9876543210fedcba9876543210"
private const val FIXED_NOW_SECONDS = 1_800_000_000L
private const val FIXTURE_CHANGED = 4102444800L
private const val FIXTURE_SEEN = 4102444900L
private const val READOUT = "head-usage"
private const val SHEET_LIST = "usage-list"

// The invented totals of web/src/fixtures/usage.ts, restated: costs in nano US dollars, and each kind's list cost as the
// fixture sets it. The figures the tests expect are the web tests' expected strings.

/** sessionTotals' list cost: $3.77, the four kinds summed. */
private const val SESSION_LIST_NANO = 3_770_000_000L

/** sessionTotals: four totals over 420 requests, billed $2.79, with every kind's cost known. */
private val SESSION_TOTALS = listOf(
    totalOf(
        model = "sonnet-5-5", agent = "task-reviewer", requests = 30L,
        inputTokens = 100_000L, outputTokens = 10_000L, cacheReadTokens = 4_000_000L, cacheWriteTokens = 100_000L,
        billed = 200_000_000L, list = 220_000_000L,
        listInput = 30_000_000L, listOutput = 30_000_000L, listCacheRead = 100_000_000L, listCacheWrite = 60_000_000L,
    ),
    totalOf(
        model = "opus-5-5", agent = "", requests = 240L,
        inputTokens = 900_000L, outputTokens = 150_000L, cacheReadTokens = 30_000_000L, cacheWriteTokens = 2_000_000L,
        billed = 1_900_000_000L, list = 2_670_000_000L,
        listInput = 270_000_000L, listOutput = 450_000_000L, listCacheRead = 750_000_000L, listCacheWrite = 1_200_000_000L,
    ),
    totalOf(
        model = "haiku-5-5", agent = "Explore", requests = 90L,
        inputTokens = 300_000L, outputTokens = 20_000L, cacheReadTokens = 4_000_000L, cacheWriteTokens = 100_000L,
        billed = 280_000_000L, list = 310_000_000L,
        listInput = 90_000_000L, listOutput = 60_000_000L, listCacheRead = 100_000_000L, listCacheWrite = 60_000_000L,
    ),
    totalOf(
        model = "opus-5-5", agent = "steelman", requests = 60L,
        inputTokens = 200_000L, outputTokens = 40_000L, cacheReadTokens = 6_000_000L, cacheWriteTokens = 400_000L,
        billed = 410_000_000L, list = 570_000_000L,
        listInput = 60_000_000L, listOutput = 120_000_000L, listCacheRead = 150_000_000L, listCacheWrite = 240_000_000L,
    ),
)

/** subscriptionTotals: billed nothing over 190 requests, with a list cost of $1.40 and every kind's cost known. */
private val SUBSCRIPTION_TOTALS = listOf(
    totalOf(
        model = "opus-5-5", agent = "", requests = 150L,
        inputTokens = 600_000L, outputTokens = 90_000L, cacheReadTokens = 12_000_000L, cacheWriteTokens = 800_000L,
        billed = 0L, list = 1_230_000_000L,
        listInput = 180_000_000L, listOutput = 270_000_000L, listCacheRead = 300_000_000L, listCacheWrite = 480_000_000L,
    ),
    totalOf(
        model = "sonnet-5-5", agent = "Explore", requests = 40L,
        inputTokens = 100_000L, outputTokens = 10_000L, cacheReadTokens = 2_000_000L, cacheWriteTokens = 100_000L,
        billed = 0L, list = 170_000_000L,
        listInput = 30_000_000L, listOutput = 30_000_000L, listCacheRead = 50_000_000L, listCacheWrite = 60_000_000L,
    ),
)

/** unsummedTotals: its kinds' costs come to $0.405 against the $0.49 list, so its kinds draw as tokens alone. */
private val UNSUMMED_TOTALS = listOf(
    totalOf(
        model = "opus-5-5", agent = "", requests = 50L,
        inputTokens = 400_000L, outputTokens = 30_000L, cacheReadTokens = 2_000_000L, cacheWriteTokens = 100_000L,
        billed = 300_000_000L, list = 400_000_000L,
        listInput = 120_000_000L, listOutput = 90_000_000L, listCacheRead = 50_000_000L, listCacheWrite = 60_000_000L,
    ),
    totalOf(
        model = "haiku-5-5", agent = "Explore", requests = 20L,
        inputTokens = 100_000L, outputTokens = 5_000L, cacheReadTokens = 400_000L, cacheWriteTokens = 50_000L,
        billed = 90_000_000L, list = 90_000_000L,
        listInput = 30_000_000L, listOutput = 15_000_000L, listCacheRead = 10_000_000L, listCacheWrite = 30_000_000L,
    ),
)

private fun totalOf(
    model: String,
    agent: String,
    requests: Long,
    inputTokens: Long,
    outputTokens: Long,
    cacheReadTokens: Long,
    cacheWriteTokens: Long,
    billed: Long,
    list: Long,
    listInput: Long,
    listOutput: Long,
    listCacheRead: Long,
    listCacheWrite: Long,
): UsageSessionTotalProto = UsageSessionTotalProto.newBuilder()
    .setModel(model)
    .setAgentType(agent)
    .setRequests(requests)
    .setInputTokens(inputTokens)
    .setOutputTokens(outputTokens)
    .setCacheReadTokens(cacheReadTokens)
    .setCacheWriteTokens(cacheWriteTokens)
    .setBilledCostNanoUsd(billed)
    .setListCostNanoUsd(list)
    .setListInputNanoUsd(listInput)
    .setListOutputNanoUsd(listOutput)
    .setListCacheReadNanoUsd(listCacheRead)
    .setListCacheWriteNanoUsd(listCacheWrite)
    .build()

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class UsageSheetTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    // The head, drawn alone: its readout's place in the title row, and its tap.

    private fun setHead(fold: HeadFold, usage: Long?, onUsage: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme {
                Head(
                    dir = "tidepool",
                    host = "halyard",
                    title = "Tide table cache warm-up",
                    context = ConversationContext(tokens = 86_000L, window = 1_000_000L),
                    note = "",
                    stats = null,
                    otherHost = null,
                    goal = "",
                    lines = emptyList(),
                    asking = "",
                    badges = emptyList(),
                    fold = fold,
                    onBack = {},
                    onOpenEpic = {},
                    onHide = {},
                    onShow = {},
                    usage = usage,
                    onUsage = onUsage,
                )
            }
        }
    }

    /** The readout reads the session's list cost, and sits after the context meter and before More, on the meter's line. */
    private fun assertReadoutAfterMeter() {
        composeRule.onNodeWithTag(READOUT).assertTextEquals("usage \$3.77")
        val meter = composeRule.onNodeWithTag("head-context").getBoundsInRoot()
        val readout = composeRule.onNodeWithTag(READOUT).getBoundsInRoot()
        val more = composeRule.onNodeWithTag("head-more").getBoundsInRoot()
        assertTrue("the readout comes after the meter: meter $meter, readout $readout", readout.left >= meter.right)
        assertTrue("the readout comes before More: readout $readout, More $more", readout.right <= more.left)
        assertTrue("the readout sits on the meter's line: meter $meter, readout $readout", readout.top < meter.bottom && meter.top < readout.bottom)
    }

    @Test
    fun theReadoutSitsAfterTheContextMeterWhenTheHeadIsOpen() {
        setHead(HeadFold.OPEN, usage = SESSION_LIST_NANO)

        assertReadoutAfterMeter()
    }

    @Test
    fun theReadoutSitsAfterTheContextMeterWhenTheHeadIsFoldedToItsLine() {
        setHead(HeadFold.READING, usage = SESSION_LIST_NANO)

        assertReadoutAfterMeter()
    }

    @Test
    fun theReadoutSitsAfterTheContextMeterWhenTheHeadIsHidden() {
        setHead(HeadFold.HIDDEN, usage = SESSION_LIST_NANO)

        assertReadoutAfterMeter()
    }

    @Test
    fun noUsageDrawsNoReadoutBesideTheMeter() {
        setHead(HeadFold.OPEN, usage = null)

        composeRule.onAllNodesWithTag("head-context").assertCountEquals(1)
        composeRule.onAllNodesWithTag(READOUT).assertCountEquals(0)
    }

    @Test
    fun tappingTheReadoutCallsOnUsage() {
        var taps = 0
        setHead(HeadFold.OPEN, usage = SESSION_LIST_NANO, onUsage = { taps++ })

        composeRule.onNodeWithTag(READOUT).performClick()

        assertEquals(1, taps)
    }

    // The screens: the reads they make through the fake server, and what the readout and the sheet show of each read.

    /** Answers the pane screen's reads: the pane list, which carries the pane's state at each read, and its conversation. */
    private fun servePane(server: FakeOrreryServer, state: () -> String, orreryId: String = PANE_ORRERY_ID) {
        server.route(PANES_PATH) { Reply.Message(paneList(state(), orreryId), 0L) }
        server.respond(
            PANE_CONVERSATION_PATH,
            ConversationResponse.newBuilder()
                .setOrigin("synthetic origin")
                .setContext(ConversationContextProto.newBuilder().setTokens(400_000L).setWindow(1_000_000L))
                .build(),
        )
        server.respond(PANE_SEEN_PATH, MarkSeenResponse.getDefaultInstance())
        // The pane has no stream, so the screen polls it, as PaneScreenTest serves it.
        server.route(PANE_STREAM_PATH) { Reply.Stream(emptyList(), StreamEnd.Error(Code.NOT_FOUND, "synthetic pane has no stream")) }
    }

    private fun paneList(state: String, orreryId: String): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId(PANE_ID)
                .setHost("example-host")
                .setSession("example-session")
                .setWindow(1)
                .setPane(2)
                .setCwd("/home/user/project")
                .setCommand("claude")
                .setState(state)
                .setSummary("Example summary.")
                .setLabel("example-label")
                .setChanged(FIXTURE_CHANGED)
                .setSeen(FIXTURE_SEEN)
                .setOrreryId(orreryId),
        )
        .build()

    private fun setPaneScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { PaneScreen(PANE_ID, client, clock = { FIXED_NOW_SECONDS }) }
        }
    }

    /** Answers each usage read with the next of [reads], and a read past the last with the last. */
    private fun serveUsage(server: FakeOrreryServer, vararg reads: List<UsageSessionTotalProto>) {
        val answered = AtomicInteger()
        server.route(USAGE_PATH) { _ ->
            val index = minOf(answered.getAndIncrement(), reads.lastIndex)
            Reply.Message(UsageSessionTotalsResponse.newBuilder().addAllTotals(reads[index]).build(), 0L)
        }
    }

    /** The session each usage read asked for, oldest first. */
    private fun usageReads(server: FakeOrreryServer): List<String> =
        server.requests.filter(::isUsageRead).map { UsageSessionTotalsRequest.parseFrom(it.body).session }

    private fun isUsageRead(request: RecordedRequest): Boolean = request.method == "POST" && request.rawPath == USAGE_PATH

    private fun paneListRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == PANES_PATH }

    /** Waits until [count] reads have been asked, and each is answered: nothing is in flight, and the main thread has run it. */
    private fun awaitReadsAnswered(server: FakeOrreryServer, count: Int) {
        composeRule.awaitUntil("$count usage read(s) answered") {
            usageReads(server).size >= count && Quiescence.inFlight() == null
        }
        composeRule.waitForIdle()
    }

    /** Opens the Usage sheet from the readout, then waits until it shows its list total. */
    private fun openSheet() {
        composeRule.onNodeWithTag(READOUT).performClick()
        composeRule.awaitExactlyOne(hasTestTag(SHEET_LIST))
    }

    private fun assertSessionSheet(list: String, requests: String, billed: String) {
        composeRule.onNodeWithTag("usage-head").assertTextEquals("Usage".inLabelCase())
        composeRule.onNodeWithTag(SHEET_LIST).assertTextEquals(list)
        composeRule.onNodeWithTag("usage-requests").assertTextEquals(requests)
        composeRule.onNodeWithTag("usage-billed").assertTextEquals(billed)
    }

    /** The bar of row [index] is filled to [percent] of its width, within a hundredth of a percent. */
    private fun assertShare(index: Int, percent: Double) {
        val bar = composeRule.onNodeWithTag("usage-row:$index").fetchSemanticsNode()
            .config[SemanticsProperties.ProgressBarRangeInfo].current
        assertTrue("row $index fills ${bar * 100}% of its bar, not $percent%", abs(bar * 100 - percent) <= 0.01)
    }

    @Test
    fun theReadoutAndTheSheetShowTheSessionsFiguresAfterATap() {
        FakeOrreryServer().use { server ->
            servePane(server, state = { "done" })
            serveUsage(server, SESSION_TOTALS)
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(READOUT) and hasText("usage \$3.77"))
            composeRule.onAllNodesWithTag("head-context").assertCountEquals(1)
            assertEquals(listOf(PANE_ORRERY_ID), usageReads(server))

            openSheet()

            assertSessionSheet(list = "\$3.77", requests = "at list price · 420 requests", billed = "billed \$2.79")
            composeRule.onNodeWithTag("usage-kind:input").assertTextEquals("input", "1.5M", "\$0.45")
            composeRule.onNodeWithTag("usage-kind:cache write").assertTextEquals("cache write", "2.6M", "\$1.56")
            composeRule.onNodeWithTag("usage-kind:cache read").assertTextEquals("cache read", "44M", "\$1.10")
            composeRule.onNodeWithTag("usage-kind:output").assertTextEquals("output", "220k", "\$0.66")
            composeRule.onNodeWithTag("usage-row:0").assertTextEquals("opus-5-5 main", "billed \$1.90", "\$2.67")
            composeRule.onNodeWithTag("usage-row:1").assertTextEquals("opus-5-5 steelman", "billed \$0.41", "\$0.57")
            composeRule.onNodeWithTag("usage-row:2").assertTextEquals("haiku-5-5 Explore", "billed \$0.28", "\$0.31")
            composeRule.onNodeWithTag("usage-row:3")
                .assertTextEquals("sonnet-5-5 task-reviewer", "billed \$0.20", "\$0.22")
            assertShare(0, 70.82)
            assertShare(1, 15.12)
            assertShare(2, 8.22)
            assertShare(3, 5.84)
        }
    }

    @Test
    fun aSessionWhoseKindCostsAreNotKnownDrawsItsKindsAsTokensAlone() {
        FakeOrreryServer().use { server ->
            servePane(server, state = { "done" })
            serveUsage(server, UNSUMMED_TOTALS)
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(READOUT) and hasText("usage \$0.49"))
            openSheet()

            assertSessionSheet(list = "\$0.49", requests = "at list price · 70 requests", billed = "billed \$0.39")
            composeRule.onNodeWithTag("usage-kind:input").assertTextEquals("input", "500k")
            composeRule.onNodeWithTag("usage-kind:cache write").assertTextEquals("cache write", "150k")
            composeRule.onNodeWithTag("usage-kind:cache read").assertTextEquals("cache read", "2.4M")
            composeRule.onNodeWithTag("usage-kind:output").assertTextEquals("output", "35k")
        }
    }

    @Test
    fun aPaneWithNoTotalsDrawsNoReadout() {
        FakeOrreryServer().use { server ->
            servePane(server, state = { "done" })
            serveUsage(server, emptyList())
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("head-context"))
            awaitReadsAnswered(server, count = 1)

            composeRule.onAllNodesWithTag(READOUT).assertCountEquals(0)
        }
    }

    @Test
    fun aPaneWhoseUsageReadFailsDrawsNoReadout() {
        FakeOrreryServer().use { server ->
            servePane(server, state = { "done" })
            server.fail(USAGE_PATH, Code.UNAVAILABLE, "usage is unavailable")
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("head-context"))
            awaitReadsAnswered(server, count = 1)

            assertEquals(listOf(PANE_ORRERY_ID), usageReads(server))
            composeRule.onAllNodesWithTag(READOUT).assertCountEquals(0)
        }
    }

    @Test
    fun aPaneWithoutAnOrreryIdMakesNoRead() {
        FakeOrreryServer().use { server ->
            servePane(server, state = { "done" }, orreryId = "")
            serveUsage(server, SESSION_TOTALS)
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag("head-context"))
            composeRule.waitForIdle()

            assertEquals(emptyList<String>(), usageReads(server))
            composeRule.onAllNodesWithTag(READOUT).assertCountEquals(0)
        }
    }

    @Test
    fun aPaneReadsOnOpenAndAgainWhenItsTurnEndsAndTheSheetAndReadoutShowTheSecondRead() {
        FakeOrreryServer().use { server ->
            val turnEnded = AtomicBoolean(false)
            servePane(server, state = { if (turnEnded.get()) "done" else "working" })
            serveUsage(server, SESSION_TOTALS, SUBSCRIPTION_TOTALS)
            setPaneScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(READOUT) and hasText("usage \$3.77"))
            assertEquals(listOf(PANE_ORRERY_ID), usageReads(server))
            openSheet()
            assertSessionSheet(list = "\$3.77", requests = "at list price · 420 requests", billed = "billed \$2.79")

            turnEnded.set(true)

            composeRule.awaitExactlyOne(hasTestTag(READOUT) and hasText("usage \$1.40"))
            assertSessionSheet(
                list = "\$1.40",
                requests = "at list price · 190 requests",
                billed = "billed \$0.00 · subscription",
            )
            assertEquals(listOf(PANE_ORRERY_ID, PANE_ORRERY_ID), usageReads(server))

            // The pane is still polled after its turn ends, and no further read follows.
            val polls = paneListRequests(server)
            composeRule.awaitUntil("the pane is polled again") { paneListRequests(server) >= polls + 2 }
            assertEquals(2, usageReads(server).size)
        }
    }

    @Test
    fun aSessionReadsOnOpenAndAgainWhenItsTurnEndsAndTheSheetAndReadoutShowTheSecondRead() {
        FakeOrreryServer().use { server ->
            // The first stream says the session is working and ends cleanly; the screen reconnects, and the next stream
            // says the turn has ended.
            val streams = AtomicInteger()
            server.route(SESSION_STREAM_PATH) { _ ->
                if (streams.getAndIncrement() == 0) {
                    Reply.Stream(listOf(sessionDelta(seq = 1L, state = "working")), StreamEnd.Clean)
                } else {
                    Reply.Stream(listOf(sessionDelta(seq = 2L, state = "done")), StreamEnd.Open)
                }
            }
            serveUsage(server, SESSION_TOTALS, SUBSCRIPTION_TOTALS)
            setSessionScreen(server)

            composeRule.awaitExactlyOne(hasTestTag(READOUT) and hasText("usage \$3.77"))
            assertEquals(listOf(SESSION_ID), usageReads(server))
            openSheet()
            assertSessionSheet(list = "\$3.77", requests = "at list price · 420 requests", billed = "billed \$2.79")

            composeRule.awaitExactlyOne(hasTestTag(READOUT) and hasText("usage \$1.40"))
            assertSessionSheet(
                list = "\$1.40",
                requests = "at list price · 190 requests",
                billed = "billed \$0.00 · subscription",
            )
            awaitReadsAnswered(server, count = 2)
            assertEquals(listOf(SESSION_ID, SESSION_ID), usageReads(server))
        }
    }

    private fun setSessionScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionScreen(SESSION_ID, client, clock = { FIXED_NOW_SECONDS }) }
        }
    }

    private fun sessionDelta(seq: Long, state: String): SessionsWatchConversationResponse {
        val session = ConversationState.newBuilder()
            .setOrigin("synthetic origin")
            .setSession(
                HeadlessSession.newBuilder()
                    .setId(SESSION_ID)
                    .setDir("/home/user/project")
                    .setLabel("example-label")
                    .setState(state)
                    .setChanged(FIXTURE_CHANGED)
                    .setSeen(FIXTURE_SEEN),
            )
            .setContext(ConversationContextProto.newBuilder().setTokens(400_000L).setWindow(1_000_000L))
            .build()
        return SessionsWatchConversationResponse.newBuilder()
            .setTranscript(SESSION_ID)
            .setSeq(seq)
            .setState(session)
            .build()
    }
}
