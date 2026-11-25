package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.LlmAccount
import io.github.joshsymonds.orrery.api.LlmContext
import io.github.joshsymonds.orrery.api.LlmRoutes
import io.github.joshsymonds.orrery.api.LlmSeat
import io.github.joshsymonds.orrery.api.SeatSpendRow
import io.github.joshsymonds.orrery.api.UsageReadingRow
import kotlin.math.roundToInt

/** The limit claims drawn as windows, in column order. 7d_oi is the column headed Fable. */
private val WINDOW_CLAIMS = listOf("5h", "7d", "7d_oi")

/** The claim whose reset the last column shows when the row is neither limited nor stale. */
private const val SEVEN_DAY = "7d"

/** The status of a limit reading whose window refuses requests. */
private const val REJECTED = "rejected"

/** The auth mode of a seat that sends its context's account token upstream. */
private const val AUTH_FORWARD = "forward"

/** The billing of a seat paid as a subscription. */
private const val BILLING_SUBSCRIPTION = "subscription"

/** A read observed over this many seconds before now is stale. */
private const val STALE_AFTER = 3600L

/** A percent from this value is warn. */
private const val WARN_FROM = 60

/** A percent from this value is crit. */
private const val CRIT_FROM = 80

/** A window's level: [OK] below 60 percent, [WARN] from 60, [CRIT] from 80, or when its account is limited. */
enum class UsageLevel { OK, WARN, CRIT }

/**
 * One drawn window: [percent] is the rounded utilization × 100, null when the reading gives none; [level] is its level,
 * null when there is no percent; [resetAt] is when its limit resets, null when the reading names none.
 */
data class UsageWindow(val claim: String, val percent: Int?, val level: UsageLevel?, val resetAt: Long?)

/** What a row offers for Use: [IN_USE] when every context naming an account names it, [USE] when some names another. */
enum class UseOffer { IN_USE, USE }

/** A context Use would move: its [context] id, and the account it uses now, [from]. */
data class MovingContext(val context: String, val from: String)

/** A row's last column: when a limited account lifts, how old a stale read is, or when the 7d window resets. */
sealed interface LastColumn {
    /** [at] is the latest reset among the account's current rejected windows, null when none names one. */
    data class Lift(val at: Long?) : LastColumn

    /** [seconds] since the oldest drawn reading was observed. */
    data class Age(val seconds: Long) : LastColumn

    /** [at] is the 7d window's reset. */
    data class Reset(val at: Long) : LastColumn
}

/**
 * One account's row in the usage band. [windows] are the 5h, 7d and 7d_oi windows in that order; a null window has no
 * current reading. [usedBy] lists the contexts naming the account, in registry order. [liftsAt] is the latest reset
 * among the current rejected windows, and [readAt] the oldest observed drawn reading. [none] says no window is drawn.
 */
data class UsageRow(
    val account: LlmAccount,
    val windows: List<UsageWindow?>,
    val inUse: Boolean,
    val usedBy: List<String>,
    val limited: Boolean,
    val liftsAt: Long?,
    val readAt: Long?,
    val stale: Boolean,
    val last: LastColumn?,
    val none: Boolean,
    val useOffer: UseOffer?,
    val movingContexts: List<MovingContext>,
)

/**
 * The seat spend a band totals: [billed] and [list] summed over all seats, the seats that [billedSeats] billed, and
 * [subscriptionList], the list price of the forward and subscription-billed seats.
 */
data class SpendTotals(
    val billed: Long,
    val list: Long,
    val billedSeats: List<SeatSpendRow>,
    val subscriptionList: Long,
)

/**
 * The usage band's rows, one per account in [accounts] order, read at unix time [now] in seconds. [readings] are the
 * ledger's limit readings and [routes] the route registry the accounts are used in.
 */
fun usageRows(
    accounts: List<LlmAccount>,
    routes: LlmRoutes,
    readings: List<UsageReadingRow>,
    now: Long,
): List<UsageRow> =
    accounts.map { account ->
        usageRow(account, routes.contexts, readings.filter { it.account == account.id }, now)
    }

/**
 * The seat spend totals for [spend]. A seat is subscription when [routes] gives it forward auth or subscription
 * billing; a seat that [routes] names no seat for is not.
 */
