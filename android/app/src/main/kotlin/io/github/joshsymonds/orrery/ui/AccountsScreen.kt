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
import androidx.compose.runtime.rememberCoroutineScope
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

/** The loaded accounts after [change]; a page that has not loaded is left as it is. */
private fun AccountsLoad.changed(change: (LlmAccounts) -> LlmAccounts): AccountsLoad =
    if (this is AccountsLoad.Loaded) AccountsLoad.Loaded(change(accounts)) else this

/**
 * The accounts and routes page: the Claude accounts the server stores, then the contexts that choose seats, then the
 * seats by how they authenticate. The read runs off the main thread. A failed read says the server did not answer,
 * with a Retry that reads again. Back in the title row calls [onBack]. An account's ⋯ opens its Rename and Remove, a
 * context's › picks the account it sends with, and the Seats row's › lists every seat. The title row's + opens the add
 * sheet; a stored or unanswered Add re-reads the page, and an unanswered one says so above the accounts.
 */
@Composable
fun AccountsScreen(
    client: OrreryClient,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var load by remember { mutableStateOf<AccountsLoad>(AccountsLoad.Loading) }
    var generation by remember { mutableIntStateOf(0) }
    val scope = rememberCoroutineScope()
    val acts = remember(client) {
        AccountActs(
            client = client,
            scope = scope,
            onRenamed = { renamed ->
                load = load.changed { all ->
                    all.copy(accounts = all.accounts.map { if (it.id == renamed.id) renamed else it })
                }
            },
            onRemoved = { id ->
                load = load.changed { all -> all.copy(accounts = all.accounts.filterNot { it.id == id }) }
            },
            onRouted = { routes -> load = load.changed { all -> all.copy(routes = routes) } },
            onNoAnswer = { generation++ },
        )
    }
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
            acts.notice?.let { InlineNote(it, NoteTone.Error) }
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
                is AccountsLoad.Loaded -> {
                    AccountsContent(
                        loaded = current.accounts,
                        onMenu = { acts.open(ActSheet.Menu(it)) },
                        onPick = { acts.open(ActSheet.Pick(it)) },
                        onSeats = { acts.open(ActSheet.Seats) },
                    )
                    AccountActsSheet(acts, current.accounts)
                }
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
private fun AccountsContent(
    loaded: LlmAccounts,
    onMenu: (LlmAccount) -> Unit,
    onPick: (LlmContext) -> Unit,
    onSeats: () -> Unit,
) {
    val contexts = loaded.routes.contexts
    val accountsById = loaded.accounts.associateBy { it.id }
    SectionHead("Accounts")
    loaded.accounts.forEach { AccountRow(it, contexts, onMenu) }
    SectionHead("Contexts")
    contexts.forEach { ContextRow(it, accountsById, onPick) }
    SectionHead("Seats")
    SeatsRow(loaded.routes.seats, onSeats)
}

/** An account: its name, then its plan, its fingerprint and the contexts that use it, each left out when empty. */
@Composable
private fun AccountRow(account: LlmAccount, contexts: List<LlmContext>, onMenu: (LlmAccount) -> Unit) {
    val usedBy = contexts.filter { it.account == account.id }.joinToString(", ") { it.id }
    val fingerprint = "#${account.fingerprint.take(FINGERPRINT_SHOWN)}"
    ListItem(
        title = account.name,
        supporting = listOf(account.plan, fingerprint, usedBy).filter { it.isNotEmpty() }.joinToString(" · "),
        modifier = Modifier.testTag("account-${account.id}"),
        end = {
            TextButton(
                onClick = { onMenu(account) },
                modifier = Modifier
                    .testTag("account-more-${account.id}")
                    .semantics { contentDescription = "More for ${account.name}" },
            ) {
                Text("⋯")
            }
        },
    )
}

/**
 * A context: its id, then the account its forward seats send, named, or the session's own login when it names none,
 * then its default seat. Its › picks the account.
 */
@Composable
private fun ContextRow(context: LlmContext, accountsById: Map<String, LlmAccount>, onPick: (LlmContext) -> Unit) {
    val sender = when (context.account) {
        "" -> "the session's own login"
        else -> accountsById[context.account]?.name ?: context.account
    }
    ListItem(
        title = context.id,
        supporting = "$sender · default seat ${context.defaultSeat}",
        modifier = Modifier.testTag("context-${context.id}"),
        onClick = { onPick(context) },
        end = { OpensSheet() },
    )
}

/**
 * The seats as one count, then how many seats authenticate each way: "forward 2 · inject 2 · sigv4 1". Its › lists
 * every seat.
 */
@Composable
private fun SeatsRow(seats: List<LlmSeat>, onOpen: () -> Unit) {
    val byKind = seats.groupingBy { it.authMode }.eachCount().toSortedMap()
    ListItem(
        title = if (seats.size == 1) "1 seat" else "${seats.size} seats",
        supporting = byKind.map { (kind, count) -> "$kind $count" }.joinToString(" · "),
        modifier = Modifier.testTag("seats-row"),
        onClick = onOpen,
        end = { OpensSheet() },
    )
}

/** The › that ends a row which opens a sheet. */
@Composable
private fun OpensSheet() {
    Text("›", color = OrreryTokens.Ink.two, style = OrreryTokens.Type.title)
}
