package io.github.joshsymonds.orrery

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.ContentTransform
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.union
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.layout.windowInsetsTopHeight
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.push.PushNotifier
import io.github.joshsymonds.orrery.push.PushSetup
import io.github.joshsymonds.orrery.ui.ContractScreen
import io.github.joshsymonds.orrery.ui.Crumb
import io.github.joshsymonds.orrery.ui.EpicHandover
import io.github.joshsymonds.orrery.ui.EpicOpening
import io.github.joshsymonds.orrery.ui.EpicScreen
import io.github.joshsymonds.orrery.ui.EpicTalkBar
import io.github.joshsymonds.orrery.ui.PaneListScreen
import io.github.joshsymonds.orrery.ui.PaneListState
import io.github.joshsymonds.orrery.ui.PaneScreen
import io.github.joshsymonds.orrery.ui.Phase as TaskPhase
import io.github.joshsymonds.orrery.ui.PhaseOutputScreen
import io.github.joshsymonds.orrery.ui.PoolsScreen
import io.github.joshsymonds.orrery.ui.SessionScreen
import io.github.joshsymonds.orrery.ui.SettingsScreen
import io.github.joshsymonds.orrery.ui.StepMode
import io.github.joshsymonds.orrery.ui.TalkKind
import io.github.joshsymonds.orrery.ui.TalkScreen
import io.github.joshsymonds.orrery.ui.TalkSession
import io.github.joshsymonds.orrery.ui.TalkTarget
import io.github.joshsymonds.orrery.ui.TitleRow
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTheme
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import io.github.joshsymonds.orrery.ui.theme.spec
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

internal const val ROOT_CONTENT_TAG = "root-content"
internal const val TOP_BAND_TAG = "top-band"

class MainActivity : ComponentActivity() {
    private var screen by mutableStateOf<Screen>(Screen.Panes)
    private val paneListState = PaneListState()

