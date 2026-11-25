package io.github.joshsymonds.orrery.ui

// The figures every usage view shares, as web/src/usagefigures.ts draws them: a session's totals summed, its four token
// kinds, its model-and-agent rows, and the formats its dollars and tokens are written in. An epic's usage is its sessions
// as rows, each named as the epic names it. Each figure is a pure function of the totals, which are the rows the usage
// reads answer.

import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicUsageSession
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.UsageTotal
import java.math.BigDecimal
import java.math.RoundingMode

/** The step role an evidence run's judge step plays, as the epic proto names it. */
private const val EVIDENCE_ROLE = "evidence"

/** The task label an evidence run's judge step carries: the run's id after this prefix. */
private const val EVIDENCE_TASK_PREFIX = "evidence-"

/** The name of the epic's steerer, the session that is shared rather than counted in the epic's total. */
private const val STEERING = "steering"

/** The note a shared session's row carries, to say its spend is not in the epic's total. */
private const val SHARED_NOTE = "shared · not in total"

/** The number of characters of a session id a row falls back to when nothing else names the session. */
private const val ID_PREFIX_LENGTH = 8

/**
 * dollars writes a cost in nano US dollars as dollars to two decimals: 3820000000 is $3.82. It rounds the exact value of
 * the cost in dollars, half up, as JavaScript's toFixed does.
 */
fun dollars(nano: Long): String =
    "\$" + BigDecimal(nano.toDouble() / 1e9).setScale(2, RoundingMode.HALF_UP).toPlainString()

/**
 * tokens writes a count of tokens short: thousands under a million, one decimal of millions under ten million, and whole
 * millions from ten million: 999000 is 999k, 1500000 is 1.5M, and 48000000 is 48M.
 */
fun tokens(n: Long): String =
    when {
        n < 1_000_000L -> "${Math.round(n / 1000.0)}k"
        n < 10_000_000L -> BigDecimal(n / 1e6).setScale(1, RoundingMode.HALF_UP).toPlainString() + "M"
        else -> "${Math.round(n / 1e6)}M"
    }

/** The totals of a session: its requests, its billed and list costs in nano US dollars, and its four token kinds. */
data class SessionSum(
    val requests: Long,
    val billed: Long,
    val list: Long,
    val inputTokens: Long,
    val outputTokens: Long,
    val cacheReadTokens: Long,
    val cacheWriteTokens: Long,
)

/** The totals of one session added up. */
fun sessionSum(totals: List<UsageTotal>): SessionSum =
    SessionSum(
        requests = totals.sumOf { it.requests },
        billed = totals.sumOf { it.billedNanoUsd },
        list = totals.sumOf { it.listNanoUsd },
        inputTokens = totals.sumOf { it.inputTokens },
        outputTokens = totals.sumOf { it.outputTokens },
        cacheReadTokens = totals.sumOf { it.cacheReadTokens },
        cacheWriteTokens = totals.sumOf { it.cacheWriteTokens },
    )

/** One of a session's four token kinds. */
enum class KindName { INPUT, OUTPUT, CACHE_READ, CACHE_WRITE }

/** One token kind of a session: its tokens, and its list cost in nano US dollars when those are known, else null. */
data class KindFigure(val kind: KindName, val label: String, val tokens: Long, val dollars: Long?)

/**
 * A session's four token kinds, in the order input, output, cache read and cache write. [hasDollars] is true only when
 * every total sets all four kind costs and they sum to the session's list cost; then each kind carries its dollars, and
 * otherwise every kind's dollars are null.
 */
data class KindsView(val hasDollars: Boolean, val kinds: List<KindFigure>)

/** One token kind as a total gives it: its label, its tokens, and its list cost, which is null when the total does not set it. */
private class KindField(
    val kind: KindName,
    val label: String,
    val tokens: (UsageTotal) -> Long,
    val list: (UsageTotal) -> Long?,
)

private val KIND_FIELDS = listOf(
    KindField(KindName.INPUT, "input", { it.inputTokens }, { it.listInputNanoUsd }),
    KindField(KindName.OUTPUT, "output", { it.outputTokens }, { it.listOutputNanoUsd }),
    KindField(KindName.CACHE_READ, "cache read", { it.cacheReadTokens }, { it.listCacheReadNanoUsd }),
    KindField(KindName.CACHE_WRITE, "cache write", { it.cacheWriteTokens }, { it.listCacheWriteNanoUsd }),
)