fun spendTotals(spend: List<SeatSpendRow>, routes: LlmRoutes): SpendTotals =
    SpendTotals(
        billed = spend.sumOf { it.billedNanoUsd },
        list = spend.sumOf { it.listNanoUsd },
        billedSeats = spend.filter { it.billedNanoUsd > 0 },
        subscriptionList = spend.filter { isSubscription(it.seat, routes.seats) }.sumOf { it.listNanoUsd },
    )

private fun usageRow(
    account: LlmAccount,
    contexts: List<LlmContext>,
    readings: List<UsageReadingRow>,
    now: Long,
): UsageRow {
    val current = readings.filter { it.claim in WINDOW_CLAIMS && isCurrent(it, now) }
    val drawn = WINDOW_CLAIMS.map { claim -> current.firstOrNull { it.claim == claim } }
    val drawnReadings = drawn.filterNotNull()
    val rejected = drawnReadings.filter { it.status == REJECTED }
    val limited = rejected.isNotEmpty()
    val liftsAt = rejected.mapNotNull { it.reset }.maxOrNull()
    val readAt = drawnReadings.minOfOrNull { it.observed }
    val staleAge = readAt?.let { now - it }?.takeIf { it > STALE_AFTER }
    val sevenDayReset = drawnReadings.firstOrNull { it.claim == SEVEN_DAY }?.reset
    val last = when {
        limited -> LastColumn.Lift(liftsAt)
        staleAge != null -> LastColumn.Age(staleAge)
        sevenDayReset != null -> LastColumn.Reset(sevenDayReset)
        else -> null
    }
    val usedBy = contexts.filter { namesAccount(it, account.id) }.map { it.id }
    return UsageRow(
        account = account,
        windows = drawn.map { reading -> reading?.let { windowOf(it, limited) } },
        inUse = usedBy.isNotEmpty(),
        usedBy = usedBy,
        limited = limited,
        liftsAt = liftsAt,
        readAt = readAt,
        stale = staleAge != null,
        last = last,
        none = drawnReadings.isEmpty(),
        useOffer = useOfferOf(account.id, contexts),
        movingContexts = movingContextsOf(account.id, contexts),
    )
}

/** A reading is current when it is not dropped and its reset is unset or later than [now]. */
private fun isCurrent(reading: UsageReadingRow, now: Long): Boolean {
    val reset = reading.reset
    return !reading.dropped && (reset == null || reset > now)
}

/** A drawn window of [reading]. Every window of a limited account is crit. */
private fun windowOf(reading: UsageReadingRow, limited: Boolean): UsageWindow {
    val percent = reading.utilization?.let { (it * 100).roundToInt() }
    val level = if (limited) UsageLevel.CRIT else percent?.let { levelOf(it) }
    return UsageWindow(reading.claim, percent, level, reading.reset)
}

private fun levelOf(percent: Int): UsageLevel = when {
    percent >= CRIT_FROM -> UsageLevel.CRIT
    percent >= WARN_FROM -> UsageLevel.WARN
    else -> UsageLevel.OK
}

/** Whether [context] names [account]. A context with no account names none. */
private fun namesAccount(context: LlmContext, account: String): Boolean =
    context.account.isNotEmpty() && context.account == account

/** Use is offered when some context names an account; it reads "in use" when every such context names [account]. */
private fun useOfferOf(account: String, contexts: List<LlmContext>): UseOffer? {
    val named = contexts.filter { it.account.isNotEmpty() }
    return when {
        named.isEmpty() -> null
        named.all { it.account == account } -> UseOffer.IN_USE
        else -> UseOffer.USE
    }
}

/** The contexts Use on [account] would move: those on another account, in registry order. */
private fun movingContextsOf(account: String, contexts: List<LlmContext>): List<MovingContext> =
    contexts.filter { it.account.isNotEmpty() && it.account != account }.map { MovingContext(it.id, it.account) }

/** Whether the seat [seat] is subscription spend: its auth mode is forward, or its billing is subscription. */
private fun isSubscription(seat: String, seats: List<LlmSeat>): Boolean {
    val found = seats.firstOrNull { it.id == seat } ?: return false
    return found.authMode == AUTH_FORWARD || found.billing == BILLING_SUBSCRIPTION
}
