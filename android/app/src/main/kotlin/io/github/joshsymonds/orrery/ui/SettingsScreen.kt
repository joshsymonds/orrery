package io.github.joshsymonds.orrery.ui

import android.Manifest
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.provider.Settings
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
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import io.github.joshsymonds.orrery.BuildConfig
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.ServerSettings
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.push.PushNotifier
import io.github.joshsymonds.orrery.push.PushPreferences
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl.Companion.toHttpUrl

/** What Save says when the typed address does not answer; nothing is changed. */
private const val NO_ANSWER = "No answer from that address. Nothing was changed."

/** What asking an address for its host found: an answer, with the host to name, or no answer at all. */
internal sealed interface AddressCheck {
    data class Answered(val host: String) : AddressCheck

    data object NoAnswer : AddressCheck
}

/**
 * Asks [address] for its host. A not_found answer counts as an answer, named by the address's first host label, as
 * the host has no name of its own to give. Any failure to get an answer is [AddressCheck.NoAnswer]. Blocking: callers
 * run it on Dispatchers.IO.
 */
internal fun checkAddress(address: String): AddressCheck =
    try {
        val host = OrreryClient(address).host()?.host.orEmpty()
        AddressCheck.Answered(host.ifEmpty { address.toHttpUrl().host.substringBefore('.') })
    } catch (error: IOException) {
        AddressCheck.NoAnswer
    }

/**
 * Settings, in three sections: Server, with the address and its Save, then Notifications, then About, with the font
 * licence and the version. Back in the title row calls [onBack]. Save asks the typed address for its host first, and
 * only once it answers stores the address, says so, calls [startPushSetup], then [onSaved]; otherwise it changes
 * nothing and says so under the field. The font licence opens on a view of its own with its title row, and that view's
 * Back, or the system's, returns here.
 */
@Composable
fun SettingsScreen(
    settings: ServerSettings,
    onBack: () -> Unit,
    onSaved: () -> Unit,
    startPushSetup: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var address by remember(settings) { mutableStateOf(settings.serverUrl) }
    var licence by remember { mutableStateOf(false) }
    var lastCheck by remember { mutableStateOf<AddressCheck?>(null) }
    val scope = rememberCoroutineScope()
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
                SectionHead("Server")
                Field(
                    value = address,
                    onValueChange = { address = it },
                    label = "Address",
                    error = lastCheck == AddressCheck.NoAnswer,
                    keyboardType = KeyboardType.Uri,
                    modifier = Modifier.testTag("server-address"),
                )
                Button(
                    enabled = address.isNotBlank(),
                    onClick = {
                        val typed = address.trim()
                        scope.launch {
                            val check = withContext(Dispatchers.IO) { checkAddress(typed) }
                            lastCheck = check
                            if (check is AddressCheck.Answered) {
                                settings.saveServerUrl(typed)
                                startPushSetup()
                                onSaved()
                            }
                        }
                    },
                    colors = ButtonDefaults.buttonColors(
                        containerColor = OrreryTokens.Role.primary,
                        contentColor = OrreryTokens.Ink.onAccent,
                    ),
                ) {
                    Text("Save")
                }
                when (val check = lastCheck) {
                    is AddressCheck.Answered -> InlineNote("Connected to ${check.host}.", NoteTone.Ok)
                    AddressCheck.NoAnswer -> InlineNote(NO_ANSWER, NoteTone.Error)
                    null -> Unit
                }
                SectionHead("Notifications")
                PostPermissionNote()
                ChannelRow(PushNotifier.CHANNEL_ID, PushNotifier.QUESTIONS_CHANNEL_NAME)
                ChannelRow(PushNotifier.SILENT_CHANNEL_ID, PushNotifier.REST_CHANNEL_NAME)
                PushStatus(settings)
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

/** Whether Android lets Orrery post notifications at all. */
private fun postsAllowed(context: Context): Boolean =
    context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

/** When Android does not let Orrery post, says so and opens Android's notification settings for the app. */
@Composable
private fun PostPermissionNote() {
    val context = LocalContext.current
    if (postsAllowed(context)) return
    InlineNote("Android does not let Orrery post notifications.", NoteTone.Error)
    TextButton(
        onClick = {
            context.startActivity(
                Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                    .putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName),
            )
        },
    ) {
        Text("Open Android's notification settings")
    }
}

/** One channel: its name, the importance Android holds for it, and a Switch for whether Orrery posts that kind. */
@Composable
private fun ChannelRow(channelId: String, name: String) {
    val context = LocalContext.current
    val importance = remember(channelId) { channelImportance(context, channelId) }
    val preferences = PushPreferences(context)
    var posts by remember(channelId) { mutableStateOf(preferences.posts(channelId)) }
    ListItem(
        title = name,
        supporting = importanceLine(importance),
        end = {
            Switch(
                checked = posts,
                onCheckedChange = {
                    posts = it
                    preferences.setPosts(channelId, it)
                },
                label = name,
            )
        },
    )
}

/** The importance Android holds for [channelId], once the app has created its channels. */
private fun channelImportance(context: Context, channelId: String): Int {
    PushNotifier(context).ensureChannels()
    return context.getSystemService(NotificationManager::class.java).getNotificationChannel(channelId).importance
}

private fun importanceLine(importance: Int): String = when (importance) {
    NotificationManager.IMPORTANCE_NONE -> "Off in Android's settings"
    NotificationManager.IMPORTANCE_MIN, NotificationManager.IMPORTANCE_LOW -> "Silent"
    else -> "Sound and vibration"
}

/** Where push registration stands: no distributor, registered with this server, or not registered with it yet. */
@Composable
private fun PushStatus(settings: ServerSettings) {
    val preferences = PushPreferences(LocalContext.current)
    val registered = preferences.registeredAddress
    when {
        preferences.distributorMissing -> InlineNote("No push distributor is set up on this phone.", NoteTone.Error)
        registered.isNotEmpty() && registered == settings.serverUrl ->
            InlineNote("Push is registered with this phone’s distributor.", NoteTone.Ok)
        else -> InlineNote("Push is not registered with this server yet.", NoteTone.Quiet)
    }
}