    // Each epic's list state, by epic id, kept here rather than in the epic's screen so a visit to its contract leaves
    // the list where it was.
    private val epicListStates = mutableMapOf<String, LazyListState>()

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
                    // With no server saved or built in, the app asks for one: settings stands in for every screen,
                    // and the screen asked for (a notification's, say) shows once an address is saved.
                    var hasServer by remember { mutableStateOf(settings.hasServer) }
                    val shown = if (hasServer) screen else Screen.Settings
                    val animated = animationsOn()
                    Box(modifier = Modifier.fillMaxSize()) {
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .windowInsetsPadding(WindowInsets.safeDrawing.union(WindowInsets.ime))
                                .testTag(ROOT_CONTENT_TAG),
                        ) {
                            // Each screen, drawn by itself. With animations on, a change of screen moves between the
                            // two; at animator scale 0 the new screen replaces the old at once and no animation runs.
                            val screenContent: @Composable (Screen) -> Unit = { current ->
                                when (current) {
                                    Screen.Panes -> PaneListScreen(
                                        client = client,
                                        onOpenPane = {
                                            screen = if (it.headless) Screen.Session(it.id) else Screen.Pane(it.id)
                                        },
                                        onOpenPaneId = { screen = Screen.Pane(it) },
                                        onStartedSession = { screen = Screen.Session(it) },
                                        onOpenEpic = { screen = Screen.Epic(it) },
                                        onOpenSettings = { screen = Screen.Settings },
                                        onOpenPools = { screen = Screen.Pools },
                                        state = paneListState,
                                    )
                                    is Screen.Epic -> {
                                        BackHandler { screen = Screen.Panes }
                                        EpicScreen(
                                            client = client,
                                            epicId = current.id,
                                            onBack = { screen = Screen.Panes },
                                            onOpenStep = {
                                                screen = Screen.Step(it.id, held = it.held, back = current)
                                            },
                                            onOpenContract = { screen = Screen.Contract(current.id) },
                                            onOpenPhase = { task, phase ->
                                                screen = Screen.Phase(current.id, task, phase)
                                            },
                                            actions = {
                                                TextButton(
                                                    onClick = { screen = Screen.Talk(current.id) },
                                                    modifier = Modifier.testTag("epic-talk"),
                                                ) {
                                                    Text("Talk")
                                                }
                                            },
                                            listState = epicListStates.getOrPut(current.id) { LazyListState() },
                                            openAt = current.open,
                                            handover = { detail -> EpicHandover(detail.handover) },
                                            foot = { detail ->
                                                val scope = rememberCoroutineScope()
                                                EpicTalkBar(
                                                    steering = detail.epic.steering,
                                                    onOpen = {
                                                        scope.launch {
                                                            screen = steererScreen(client, detail.epic.steering)
                                                        }
                                                    },
                                                )
                                            },
                                        )
                                    }
                                    is Screen.Phase -> {
                                        BackHandler { screen = Screen.Epic(current.epicId) }
                                        PhaseOutputScreen(
                                            client = client,
                                            epicId = current.epicId,
                                            epic = current.epicId.substringAfterLast('/'),
                                            task = current.task,
                                            phase = current.phase,
                                            onBack = { screen = Screen.Epic(current.epicId) },
                                            onGoHome = { screen = Screen.Panes },
                                            onOpenPhase = { task, phase ->
                                                screen = Screen.Phase(current.epicId, task, phase)
                                            },
                                            onOpenSession = {
                                                screen = Screen.Step(it.id, held = it.held, back = current)
                                            },
                                        )
                                    }
                                    is Screen.Contract -> {
                                        BackHandler { screen = Screen.Epic(current.epicId) }
                                        ContractScreen(
                                            client = client,
                                            epicId = current.epicId,
                                            onBack = { screen = Screen.Epic(current.epicId) },
                                            onHome = { screen = Screen.Panes },
                                        )
                                    }
                                    is Screen.Step -> {
                                        BackHandler { screen = current.back }
                                        SessionScreen(
                                            sessionId = current.id,
                                            client = client,
                                            onBack = { screen = current.back },
                                            step = StepMode(held = current.held),
                                        )
                                    }
                                    is Screen.Talk -> {
                                        BackHandler { screen = Screen.Epic(current.epicId) }
                                        TalkListScreen(
                                            client = client,
                                            epicId = current.epicId,
                                            onBack = { screen = Screen.Epic(current.epicId) },
                                            onHome = { screen = Screen.Panes },
                                            onOpen = { target ->
                                                screen = if (target.kind == TalkKind.PANE) {
                                                    Screen.Pane(target.id)
                                                } else {
                                                    Screen.Session(target.id)
                                                }
                                            },
                                        )
                                    }
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
                                    Screen.Pools -> {
                                        BackHandler { screen = Screen.Panes }
                                        PoolsScreen(
                                            client = client,
                                            onBack = { screen = Screen.Panes },
                                        )
                                    }
                                    Screen.Settings -> {
                                        // While it asks for the server, back leaves the app as on the home list.
                                        if (hasServer) {
                                            BackHandler { screen = Screen.Panes }
                                        }
                                        SettingsScreen(
                                            settings = settings,
                                            onSaved = {
                                                client = OrreryClient(settings.serverUrl)
                                                if (hasServer || screen == Screen.Settings) {
                                                    screen = Screen.Panes
                                                }
                                                hasServer = settings.hasServer
                                            },
                                        )
                                    }
                                }
                            }
                            if (animated) {
                                AnimatedContent(
                                    targetState = shown,
                                    modifier = Modifier.fillMaxSize(),
                                    transitionSpec = {
                                        moveBetween(deeper = targetState.depth >= initialState.depth)
                                    },
                                ) { screenContent(it) }
                            } else {
                                screenContent(shown)
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

    /** The screen a notice's intent opens: its pane, its headless session, or its epic at the block it is about. */
    private fun openExtra(intent: Intent): Screen? =
        intent.getStringExtra(PushNotifier.EXTRA_PANE)?.let { Screen.Pane(it) }
            ?: intent.getStringExtra(PushNotifier.EXTRA_SESSION)?.let { Screen.Session(it) }
            ?: intent.getStringExtra(PushNotifier.EXTRA_EPIC)?.let {
                epicScreenOf(it, intent.getStringExtra(PushNotifier.EXTRA_BLOCK))
            }

    /**
     * The screen a notice opens its epic at: the contract for a contract block, the epic's now section for a question
     * or a release step, its report for a report, and its top when there is no block.
     */
    private fun epicScreenOf(epicId: String, block: String?): Screen = when (block) {
        "contract" -> Screen.Contract(epicId)
        "question", "release_step" -> Screen.Epic(epicId, EpicOpening("now"))
        "report" -> Screen.Epic(epicId, EpicOpening("report"))
        else -> Screen.Epic(epicId)
    }

    /**
     * The screen an epic's steering session opens: its tmux pane when [client] lists a pane with [id], else its headless
     * session. The pane list is read off the main thread; a list that does not read opens the session, as an id that no
     * pane has does.
     */
    private suspend fun steererScreen(client: OrreryClient, id: String): Screen {
        val pane = try {
            withContext(Dispatchers.IO) { client.panes().firstOrNull { it.id == id } }
        } catch (error: IOException) {
            null
        }
        return if (pane == null) Screen.Session(id) else Screen.Pane(id)
    }

    /**
     * The move between two screens. A deeper screen enters from the right and the one it covers leaves to the left, on
     * [Move.DRILL]; coming back is the mirror, on [Move.LEAVE]. It is used only while animations are on.
     */
    private fun moveBetween(deeper: Boolean): ContentTransform {
        val move = if (deeper) Move.DRILL else Move.LEAVE
        val side = if (deeper) 1 else -1
        val enter = slideInHorizontally(move.spec(true)) { side * it / 4 } + fadeIn(move.spec(true))
        val exit = slideOutHorizontally(move.spec(true)) { -side * it / 4 } + fadeOut(move.spec(true))
        return enter togetherWith exit
    }

    /**
     * How deep a screen sits: the home list is 0; a pane, a session, the pools, the settings or an epic is 1; an epic's
     * contract, a task's phase output or a Talk list is 2; a step's session is one deeper than the screen it opened from.
     */
    private val Screen.depth: Int
        get() = when (this) {
            Screen.Panes -> 0
            Screen.Pools, Screen.Settings, is Screen.Pane, is Screen.Session, is Screen.Epic -> 1
            is Screen.Contract, is Screen.Phase, is Screen.Talk -> 2
            is Screen.Step -> back.depth + 1
        }

    private sealed interface Screen {
        data object Panes : Screen
        data object Settings : Screen
        data object Pools : Screen
        data class Pane(val id: String) : Screen
        data class Session(val id: String) : Screen
        /** An epic, at its top or, when [open] is set, at that section. */
        data class Epic(val id: String, val open: EpicOpening? = null) : Screen

        /**
         * An epic's accepted contract, reached from the epic's summary or from a notice's contract block: back returns
         * to the epic.
         */
        data class Contract(val epicId: String) : Screen

        /** A task's phase output, reached from its epic: back returns to the epic, and [phase] is open. */
        data class Phase(val epicId: String, val task: String, val phase: TaskPhase) : Screen

        /**
         * A step's session, reached from its epic or from a task's phase output: back returns to [back]. [held] is the
         * step's held state when it was opened.
         */
        data class Step(val id: String, val held: Boolean, val back: Screen) : Screen

        /** An epic's Talk list, reached from the epic's title row: back returns to the epic. */
        data class Talk(val epicId: String) : Screen
    }
}

/**
 * An epic's Talk list as its own screen: the crumbs orrery, the epic's slug and talk, then [TalkScreen] with the epic's
 * sessions and repository. The orrery crumb calls [onHome]; back and the slug crumb call [onBack]. The epic is read off
 * the main thread; a read that fails, or an epic with no such id, says so in the list's place. [onOpen] receives the
 * target a row or a new session opens.
 */
@Composable
private fun TalkListScreen(
    client: OrreryClient,
    epicId: String,
    onBack: () -> Unit,
    onHome: () -> Unit,
    onOpen: (TalkTarget) -> Unit,
) {
    var load by remember(client, epicId) { mutableStateOf<TalkLoad>(TalkLoad.Loading) }
    LaunchedEffect(client, epicId) {
        load = try {
            val detail = withContext(Dispatchers.IO) { client.epic(epicId) }
            if (detail == null) TalkLoad.NotFound else TalkLoad.Loaded(detail)
        } catch (error: IOException) {
            TalkLoad.Failed
        }
    }

    val detail = (load as? TalkLoad.Loaded)?.detail
    Column(modifier = Modifier.fillMaxSize()) {
        TitleRow(
            crumbs = listOfNotNull(
                Crumb("orrery", onHome),
                detail?.let { Crumb(it.epic.slug, onBack) },
                Crumb("talk"),
            ),
            onBack = onBack,
        )
        when (val current = load) {
            TalkLoad.Loading -> Text("Loading...", modifier = Modifier.padding(16.dp))
            TalkLoad.Failed -> Text(
                "could not load epic",
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(16.dp),
            )
            TalkLoad.NotFound -> Text("epic not found", modifier = Modifier.padding(16.dp))
            is TalkLoad.Loaded -> TalkScreen(
                epicId = epicId,
                sessions = current.detail.sessions.map {
                    TalkSession(id = it.id, pane = it.pane, label = it.label, call = it.call, at = it.at)
                },
                repository = current.detail.repository,
                client = client,
                onOpen = onOpen,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

private sealed interface TalkLoad {
    data object Loading : TalkLoad
    data object Failed : TalkLoad
    data object NotFound : TalkLoad
    data class Loaded(val detail: EpicDetail) : TalkLoad
}
