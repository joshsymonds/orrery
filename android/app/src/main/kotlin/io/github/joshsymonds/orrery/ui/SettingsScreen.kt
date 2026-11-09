package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.ServerSettings

@Composable
fun SettingsScreen(settings: ServerSettings, onSaved: () -> Unit, modifier: Modifier = Modifier) {
    var address by remember(settings) { mutableStateOf(settings.serverUrl) }
    val context = LocalContext.current
    val fontLicence = remember {
        context.resources.openRawResource(R.raw.orrery_mono_ofl).bufferedReader().use { it.readText() }
    }

    Column(modifier = modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp)) {
        OutlinedTextField(
            value = address,
            onValueChange = { address = it },
            label = { Text("Server address") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            modifier = Modifier.fillMaxWidth().testTag("server-address"),
        )
        Button(
            onClick = {
                settings.saveServerUrl(address)
                onSaved()
            },
        ) {
            Text("Save")
        }
        Text("Font licence: Orrery Mono, a subset of Maple Mono NF")
        Text(fontLicence)
    }
}
