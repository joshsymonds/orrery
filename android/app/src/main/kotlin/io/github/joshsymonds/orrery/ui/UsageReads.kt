package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.github.joshsymonds.orrery.api.LlmAccounts
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.SeatSpendRow
import io.github.joshsymonds.orrery.api.UsageReadingRow
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.io.IOException

/** How long the usage reads wait between reads while the inbox is started, in milliseconds. */
private const val READ_EVERY_MILLIS = 60_000L

/** The least span between two Spend reads, in seconds: a read is not made within this long after the last one. */
private const val SPEND_EVERY_SECONDS = 60L

/** The span a Spend read covers, ending at the read's time, in seconds: seven days. */
private const val SPEND_SPAN_SECONDS = 7L * 24 * 60 * 60

/**
 * The usage the inbox shows, as the phone holds it: [accounts] (the accounts and the route registry), [readings] and
 * [spend], each null until a read of it has answered. A read that does not answer keeps the data of the last one that
 * did, and [failed] says whether the last read of any of them did not. [now] is the time in unix seconds.
 */
class UsageReads(private val client: OrreryClient, private val now: () -> Long) {
    var accounts: LlmAccounts? by mutableStateOf(null)
        private set

    var readings: List<UsageReadingRow>? by mutableStateOf(null)
        private set

    var spend: List<SeatSpendRow>? by mutableStateOf(null)
        private set

    var failed: Boolean by mutableStateOf(false)
        private set

    /** When the last Spend read was made, in unix seconds, or null before the first. A read that failed counts as made. */
    private var spendReadAt: Long? = null

    /** Whether the last Spend read did not answer. */
    private var spendFailed = false

    /**
     * Reads the accounts and the usage readings, and the spend over the seven days before now unless a Spend read was
     * made within the last [SPEND_EVERY_SECONDS]. Each read stands on its own: one that fails does not stop the others.
     */
    suspend fun read() {
        var accountsFailed = false
        var readingsFailed = false
        try {
            accounts = withContext(Dispatchers.IO) { client.llmAccounts() }
        } catch (error: IOException) {
            accountsFailed = true
        }
        try {
            readings = withContext(Dispatchers.IO) { client.usageReadings() }
        } catch (error: IOException) {
            readingsFailed = true
        }
        val at = now()
        if (!spendMadeWithin(at)) {
            spendReadAt = at
            try {
                spend = withContext(Dispatchers.IO) { client.usageSpend(from = at - SPEND_SPAN_SECONDS, to = at) }
                spendFailed = false
            } catch (error: IOException) {
                spendFailed = true
            }
        }
        failed = accountsFailed || readingsFailed || spendFailed
    }

    /** Whether a Spend read was made less than [SPEND_EVERY_SECONDS] before [at]. A clock set back is not a read made within it. */
    private fun spendMadeWithin(at: Long): Boolean {
        val madeAt = spendReadAt ?: return false
        val age = at - madeAt
        return age in 0 until SPEND_EVERY_SECONDS
    }
}

/**
 * The usage reads of [client], read when the composition's lifecycle starts and every [READ_EVERY_MILLIS] while it is
 * started. Nothing is read while it is stopped.
 */
@Composable
fun rememberUsageReads(client: OrreryClient): UsageReads {
    val reads = remember(client) { UsageReads(client, now = { System.currentTimeMillis() / 1000 }) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(reads, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                reads.read()
                delay(READ_EVERY_MILLIS)
            }
        }
    }
    return reads
}
