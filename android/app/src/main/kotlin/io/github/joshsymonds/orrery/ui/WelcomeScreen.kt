package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** What Connect says when the typed address does not answer; nothing is changed. */
private const val NO_ANSWER = "No answer from that address. Nothing was changed."

/**
 * The first-run page, shown while no server address is saved or built in. Its title row reads Welcome with no Back, and
 * it asks which server the phone should talk to. Connect asks the typed address for its host, as Save does on Settings,
 * and only once it answers saves the address, starts push setup again, then calls [onConnected]. Otherwise it saves
 * nothing and says so under the field.
 */
@Composable
fun WelcomeScreen(
    onConnected: () -> Unit,
    settings: ServerSettings,
    startPushSetup: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var address by remember { mutableStateOf("") }
    var lastCheck by remember { mutableStateOf<AddressCheck?>(null) }
    val scope = rememberCoroutineScope()

    Column(modifier = modifier.fillMaxSize()) {
        TitleRow(crumbs = listOf(Crumb("Welcome")), onBack = null)
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
        ) {
            Text("Which orrery server should this phone talk to?")
            Field(
                value = address,
                onValueChange = { address = it },
                label = "Address",
                placeholder = "https://host.your-tailnet.ts.net",
                error = lastCheck == AddressCheck.NoAnswer,
                keyboardType = KeyboardType.Uri,
                modifier = Modifier.testTag("server-address"),
            )
            PrimaryButton(
                label = "Connect",
                enabled = address.isNotBlank(),
                onClick = {
                    val typed = address.trim()
                    scope.launch {
                        val check = withContext(Dispatchers.IO) { checkAddress(typed) }
                        lastCheck = check
                        if (check is AddressCheck.Answered) {
                            settings.saveServerUrl(typed)
                            startPushSetup()
                            onConnected()
                        }
                    }
                },
            )
            if (lastCheck == AddressCheck.NoAnswer) {
                InlineNote(NO_ANSWER, NoteTone.Error)
            }
        }
    }
}
