package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.selection.selectable
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

// The names the two controls ask under in their owner call.
private const val RELEASE_ANYWAY = "release-anyway"
private const val CLOSE = "close"

// The kinds Close as… picks, as the server names them.
private const val SUPERSEDED = "superseded"
private const val SHIPPED = "shipped"
private const val ABANDONED = "abandoned"

/** The epics Close as… lists, once read: loading while the read is out, failed when it did not answer. */
private sealed interface Listed {
    data object Loading : Listed
    data object Failed : Listed
    data class Loaded(val replacements: List<Replacement>) : Listed
}

/**
 * The two controls of an epic that ended with gaps, Release it anyway and Close as…, one owner call group under its
 * Report. Each control opens its confirm sheet; only a confirm sends, through [onRelease] or [onClose] with the kind
 * and the epic it names. [detail] is the epic as last read, [call] the group's shared state.
 */
@Composable
internal fun EpicGapsControls(
    client: OrreryClient,
    detail: EpicDetail,
    call: OwnerCall,
    onRelease: () -> Unit,
    onClose: (kind: String, by: String) -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        TextButton(
            onClick = { call.ask(RELEASE_ANYWAY) },
            enabled = !call.disabled,
            modifier = Modifier.testTag("report-release-anyway"),
        ) {
            Text("Release it anyway")
        }
        TextButton(
            onClick = { call.ask(CLOSE) },
            enabled = !call.disabled,
            modifier = Modifier.testTag("report-close"),
        ) {
            Text("Close as…")
        }
    }
    if (call.line.isNotEmpty()) {
        Text(
            call.line,
            style = OrreryTokens.Type.small,
            color = if (call.refused) OrreryTokens.Role.bad else OrreryTokens.Ink.three,
            modifier = Modifier
                .padding(horizontal = OrreryTokens.Space.sixteen)
                .testTag("gaps-call-line"),
        )
    }
    if (call.asking == RELEASE_ANYWAY) {
        ReleaseAnywaySheet(slug = detail.epic.slug, call = call, onRelease = onRelease)
    }
    if (call.asking == CLOSE) {
        CloseSheet(client = client, detail = detail, call = call, onClose = onClose)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReleaseAnywaySheet(slug: String, call: OwnerCall, onRelease: () -> Unit) {
    ModalBottomSheet(onDismissRequest = { call.keep() }) {
        Column(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        ) {
            Text(
                "Release $slug anyway?",
                style = OrreryTokens.Type.small,
                modifier = Modifier.testTag("anyway-title"),
            )
            Text(
                "Its gaps stay, and its release steps run.",
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("anyway-line"),
            )
            TextButton(
                onClick = { call.keep() },
                modifier = Modifier.fillMaxWidth().testTag("anyway-keep"),
            ) {
                Text("Keep it")
            }
            TextButton(
                onClick = {
                    if (call.send()) onRelease()
                },
                modifier = Modifier.fillMaxWidth().testTag("anyway-confirm"),
            ) {
                Text("Release it anyway", color = OrreryTokens.Role.bad)
            }
        }
    }
}

/**
 * The Close as… sheet. It reads the epics of this repository when it opens, and holds its pick until it closes: a
 * superseded pick names one of those epics, a shipped or abandoned pick names none.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CloseSheet(client: OrreryClient, detail: EpicDetail, call: OwnerCall, onClose: (String, String) -> Unit) {
    var listed by remember { mutableStateOf<Listed>(Listed.Loading) }
    var kind by remember { mutableStateOf("") }
    var picked by remember { mutableStateOf("") }
    LaunchedEffect(client, detail.epic.id) {
        listed = try {
            Listed.Loaded(replacementsOf(detail, withContext(Dispatchers.IO) { client.epics() }))
        } catch (error: IOException) {
            Listed.Failed
        }
    }
    val replacements = (listed as? Listed.Loaded)?.replacements.orEmpty()
    val supersededOpen = replacements.isNotEmpty()
    val ready = kind == SHIPPED || kind == ABANDONED || (kind == SUPERSEDED && picked.isNotEmpty())

    ModalBottomSheet(onDismissRequest = { call.keep() }) {
        Column(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        ) {
            Text(
                "Close ${detail.epic.slug} as…",
                style = OrreryTokens.Type.small,
                modifier = Modifier.testTag("close-title"),
            )
            Row(
                Modifier
                    .fillMaxWidth()
                    .testTag("close-kind:$SUPERSEDED")
                    .selectable(
                        selected = kind == SUPERSEDED,
                        enabled = supersededOpen,
                        role = Role.RadioButton,
                        onClick = { kind = SUPERSEDED },
                    )
                    .padding(vertical = OrreryTokens.Space.eight),
            ) {
                Text("superseded by", style = OrreryTokens.Type.small)
            }
            replacements.forEach { replacement ->
                Row(
                    Modifier
                        .fillMaxWidth()
                        .padding(start = OrreryTokens.Space.sixteen)
                        .testTag("close-by:${replacement.id}")
                        .selectable(
                            selected = kind == SUPERSEDED && picked == replacement.id,
                            role = Role.RadioButton,
                            onClick = {
                                kind = SUPERSEDED
                                picked = replacement.id
                            },
                        )
                        .padding(vertical = OrreryTokens.Space.eight),
                ) {
                    Text(replacement.slug, style = OrreryTokens.Type.small)
                }
            }
            epicsLineOf(listed)?.let { line ->
                Text(
                    line,
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.three,
                    modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen).testTag("close-epics-line"),
                )
            }
            Row(
                Modifier
                    .fillMaxWidth()
                    .testTag("close-kind:$SHIPPED")
                    .selectable(
                        selected = kind == SHIPPED,
                        role = Role.RadioButton,
                        onClick = {
                            kind = SHIPPED
                            picked = ""
                        },
                    )
                    .padding(vertical = OrreryTokens.Space.eight),
            ) {
                Text("shipped outside orrery", style = OrreryTokens.Type.small)
            }
            Row(
                Modifier
                    .fillMaxWidth()
                    .testTag("close-kind:$ABANDONED")
                    .selectable(
                        selected = kind == ABANDONED,
                        role = Role.RadioButton,
                        onClick = {
                            kind = ABANDONED
                            picked = ""
                        },
                    )
                    .padding(vertical = OrreryTokens.Space.eight),
            ) {
                Text("abandoned", style = OrreryTokens.Type.small)
            }
            TextButton(
                onClick = { call.keep() },
                modifier = Modifier.fillMaxWidth().testTag("close-keep"),
            ) {
                Text("Keep it")
            }
            TextButton(
                onClick = {
                    if (call.send()) onClose(kind, if (kind == SUPERSEDED) picked else "")
                },
                enabled = ready,
                modifier = Modifier.fillMaxWidth().testTag("close-confirm"),
            ) {
                Text(if (kind.isEmpty()) "Close as…" else "Close as $kind", color = OrreryTokens.Role.bad)
            }
        }
    }
}

// epicsLineOf is the line under the superseded choice while its epics are not listed: the words of a read in flight,
// of a read that failed, and of a repository with no other epic. Null once the epics are listed.
private fun epicsLineOf(listed: Listed): String? = when (listed) {
    Listed.Loading -> "loading the epics…"
    Listed.Failed -> "The epics could not be read."
    is Listed.Loaded -> "no other epic in this repository".takeIf { listed.replacements.isEmpty() }
}
