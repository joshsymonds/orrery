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
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.union
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.layout.windowInsetsTopHeight
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.push.PushNotifier
import io.github.joshsymonds.orrery.push.PushSetup
import io.github.joshsymonds.orrery.ui.ContractScreen
import io.github.joshsymonds.orrery.ui.EpicOpening
import io.github.joshsymonds.orrery.ui.EpicScreen
import io.github.joshsymonds.orrery.ui.PaneListScreen
import io.github.joshsymonds.orrery.ui.PaneListState
import io.github.joshsymonds.orrery.ui.PaneScreen
import io.github.joshsymonds.orrery.ui.Phase as TaskPhase
import io.github.joshsymonds.orrery.ui.PhaseOutputScreen
import io.github.joshsymonds.orrery.ui.PickerPanel
import io.github.joshsymonds.orrery.ui.RowAbout
import io.github.joshsymonds.orrery.ui.PoolsScreen
import io.github.joshsymonds.orrery.ui.ProjectPageScreen
import io.github.joshsymonds.orrery.ui.SessionScreen
import io.github.joshsymonds.orrery.ui.SettingsScreen
import io.github.joshsymonds.orrery.ui.StepMode
import io.github.joshsymonds.orrery.ui.WelcomeScreen
import io.github.joshsymonds.orrery.ui.conversationLive
import io.github.joshsymonds.orrery.ui.steererOf
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

    // The About a contract's row set, with the conversation screen it returns to. The screen shows it while it is the
    // one open, and a send or Not about this clears it.
    private var conversationAbout by mutableStateOf<ConversationAbout?>(null)

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
                    // With no server saved or built in, Welcome is the only page; the screen asked for (a
                    // notification's, say) shows once an address answers.
                    var hasServer by remember { mutableStateOf(settings.hasServer) }
                    val scope = rememberCoroutineScope()
                    // Opens the conversation that steers an epic once a steering settles on it.
                    val openSteerer: (String) -> Unit = { id -> scope.launch { screen = steererScreen(client, id) } }
                    // The listed conversation an epic's steering names. The screen that draws it reads it off the main
                    // thread.
                    val listedSteerer: (String) -> Pane? = remember(client) {
                        { id -> steererOf(client.panes() + client.headless(), id) }
                    }
                    // The directory a project page's New session here opens the picker on, null while it is closed.
                    var pickerDir by remember { mutableStateOf<String?>(null) }
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
                                        onOpenPane = { screen = paneScreen(it) },
                                        onOpenPaneId = { screen = Screen.Pane(it) },
                                        onStartedSession = { screen = Screen.Session(it) },
                                        onOpenEpic = { screen = Screen.Epic(it) },
                                        onOpenProject = { screen = Screen.Project(it) },
                                        onOpenContract = { id, conversation ->
                                            screen = Screen.Contract(
                                                id,
                                                back = Screen.Panes,
                                                live = conversationLive(conversation),
                                                talk = conversation?.let(::paneScreen) ?: Screen.Panes,
                                            )
                                        },
                                        onOpenSettings = { screen = Screen.Settings },
                                        onOpenPools = { screen = Screen.Pools },
                                        state = paneListState,
                                    )
                                    is Screen.Project -> {
                                        BackHandler { screen = Screen.Panes }
                                        ProjectPageScreen(
                                            client = client,
                                            projectKey = current.key,
                                            state = paneListState,
                                            onOpenPane = { screen = paneScreen(it) },
                                            onOpenEpic = { screen = Screen.Epic(it) },
                                            onNewHere = { pickerDir = it },
                                            onBack = { screen = Screen.Panes },
                                            onOpenContract = { id, conversation ->
                                                screen = Screen.Contract(
                                                    id,
                                                    back = current,
                                                    live = conversationLive(conversation),
                                                    talk = conversation?.let(::paneScreen) ?: current,
                                                )
                                            },
                                        )
                                    }
                                    is Screen.Epic -> {
                                        BackHandler { screen = Screen.Panes }
                                        EpicScreen(
                                            client = client,
                                            epicId = current.id,
                                            onBack = { screen = Screen.Panes },
                                            onOpenStep = {
                                                screen = Screen.Step(
                                                    it.id,
                                                    held = it.held,
                                                    back = current,
                                                    label = it.task.ifEmpty { it.agent },
                                                    doing = it.doing,
                                                )
                                            },
                                            onOpenContract = { screen = Screen.Contract(current.id) },
                                            onOpenPhase = { task, phase ->
                                                screen = Screen.Phase(current.id, task, phase)
                                            },
                                            onTalk = openSteerer,
                                            listedSteerer = listedSteerer,
                                            onSteered = openSteerer,
                                            listState = epicListStates.getOrPut(current.id) { LazyListState() },
                                            openAt = current.open,
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
                                                screen = Screen.Step(
                                                    it.id,
                                                    held = it.held,
                                                    back = current,
                                                    label = it.task.ifEmpty { it.agent },
                                                    doing = it.doing,
                                                )
                                            },
                                        )
                                    }
                                    is Screen.Contract -> {
                                        BackHandler { screen = current.back }
                                        ContractScreen(
                                            client = client,
                                            epicId = current.epicId,
                                            onBack = { screen = current.back },
                                            onHome = { screen = Screen.Panes },
                                            live = current.live,
                                            onAbout = if (current.live) {
                                                { about: RowAbout ->
                                                    conversationAbout = ConversationAbout(current.talk, about)
                                                    screen = current.talk
                                                }
                                            } else {
                                                null
                                            },
                                            onTalk = if (current.live) { { screen = current.talk } } else null,
                                        )
                                    }
                                    is Screen.Step -> {
                                        BackHandler { screen = current.back }
                                        SessionScreen(
                                            sessionId = current.id,
                                            client = client,
                                            onBack = { screen = current.back },
                                            step = StepMode(
                                                held = current.held,
                                                label = current.label,
                                                doing = current.doing,
                                            ),
                                            onOpenEpic = { screen = Screen.Epic(it) },
                                            about = aboutOf(current),
                                            onClearAbout = { conversationAbout = null },
                                            onOpenContract = { id, live ->
                                                screen = Screen.Contract(id, back = current, live = live)
                                            },
                                            onSteered = openSteerer,
                                        )
                                    }
                                    is Screen.Pane -> {
                                        BackHandler { screen = Screen.Panes }
                                        PaneScreen(
                                            paneId = current.id,
                                            client = client,
                                            onBack = { screen = Screen.Panes },
                                            onOpenEpic = { screen = Screen.Epic(it) },
                                            about = aboutOf(current),
                                            onClearAbout = { conversationAbout = null },
                                            onOpenContract = { id, live ->
                                                screen = Screen.Contract(id, back = current, live = live)
                                            },
                                        )
                                    }
                                    is Screen.Session -> {
                                        BackHandler { screen = Screen.Panes }
                                        SessionScreen(
                                            sessionId = current.id,
                                            client = client,
                                            onBack = { screen = Screen.Panes },
                                            reason = current.reason,
                                            onOpenEpic = { screen = Screen.Epic(it) },
                                            about = aboutOf(current),
                                            onClearAbout = { conversationAbout = null },
                                            onOpenContract = { id, live ->
                                                screen = Screen.Contract(id, back = current, live = live)
                                            },
                                            onSteered = openSteerer,
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
                                        BackHandler { screen = Screen.Panes }
                                        SettingsScreen(
                                            settings = settings,
                                            onBack = { screen = Screen.Panes },
                                            onSaved = {
                                                client = OrreryClient(settings.serverUrl)
                                                screen = Screen.Panes
                                            },
                                            startPushSetup = pushSetup::start,
                                        )
                                    }
                                }
                            }
                            if (!hasServer) {
                                WelcomeScreen(
                                    onConnected = {
                                        client = OrreryClient(settings.serverUrl)
                                        hasServer = true
                                    },
                                    settings = settings,
                                    startPushSetup = pushSetup::start,
                                )
                            } else if (animated) {
                                AnimatedContent(
                                    targetState = screen,
                                    modifier = Modifier.fillMaxSize(),
                                    transitionSpec = {
                                        moveBetween(deeper = targetState.depth >= initialState.depth)
                                    },
                                ) { screenContent(it) }
                            } else {
                                screenContent(screen)
                            }
                        }
                        pickerDir?.let { dir ->
                            PickerPanel(
                                client = client,
                                onOpened = { paneId ->
                                    pickerDir = null
                                    screen = Screen.Pane(paneId)
                                },
                                onStarted = { sessionId ->
                                    pickerDir = null
                                    screen = Screen.Session(sessionId)
                                },
                                onClose = { pickerDir = null },
                                startIn = dir,
                            )
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
     * or a release step, and its top when there is no block.
     */
    private fun epicScreenOf(epicId: String, block: String?): Screen = when (block) {
        "contract" -> Screen.Contract(epicId)
        "question", "release_step" -> Screen.Epic(epicId, EpicOpening("now"))
        else -> Screen.Epic(epicId)
    }

    /**
     * The screen an epic's steering session opens: the tmux pane whose orrery id is [id], else the headless session [id].
     * The pane list is read off the main thread; a list that does not read opens the session, as an id that no pane has
     * does.
     */
    private suspend fun steererScreen(client: OrreryClient, id: String): Screen {
        val pane = try {
            withContext(Dispatchers.IO) { client.panes().firstOrNull { it.orreryId == id } }
        } catch (error: IOException) {
            null
        }
        return if (pane == null) Screen.Session(id) else Screen.Pane(pane.id)
    }

    /** The screen a conversation's pane opens: its session screen when headless, else its pane screen. */
    private fun paneScreen(pane: Pane): Screen =
        if (pane.headless) Screen.Session(pane.id, reason = pane.summary) else Screen.Pane(pane.id)

    /** The About set on [conversation], the screen a contract's row returned to, or null. */
    private fun aboutOf(conversation: Screen): RowAbout? = conversationAbout?.takeIf { it.screen == conversation }?.about

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
     * How deep a screen sits: the home list is 0; a project's page, a pane, a session, the pools, the settings or an epic
     * is 1; an epic's contract or a task's phase output is 2; a step's session is one deeper than the screen it opened
     * from.
     */
    private val Screen.depth: Int
        get() = when (this) {
            Screen.Panes -> 0
            Screen.Pools, Screen.Settings, is Screen.Project, is Screen.Pane, is Screen.Session, is Screen.Epic -> 1
            is Screen.Contract, is Screen.Phase -> 2
            is Screen.Step -> back.depth + 1
        }

    /** A row's About, and the conversation screen it was set on, which the About returns to. */
    private data class ConversationAbout(val screen: Screen, val about: RowAbout)

    private sealed interface Screen {
        data object Panes : Screen
        data object Settings : Screen
        data object Pools : Screen
        data class Pane(val id: String) : Screen
        data class Session(val id: String, val reason: String = "") : Screen

        /** One project of the home tree as its page, reached from its name there: back returns to the tree. */
        data class Project(val key: String) : Screen
        /** An epic, at its top or, when [open] is set, at that section. */
        data class Epic(val id: String, val open: EpicOpening? = null) : Screen

        /**
         * A contract, reached from its epic's summary, a notice's contract block, a conversation's tile, band line or
         * transcript line, or a contract row in the inbox tree: back returns to [back], which is the epic unless a
         * conversation or the inbox opened it. [live] is whether the contract's row controls and its actions are on, which
         * a conversation sets while it is not stopped or failed. [talk] is the conversation screen that a row's control or
         * Talk about it opens with its About set, which is [back] when a conversation opened it.
         */
        data class Contract(
            val epicId: String,
            val back: Screen = Epic(epicId),
            val live: Boolean = false,
            val talk: Screen = back,
        ) : Screen

        /** A task's phase output, reached from its epic: back returns to the epic, and [phase] is open. */
        data class Phase(val epicId: String, val task: String, val phase: TaskPhase) : Screen

        /**
         * A step's session, reached from its epic or from a task's phase output: back returns to [back]. [held] is the
         * step's held state when it was opened, and [label] and [doing] name the step and say what it is doing in its
         * dock.
         */
        data class Step(
            val id: String,
            val held: Boolean,
            val back: Screen,
            val label: String,
            val doing: String,
        ) : Screen
    }
}
