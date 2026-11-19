package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import io.github.joshsymonds.orrery.BuildConfig
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The line settings shows while there is no server address to talk to. */
internal const val SERVER_ASK_TAG = "server-ask"

/**
 * Settings, in three sections: Server, with the address and its Save, then Notifications, then About, with the font
 * licence and the version. Back in the title row calls [onBack]. Save stores the typed address, then calls [onSaved].
 * The font licence opens on a view of its own with its title row, and that view's Back, or the system's, returns here.
 */
@Composable
fun SettingsScreen(settings: ServerSettings, onBack: () -> Unit, onSaved: () -> Unit, modifier: Modifier = Modifier) {
    var address by remember(settings) { mutableStateOf(settings.serverUrl) }
    var licence by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val fontLicence = remember {
        context.resources.openRawResource(R.raw.orrery_mono_ofl).bufferedReader().use { it.readText() }
    }

    Column(modifier = modifier.fillMaxSize()) {
        if (licence) {
            BackHandler { licence = false }
            TitleRow(crumbs = listOf(Crumb("Font licence")), onBack = { licence = false })
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text(fontLicence, color = OrreryTokens.Ink.two, style = OrreryTokens.Type.small)
            }
        } else {
            TitleRow(crumbs = listOf(Crumb("Settings")), onBack = onBack)
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve),
            ) {
                if (!settings.hasServer) {
                    Text("Which orreryd server should Orrery talk to?", modifier = Modifier.testTag(SERVER_ASK_TAG))
                }
                SectionHead("Server")
                Field(
                    value = address,
                    onValueChange = { address = it },
                    label = "Address",
                    keyboardType = KeyboardType.Uri,
                    modifier = Modifier.testTag("server-address"),
                )
                Button(
                    onClick = {
                        settings.saveServerUrl(address)
                        onSaved()
                    },
                    colors = ButtonDefaults.buttonColors(
                        containerColor = OrreryTokens.Role.primary,
                        contentColor = OrreryTokens.Ink.onAccent,
                    ),
                ) {
                    Text("Save")
                }
                SectionHead("Notifications")
                SectionHead("About")
                ListItem(
                    title = "Font licence",
                    supporting = "Orrery Mono, a subset of Maple Mono NF",
                    onClick = { licence = true },
                    end = { Text("›", color = OrreryTokens.Ink.three) },
                )
                ListItem(title = "Version", supporting = BuildConfig.VERSION_NAME)
            }
        }
    }
}
