package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONException

private const val TAG = "PaneScreen"
private const val POLL_INTERVAL_MILLIS = 1_000L

private class Snapshot(val text: String, val pane: Pane?)

/** One tick: the screen text and the pane's entry, or null when either call fails. */
private suspend fun fetchSnapshot(client: OrreryClient, paneId: String): Snapshot? =
    withContext(Dispatchers.IO) {
        try {
            Snapshot(client.screen(paneId), client.panes().firstOrNull { it.id == paneId })
        } catch (e: IOException) {
            null
        } catch (e: JSONException) {
            null
        }
    }

/**
 * A pane's visible text, refreshed every second while the screen is at least
 * started. Opening it marks the pane seen once. The question card and the
 * compose box and quick keys sit below the text.
 */
@Composable
fun PaneScreen(paneId: String, client: OrreryClient, modifier: Modifier = Modifier) {
    var text by remember(paneId) { mutableStateOf("") }
    var pane by remember(paneId) { mutableStateOf<Pane?>(null) }
    var refreshFailed by remember(paneId) { mutableStateOf(false) }
    val refreshRequests = remember(paneId) { Channel<Unit>(Channel.CONFLATED) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()

    LaunchedEffect(paneId) {
        withContext(Dispatchers.IO) {
            try {
                client.markSeen(paneId)
            } catch (e: IOException) {
                Log.w(TAG, "could not mark $paneId seen", e)
            }
        }
    }

    DisposableEffect(paneId, lifecycle) {
        var poller: Job? = null
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> {
                    poller = scope.launch {
                        while (true) {
                            val snapshot = fetchSnapshot(client, paneId)
                            if (snapshot == null) {
                                refreshFailed = true
                            } else {
                                text = snapshot.text
                                pane = snapshot.pane
                                refreshFailed = false
                            }
                            withTimeoutOrNull(POLL_INTERVAL_MILLIS) { refreshRequests.receive() }
                        }
                    }
                }
                Lifecycle.Event.ON_STOP -> {
                    poller?.cancel()
                    poller = null
                }
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            poller?.cancel()
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        if (refreshFailed) {
            Text("could not refresh")
        }
        TerminalView(text, modifier = Modifier.weight(1f).fillMaxWidth())
        val current = pane
        if (current != null && current.question.isNotEmpty()) {
            QuestionCard(current, client, onRefresh = { refreshRequests.trySend(Unit) })
        }
        ComposeBox(paneId, client)
        QuickKeys(paneId, client)
    }
}
