package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicUsageSession
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.UsageTotal
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UsageFiguresTest {
    /** The fixture's first session: four totals, in the order the server sends them, which is not largest first. */
    private val sessionTotals = listOf(
        UsageTotal(
            model = "sonnet-5-5",
            agentType = "task-reviewer",
            requests = 30L,
            inputTokens = 100_000L,
            outputTokens = 10_000L,
            cacheReadTokens = 4_000_000L,
            cacheWriteTokens = 100_000L,
            billedNanoUsd = 200_000_000L,
            listNanoUsd = 220_000_000L,
            listInputNanoUsd = 30_000_000L,
            listOutputNanoUsd = 30_000_000L,
            listCacheReadNanoUsd = 100_000_000L,
            listCacheWriteNanoUsd = 60_000_000L,
        ),
        UsageTotal(
            model = "opus-5-5",
            agentType = "",
            requests = 240L,
            inputTokens = 900_000L,
            outputTokens = 150_000L,
            cacheReadTokens = 30_000_000L,
            cacheWriteTokens = 2_000_000L,
            billedNanoUsd = 1_900_000_000L,
            listNanoUsd = 2_670_000_000L,
            listInputNanoUsd = 270_000_000L,
            listOutputNanoUsd = 450_000_000L,
            listCacheReadNanoUsd = 750_000_000L,
            listCacheWriteNanoUsd = 1_200_000_000L,
        ),
        UsageTotal(
            model = "haiku-5-5",
            agentType = "Explore",
            requests = 90L,
            inputTokens = 300_000L,
            outputTokens = 20_000L,
            cacheReadTokens = 4_000_000L,
            cacheWriteTokens = 100_000L,
            billedNanoUsd = 280_000_000L,
            listNanoUsd = 310_000_000L,
            listInputNanoUsd = 90_000_000L,
            listOutputNanoUsd = 60_000_000L,
            listCacheReadNanoUsd = 100_000_000L,
            listCacheWriteNanoUsd = 60_000_000L,
        ),
        UsageTotal(
            model = "opus-5-5",
            agentType = "steelman",
            requests = 60L,
            inputTokens = 200_000L,
            outputTokens = 40_000L,
            cacheReadTokens = 6_000_000L,
            cacheWriteTokens = 400_000L,
            billedNanoUsd = 410_000_000L,
            listNanoUsd = 570_000_000L,
            listInputNanoUsd = 60_000_000L,
            listOutputNanoUsd = 120_000_000L,
            listCacheReadNanoUsd = 150_000_000L,
            listCacheWriteNanoUsd = 240_000_000L,
        ),
    )

    /** The fixture's second session, whose four kind costs sum to $4.05 against its $4.90 list cost. */
    private val unsummedTotals = listOf(
        UsageTotal(
            model = "opus-5-5",
            agentType = "",
            requests = 50L,
            inputTokens = 400_000L,
            outputTokens = 30_000L,
            cacheReadTokens = 2_000_000L,
            cacheWriteTokens = 100_000L,
            billedNanoUsd = 300_000_000L,
            listNanoUsd = 400_000_000L,
            listInputNanoUsd = 120_000_000L,
            listOutputNanoUsd = 90_000_000L,
            listCacheReadNanoUsd = 50_000_000L,
            listCacheWriteNanoUsd = 60_000_000L,
        ),
        UsageTotal(
            model = "haiku-5-5",
            agentType = "Explore",
            requests = 20L,
            inputTokens = 100_000L,
            outputTokens = 5_000L,
            cacheReadTokens = 400_000L,
            cacheWriteTokens = 50_000L,
            billedNanoUsd = 90_000_000L,
            listNanoUsd = 90_000_000L,
            listInputNanoUsd = 30_000_000L,
            listOutputNanoUsd = 15_000_000L,
            listCacheReadNanoUsd = 10_000_000L,
            listCacheWriteNanoUsd = 30_000_000L,
        ),
    )

    /** The fixture's subscription session: billed nothing, over $1.40 of list cost and 190 requests. */
    private val subscriptionTotals = listOf(
        UsageTotal(
            model = "opus-5-5",
            agentType = "",
            requests = 150L,
            inputTokens = 600_000L,
            outputTokens = 90_000L,
            cacheReadTokens = 12_000_000L,
            cacheWriteTokens = 800_000L,
            billedNanoUsd = 0L,
            listNanoUsd = 1_230_000_000L,
            listInputNanoUsd = 180_000_000L,
            listOutputNanoUsd = 270_000_000L,
            listCacheReadNanoUsd = 300_000_000L,
            listCacheWriteNanoUsd = 480_000_000L,
        ),
        UsageTotal(
            model = "sonnet-5-5",
            agentType = "Explore",
            requests = 40L,
            inputTokens = 100_000L,
            outputTokens = 10_000L,
            cacheReadTokens = 2_000_000L,
            cacheWriteTokens = 100_000L,
            billedNanoUsd = 0L,
            listNanoUsd = 170_000_000L,
            listInputNanoUsd = 30_000_000L,
            listOutputNanoUsd = 30_000_000L,
            listCacheReadNanoUsd = 50_000_000L,
            listCacheWriteNanoUsd = 60_000_000L,
        ),
    )

    /** The steps of an invented epic: two steps on task T1, a judge of requirement R2, an orphan judge and a planner. */
    private val epicSteps = listOf(
        step("sess-step-t1-impl", task = "T1", agent = "haiku", role = "implementer"),
        step("sess-step-t1-review", task = "T1", agent = "sonnet", role = "reviewer", readOnly = true),
        step("sess-step-judge", task = "evidence-run-r2", agent = "opus", role = "evidence", readOnly = true),
        step("sess-step-orphan", task = "evidence-run-gone", agent = "haiku", role = "evidence", readOnly = true),
        step("sess-step-plan", agent = "opus", role = "planner"),
    )

    /** The requirements of the invented epic: R2, whose evidence run is run-r2. */
    private val epicRequirements = listOf(
        EpicRequirementRow(
            id = "R2",
            text = "The export writes a file",
            evidence = "the export test",
            kind = "check",
            runs = listOf(evidenceRun("run-r2")),
        ),
    )

    /**
     * The invented epic's sessions: its steerer, shared, with two totals sent smallest first; its five steps; and a session
     * no step is. The steerer's spend is not in the epic's total, so the header leaves it out.
     */
    private val epicSessions = listOf(
        EpicUsageSession(
            session = "sess-steer-example",
            shared = true,
            totals = listOf(
                total(
                    model = "haiku-5-5",
                    agentType = "Explore",
                    requests = 5L,
                    cacheWriteTokens = 500L,
                    billed = 1_500_000_000L,
                    list = 2_000_000_000L,
                ),
                total(
                    model = "opus-5-5",
                    requests = 10L,
                    cacheWriteTokens = 1_000L,
                    billed = 2_500_000_000L,
                    list = 3_000_000_000L,
                ),
            ),
        ),
        EpicUsageSession(
            session = "sess-step-t1-impl",
            shared = false,
            totals = listOf(
                total(model = "haiku-5-5", requests = 40L, cacheWriteTokens = 9_000L, billed = 480_000_000L, list = 600_000_000L),
            ),
        ),
        EpicUsageSession(
            session = "sess-step-t1-review",
            shared = false,
            totals = listOf(
                total(model = "sonnet-5-5", requests = 12L, cacheWriteTokens = 3_000L, billed = 180_000_000L, list = 240_000_000L),
            ),
        ),
        EpicUsageSession(
            session = "sess-step-judge",
            shared = false,
            totals = listOf(
                total(model = "opus-5-5", requests = 6L, cacheWriteTokens = 1_500L, billed = 200_000_000L, list = 250_000_000L),
            ),
        ),
        EpicUsageSession(
            session = "sess-step-orphan",
            shared = false,
            totals = listOf(
                total(model = "haiku-5-5", requests = 2L, cacheWriteTokens = 100L, billed = 20_000_000L, list = 20_000_000L),
            ),
        ),
        EpicUsageSession(
            session = "sess-step-plan",
            shared = false,
            totals = listOf(
                total(model = "opus-5-5", requests = 3L, cacheWriteTokens = 400L, billed = 90_000_000L, list = 120_000_000L),
            ),
        ),
        EpicUsageSession(
            session = "sess-unknown-0123456789",
            shared = false,
            totals = listOf(total(model = "sonnet-5-5", requests = 1L, billed = 50_000_000L, list = 60_000_000L)),
        ),
    )

    @Test
    fun dollarsWritesNanoUsdAsDollarsToTwoDecimals() {
        assertEquals("$0.00", dollars(0L))
        assertEquals("$1.00", dollars(1_000_000_000L))
        assertEquals("$1.23", dollars(1_230_000_000L))
        assertEquals("$3.82", dollars(3_820_000_000L))
    }

    @Test
    fun tokensWritesACountUnderAMillionAsWholeThousands() {
        assertEquals("0k", tokens(0L))
        assertEquals("1k", tokens(1_000L))
        assertEquals("999k", tokens(999_000L))
    }

    @Test
    fun tokensWritesACountFromAMillionToUnderTenMillionAsOneDecimalOfMillions() {
        assertEquals("1.0M", tokens(1_000_000L))
        assertEquals("1.5M", tokens(1_500_000L))
        assertEquals("9.9M", tokens(9_900_000L))
    }

    @Test
    fun tokensWritesACountFromTenMillionAsWholeMillions() {
        assertEquals("10M", tokens(10_000_000L))
        assertEquals("48M", tokens(48_000_000L))
    }

    @Test
    fun sessionSumAddsTheFixtureSessionsTotalsAndItsDollarsReadAsTheFixtureSays() {
        val sum = sessionSum(sessionTotals)

        assertEquals(
            SessionSum(
                requests = 420L,
                billed = 2_790_000_000L,
                list = 3_770_000_000L,
                inputTokens = 1_500_000L,
                outputTokens = 220_000L,
                cacheReadTokens = 44_000_000L,
                cacheWriteTokens = 2_600_000L,
            ),
            sum,
        )
        assertEquals("$3.77", dollars(sum.list))
        assertEquals("$2.79", dollars(sum.billed))
    }

    @Test
    fun sessionSumOfASubscriptionSessionBillsNothingAndListsItsCost() {
        val sum = sessionSum(subscriptionTotals)

        assertEquals(190L, sum.requests)
        assertEquals("$0.00", dollars(sum.billed))
        assertEquals("$1.40", dollars(sum.list))
    }

    @Test
    fun kindsViewCarriesEachKindsDollarsWhenEveryTotalSetsAllFourAndTheySumToTheList() {
        val view = kindsView(sessionTotals)

        assertTrue(view.hasDollars)
        assertEquals(
            listOf(
                listOf("input", "1.5M", "$0.45"),
                listOf("output", "220k", "$0.66"),
                listOf("cache read", "44M", "$1.10"),
                listOf("cache write", "2.6M", "$1.56"),
            ),
            view.kinds.map { listOf(it.label, tokens(it.tokens), it.dollars?.let(::dollars) ?: "none") },
        )
    }

    @Test
    fun kindsViewCarriesNoDollarsWhenTheKindCostsDoNotSumToTheList() {
        val view = kindsView(unsummedTotals)

        assertFalse(view.hasDollars)
        assertEquals(
            listOf(
                listOf("input", "500k", "none"),
                listOf("output", "35k", "none"),
                listOf("cache read", "2.4M", "none"),
                listOf("cache write", "150k", "none"),
            ),
            view.kinds.map { listOf(it.label, tokens(it.tokens), it.dollars?.let(::dollars) ?: "none") },
        )
    }

    @Test
    fun kindsViewCarriesNoDollarsWhenOneTotalLeavesAKindCostUnsetEvenThoughTheRestSumToTheList() {
        val view = kindsView(
            listOf(
                total(
                    inputTokens = 10L,
                    outputTokens = 20L,
                    cacheReadTokens = 30L,
                    cacheWriteTokens = 40L,
                    list = 6L,
                    listInput = 1L,
                    listOutput = 2L,
                    listCacheRead = 3L,
                ),
            ),
        )

        assertFalse(view.hasDollars)
        assertEquals(listOf(null, null, null, null), view.kinds.map { it.dollars })
    }

    @Test
    fun modelRowsReadsModelAndAgentWithMainForAnEmptyAgentTypeLargestListFirstTiesByModelThenAgent() {
        val rows = modelRows(
            listOf(
                total(model = "sonnet-5-5", agentType = "", list = 100L, billed = 90L),
                total(model = "opus-5-5", agentType = "steelman", list = 100L, billed = 0L),
                total(model = "opus-5-5", agentType = "", list = 100L, billed = 50L),
                total(model = "haiku-5-5", agentType = "Explore", list = 300L, billed = 10L),
            ),
        )

        assertEquals(
            listOf(
                listOf("haiku-5-5", "Explore", 10L, 300L),
                listOf("opus-5-5", "main", 50L, 100L),
                listOf("opus-5-5", "steelman", 0L, 100L),
                listOf("sonnet-5-5", "main", 90L, 100L),
            ),
            rows.map { listOf(it.model, it.agent, it.billed, it.list) },
        )
    }

    @Test
    fun modelRowsOfTheFixtureSessionSortTheRowsTheServerSentOutOfOrder() {
        val rows = modelRows(sessionTotals)

        assertEquals(
            listOf(
                listOf("opus-5-5", "main", "$1.90", "$2.67"),
                listOf("opus-5-5", "steelman", "$0.41", "$0.57"),
                listOf("haiku-5-5", "Explore", "$0.28", "$0.31"),
                listOf("sonnet-5-5", "task-reviewer", "$0.20", "$0.22"),
            ),
            rows.map { listOf(it.model, it.agent, dollars(it.billed), dollars(it.list)) },
        )
    }

    @Test
    fun modelRowsGiveEachRowItsShareOfTheSessionsListCost() {
        val rows = modelRows(sessionTotals)

        assertEquals(
            listOf(
                2_670_000_000.0 / 3_770_000_000.0,
                570_000_000.0 / 3_770_000_000.0,
                310_000_000.0 / 3_770_000_000.0,
                220_000_000.0 / 3_770_000_000.0,
            ),
            rows.map { it.share },
        )
    }

    @Test
    fun modelRowsGiveEachRowAShareOfZeroWhenNoTotalCostsAnything() {
        val rows = modelRows(listOf(total(model = "opus-5-5", requests = 2L)))

        assertEquals(listOf(0.0), rows.map { it.share })
    }

    @Test
    fun epicUsageFiguresNamesEachSessionByItsStepTheAgentOrItsIdAndTheSharedOneSteering() {
        val figures = epicUsageFigures(epicSessions, epicSteps, epicRequirements)

        assertEquals(
            mapOf(
                "sess-steer-example" to Pair("steering", "shared · not in total"),
                "sess-step-t1-impl" to Pair("T1 implement", ""),
                "sess-step-t1-review" to Pair("T1 review", ""),
                "sess-step-judge" to Pair("R2 judge", ""),
                "sess-step-orphan" to Pair("haiku", ""),
                "sess-step-plan" to Pair("opus", ""),
                "sess-unknown-0123456789" to Pair("sess-unk", ""),
            ),
            figures.rows.associate { it.session to Pair(it.name, it.note) },
        )
    }

    @Test
    fun epicUsageFiguresSumsTheHeaderOverTheSessionsThatAreNotShared() {
        val figures = epicUsageFigures(epicSessions, epicSteps, epicRequirements)

        assertEquals("$1.29", dollars(figures.list))
        assertEquals("$1.02", dollars(figures.billed))
    }

    @Test
    fun epicUsageFiguresListsItsRowsLargestListFirst() {
        val figures = epicUsageFigures(epicSessions, epicSteps, epicRequirements)

        assertEquals(
            listOf(
                "sess-steer-example",
                "sess-step-t1-impl",
                "sess-step-judge",
                "sess-step-t1-review",
                "sess-step-plan",
                "sess-unknown-0123456789",
                "sess-step-orphan",
            ),
            figures.rows.map { it.session },
        )
    }

    @Test
    fun epicUsageFiguresGiveASessionTheModelOfItsLargestTotalAndSumItsRequestsAndCacheWrite() {
        val figures = epicUsageFigures(epicSessions, epicSteps, epicRequirements)

        assertEquals(
            EpicUsageRow(
                session = "sess-steer-example",
                name = "steering",
                note = "shared · not in total",
                model = "opus-5-5",
                requests = 15L,
                cacheWriteTokens = 1_500L,
                list = 5_000_000_000L,
            ),
            figures.rows.first(),
        )
    }

    @Test
    fun epicUsageFiguresGiveASessionWithNoTotalsAnEmptyModelAndNoFigures() {
        val figures = epicUsageFigures(
            listOf(EpicUsageSession(session = "sess-quiet-0123456789", shared = false, totals = emptyList())),
            emptyList(),
            emptyList(),
        )

        assertEquals(
            EpicUsageFigures(
                list = 0L,
                billed = 0L,
                rows = listOf(
                    EpicUsageRow(
                        session = "sess-quiet-0123456789",
                        name = "sess-qui",
                        note = "",
                        model = "",
                        requests = 0L,
                        cacheWriteTokens = 0L,
                        list = 0L,
                    ),
                ),
            ),
            figures,
        )
    }

    /** total builds one total from the fields a test names: every other field is zero, and a kind cost no test names is unset. */
    private fun total(
        model: String = "",
        agentType: String = "",
        requests: Long = 0L,
        inputTokens: Long = 0L,
        outputTokens: Long = 0L,
        cacheReadTokens: Long = 0L,
        cacheWriteTokens: Long = 0L,
        billed: Long = 0L,
        list: Long = 0L,
        listInput: Long? = null,
        listOutput: Long? = null,
        listCacheRead: Long? = null,
        listCacheWrite: Long? = null,
    ): UsageTotal =
        UsageTotal(
            model = model,
            agentType = agentType,
            requests = requests,
            inputTokens = inputTokens,
            outputTokens = outputTokens,
            cacheReadTokens = cacheReadTokens,
            cacheWriteTokens = cacheWriteTokens,
            billedNanoUsd = billed,
            listNanoUsd = list,
            listInputNanoUsd = listInput,
            listOutputNanoUsd = listOutput,
            listCacheReadNanoUsd = listCacheRead,
            listCacheWriteNanoUsd = listCacheWrite,
        )

    /** evidenceRun is an evidence run with the given id and every other field empty or zero. */
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

    /** step is a step with the given id and every other field empty or zero. */
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
}
