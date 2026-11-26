package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.github.joshsymonds.orrery.api.HostEntry
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.io.IOException
import kotlin.math.roundToInt

/** The wait between the hosts view's Host.Status reads, the same as the pane screen's read waits. */
private const val HOSTS_POLL_MILLIS = 1_000L

/** The level every meter here draws at: the server sends no levels per host, so no meter draws as warn or crit. */
private const val HOST_METER_LEVEL = "ok"

/**
 * The hosts the server's Host.Status lists, in its order, or null when the call fails, so the view keeps the hosts it
 * showed. A status that does not exist (not_found) has no hosts, which is an empty list.
 */
private suspend fun fetchHosts(client: OrreryClient): List<HostEntry>? =
    withContext(Dispatchers.IO) {
        try {
            client.host()?.hosts.orEmpty()
        } catch (e: IOException) {
            null
        }
    }

/**
 * The hosts view, opened from the inbox head's Hosts action under the title "Hosts". It lists each host in the server's
 * order: its name, its load percent and its memory percent, or the word unreachable beside its name with no numbers
 * when the host does not answer. It reads Host.Status when it opens and every [HOSTS_POLL_MILLIS] while it is started,
 * and [onBack] returns to the inbox.
 */
@Composable
fun HostsView(client: OrreryClient, onBack: () -> Unit, modifier: Modifier = Modifier) {
    var hosts by remember(client) { mutableStateOf<List<HostEntry>>(emptyList()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    LaunchedEffect(client, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                fetchHosts(client)?.let { hosts = it }
                delay(HOSTS_POLL_MILLIS)
            }
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(crumbs = listOf(Crumb("Hosts")), onBack = onBack)
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.sixteen),
        ) {
            hosts.forEach { HostRow(it) }
        }
    }
}

/**
 * One host's row, tagged "host-row:<name>": its label, then its load and memory meters when it answers. A host that
 * does not answer shows its label with the unreachable mark and no meters, so it shows no numbers.
 */
@Composable
private fun HostRow(host: HostEntry) {
    Column(
        modifier = Modifier.fillMaxWidth().testTag("host-row:${host.name}"),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        HostLabel(host.name, unreachable = !host.reachable)
        if (host.reachable) {
            HostMeters(
                listOf(
                    HostStat("load", percentOf(host.loadPercent), host.loadPercent, HOST_METER_LEVEL),
                    HostStat("memory", percentOf(host.memoryPercent), host.memoryPercent, HOST_METER_LEVEL),
                ),
            )
        }
    }
}

/** A percent as the whole number a meter's value shows, such as "13%". */
private fun percentOf(percent: Double): String = "${percent.roundToInt()}%"
