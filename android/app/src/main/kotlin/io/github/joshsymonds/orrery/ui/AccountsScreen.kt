package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.LlmAccount
import io.github.joshsymonds.orrery.api.LlmAccounts
import io.github.joshsymonds.orrery.api.LlmContext
import io.github.joshsymonds.orrery.api.LlmSeat
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

/** How many characters of an account's fingerprint the page shows: enough to tell the token apart, never the token. */
private const val FINGERPRINT_SHOWN = 6

/** The title row's + that opens the add sheet. */
private const val ADD_TAG = "title-add"

/** The note the page shows when an Add got no answer: the write may or may not have landed; the re-read says which. */
private const val UNANSWERED = "Could not tell whether that landed: no answer came. This is what stands now."

private sealed interface AccountsLoad {
    data object Loading : AccountsLoad
    data object Failed : AccountsLoad
    data class Loaded(val accounts: LlmAccounts) : AccountsLoad
}

/**
 * The accounts and routes page: the Claude accounts the server stores, then the contexts that choose seats, then the
 * seats by how they authenticate. The read runs off the main thread. A failed read says the server did not answer,
 * with a Retry that reads again. Back in the title row calls [onBack]. The title row's + opens the add sheet; a stored
 * or unanswered Add re-reads the page, and an unanswered one says so above the accounts.
 */
@Composable
fun AccountsScreen(
    client: OrreryClient,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var load by remember { mutableStateOf<AccountsLoad>(AccountsLoad.Loading) }
    var generation by remember { mutableIntStateOf(0) }
    var adding by remember { mutableStateOf(false) }
    var unanswered by remember { mutableStateOf(false) }

    LaunchedEffect(client, generation) {
        load = AccountsLoad.Loading
        load = try {
            AccountsLoad.Loaded(withContext(Dispatchers.IO) { client.llmAccounts() })
        } catch (error: IOException) {
            AccountsLoad.Failed
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(
            crumbs = listOf(Crumb("Accounts and routes")),
            onBack = onBack,
            actions = {
                TextButton(
                    onClick = {
                        unanswered = false
                        adding = true
                    },
                    modifier = Modifier.testTag(ADD_TAG).semantics { contentDescription = "Add account" },
                ) {
                    Text("+")
                }
            },
        )
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
        ) {
            if (unanswered) {
                InlineNote(UNANSWERED, NoteTone.Error, Modifier.testTag("accounts-unanswered"))
            }
            when (val current = load) {
                AccountsLoad.Loading -> Text("Loading...")
                AccountsLoad.Failed -> Column {
                    InlineNote("The server did not answer.", NoteTone.Error)
                    TextButton(onClick = { generation++ }) {
                        Text("Retry")
                    }
                }
                is AccountsLoad.Loaded -> AccountsContent(current.accounts)
            }
        }
    }

    if (adding) {
        AccountsAddSheet(
            client = client,
            onAdded = {
                adding = false
                generation++
            },
            onUnanswered = {
                adding = false
                unanswered = true
                generation++
            },
            onDismiss = { adding = false },
        )
    }
}

@Composable
private fun AccountsContent(loaded: LlmAccounts) {
    val contexts = loaded.routes.contexts
    val accountsById = loaded.accounts.associateBy { it.id }
    SectionHead("Accounts")
    loaded.accounts.forEach { AccountRow(it, contexts) }
    SectionHead("Contexts")
    contexts.forEach { ContextRow(it, accountsById) }
    SectionHead("Seats")
    SeatsRow(loaded.routes.seats)
}

/** An account: its name, then its plan, its fingerprint and the contexts that use it, each left out when empty. */
@Composable
private fun AccountRow(account: LlmAccount, contexts: List<LlmContext>) {
    val usedBy = contexts.filter { it.account == account.id }.joinToString(", ") { it.id }
    val fingerprint = "#${account.fingerprint.take(FINGERPRINT_SHOWN)}"
    ListItem(
        title = account.name,
        supporting = listOf(account.plan, fingerprint, usedBy).filter { it.isNotEmpty() }.joinToString(" · "),
    )
}

/**
 * A context: its id, then the account its forward seats send, named, or the session's own login when it names none,
 * then its default seat.
 */
@Composable
private fun ContextRow(context: LlmContext, accountsById: Map<String, LlmAccount>) {
    val sender = when (context.account) {
        "" -> "the session's own login"
        else -> accountsById[context.account]?.name ?: context.account
    }
    ListItem(title = context.id, supporting = "$sender · default seat ${context.defaultSeat}")
}

/** The seats as one count, then how many seats authenticate each way: "forward 2 · inject 2 · sigv4 1". */
@Composable
private fun SeatsRow(seats: List<LlmSeat>) {
    val byKind = seats.groupingBy { it.authMode }.eachCount().toSortedMap()
    ListItem(
        title = if (seats.size == 1) "1 seat" else "${seats.size} seats",
        supporting = byKind.map { (kind, count) -> "$kind $count" }.joinToString(" · "),
    )
}
