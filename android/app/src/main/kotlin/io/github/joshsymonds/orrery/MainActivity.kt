package io.github.joshsymonds.orrery

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.union
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.layout.windowInsetsTopHeight
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.push.PushNotifier
import io.github.joshsymonds.orrery.push.PushSetup
import io.github.joshsymonds.orrery.ui.PaneListScreen
import io.github.joshsymonds.orrery.ui.PaneListState
import io.github.joshsymonds.orrery.ui.PaneScreen
import io.github.joshsymonds.orrery.ui.SessionScreen
import io.github.joshsymonds.orrery.ui.SettingsScreen
import io.github.joshsymonds.orrery.ui.theme.OrreryTheme

internal const val ROOT_CONTENT_TAG = "root-content"
internal const val TOP_BAND_TAG = "top-band"

class MainActivity : ComponentActivity() {
    private var screen by mutableStateOf<Screen>(Screen.Panes)
    private val paneListState = PaneListState()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        val pushSetup = PushSetup(this)
        if (savedInstanceState == null) {
            pushSetup.start()
        }
        openExtra(intent)?.let { screen = it }
        val settings = ServerSettings(this)
        setContent {
            OrreryTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    var client by remember { mutableStateOf(OrreryClient(settings.serverUrl)) }
                    Box(modifier = Modifier.fillMaxSize()) {
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .windowInsetsPadding(WindowInsets.safeDrawing.union(WindowInsets.ime))
                                .testTag(ROOT_CONTENT_TAG),
                        ) {
                            when (val current = screen) {
                                Screen.Panes -> PaneListScreen(
                                    client = client,
                                    onOpenPane = {
                                        screen = if (it.headless) Screen.Session(it.id) else Screen.Pane(it.id)
                                    },
                                    onOpenPaneId = { screen = Screen.Pane(it) },
                                    onStartedSession = { screen = Screen.Session(it) },
                                    onOpenSettings = { screen = Screen.Settings },
                                    state = paneListState,
                                )
                                is Screen.Pane -> {
                                    BackHandler { screen = Screen.Panes }
                                    PaneScreen(
                                        paneId = current.id,
                                        client = client,
                                        onBack = { screen = Screen.Panes },
                                    )
                                }
                                is Screen.Session -> {
                                    BackHandler { screen = Screen.Panes }
                                    SessionScreen(
                                        sessionId = current.id,
                                        client = client,
                                        onBack = { screen = Screen.Panes },
                                    )
                                }
                                Screen.Settings -> {
                                    BackHandler { screen = Screen.Panes }
                                    SettingsScreen(
                                        settings = settings,
                                        onSaved = {
                                            client = OrreryClient(settings.serverUrl)
                                            screen = Screen.Panes
                                        },
                                    )
                                }
                            }
                        }
                        // A solid band over the status bar's inset, drawn last so nothing that
                        // scrolls or slides up from the content shows behind the clock and battery.
                        Box(
                            modifier = Modifier
                                .fillMaxWidth()
                                .windowInsetsTopHeight(WindowInsets.safeDrawing)
                                .background(MaterialTheme.colorScheme.background)
                                .testTag(TOP_BAND_TAG),
                        )
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        openExtra(intent)?.let { screen = it }
    }

    /** The screen a notice's intent opens: its pane, or its headless session. */
    private fun openExtra(intent: Intent): Screen? =
        intent.getStringExtra(PushNotifier.EXTRA_PANE)?.let { Screen.Pane(it) }
            ?: intent.getStringExtra(PushNotifier.EXTRA_SESSION)?.let { Screen.Session(it) }

    private sealed interface Screen {
        data object Panes : Screen
        data object Settings : Screen
        data class Pane(val id: String) : Screen
        data class Session(val id: String) : Screen
    }
}