/** A session's four token kinds summed, each with its dollars when the kind costs are all known and sum to the list cost. */
fun kindsView(totals: List<UsageTotal>): KindsView {
    val allKnown = totals.all { total -> KIND_FIELDS.all { it.list(total) != null } }
    val kindCostSum = totals.sumOf { total -> KIND_FIELDS.sumOf { it.list(total) ?: 0L } }
    val listSum = totals.sumOf { it.listNanoUsd }
    val hasDollars = allKnown && kindCostSum == listSum
    return KindsView(
        hasDollars = hasDollars,
        kinds = KIND_FIELDS.map { field ->
            KindFigure(
                kind = field.kind,
                label = field.label,
                tokens = totals.sumOf { field.tokens(it) },
                dollars = if (hasDollars) totals.sumOf { field.list(it) ?: 0L } else null,
            )
        },
    )
}

/**
 * One total of a session as a row: its model, its agent type, which reads "main" when the total has none, its billed and
 * list costs in nano US dollars, and its share of the session's list cost, from 0 to 1.
 */
data class ModelRow(val model: String, val agent: String, val billed: Long, val list: Long, val share: Double)

/** One row per total, largest list cost first, ties by model and then by agent. */
fun modelRows(totals: List<UsageTotal>): List<ModelRow> {
    val listSum = sessionSum(totals).list
    return totals
        .map { total ->
            ModelRow(
                model = total.model,
                agent = total.agentType.ifEmpty { "main" },
                billed = total.billedNanoUsd,
                list = total.listNanoUsd,
                share = if (listSum == 0L) 0.0 else total.listNanoUsd.toDouble() / listSum.toDouble(),
            )
        }
        .sortedWith(compareByDescending<ModelRow> { it.list }.thenBy { it.model }.thenBy { it.agent })
}

/**
 * One session of an epic as a row: its [name], its [note] (empty for a step), the [model] of its largest total, and its
 * [requests], [cacheWriteTokens] and [list] cost in nano US dollars summed over its totals.
 */
data class EpicUsageRow(
    val session: String,
    val name: String,
    val note: String,
    val model: String,
    val requests: Long,
    val cacheWriteTokens: Long,
    val list: Long,
)

/**
 * An epic's usage: its header's [list] and [billed] costs in nano US dollars, summed over the sessions that are not
 * shared, and its [rows], largest list cost first.
 */
data class EpicUsageFigures(val list: Long, val billed: Long, val rows: List<EpicUsageRow>)

/**
 * An epic's usage: its sessions as rows, each named, and its header's sums. [steps] and [requirements] are the epic's
 * steps and requirements as the epic's detail holds them, and name the sessions that are steps.
 */
fun epicUsageFigures(
    sessions: List<EpicUsageSession>,
    steps: List<StepRow>,
    requirements: List<EpicRequirementRow>,
): EpicUsageFigures {
    val stepsBySession = steps.associateBy { it.id }
    val requirementOfLabel = requirements
        .flatMap { requirement -> requirement.runs.map { "$EVIDENCE_TASK_PREFIX${it.id}" to requirement.id } }
        .toMap()
    val rows = sessions
        .map { session ->
            val sum = sessionSum(session.totals)
            EpicUsageRow(
                session = session.session,
                name = nameOf(session, stepsBySession[session.session], requirementOfLabel),
                note = if (session.shared) SHARED_NOTE else "",
                model = modelRows(session.totals).firstOrNull()?.model.orEmpty(),
                requests = sum.requests,
                cacheWriteTokens = sum.cacheWriteTokens,
                list = sum.list,
            )
        }
        .sortedWith(compareByDescending<EpicUsageRow> { it.list }.thenBy { it.name }.thenBy { it.session })
    val counted = sessions.filterNot { it.shared }.map { sessionSum(it.totals) }
    return EpicUsageFigures(
        list = counted.sumOf { it.list },
        billed = counted.sumOf { it.billed },
        rows = rows,
    )
}

/**
 * The name of one session of an epic: steering for the shared session; else the requirement an evidence step's judge
 * checks, as "<requirement> judge"; else the task a writing or reading step works on, as "<task> implement" or "<task>
 * review"; else the step's agent; else the first eight characters of the session id.
 */
private fun nameOf(session: EpicUsageSession, step: StepRow?, requirementOfLabel: Map<String, String>): String {
    if (session.shared) return STEERING
    if (step != null) {
        if (step.role == EVIDENCE_ROLE) {
            requirementOfLabel[step.task]?.let { return "$it judge" }
        } else if (step.task.isNotEmpty()) {
            return "${step.task} ${if (step.readOnly) "review" else "implement"}"
        }
        if (step.agent.isNotEmpty()) return step.agent
    }
    return session.session.take(ID_PREFIX_LENGTH)
}
