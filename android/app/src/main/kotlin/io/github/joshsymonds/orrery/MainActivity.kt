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
import io.github.joshsymonds.orrery.ui.NoticesScreen
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
                                                talk = conversation?.let { paneScreen(it) } ?: Screen.Panes,
                                            )
                                        },
                                        onOpenSettings = { screen = Screen.Settings },
                                        onOpenPools = { screen = Screen.Pools },
                                        onOpenNotices = { screen = Screen.Notices },
                                        state = paneListState,
                                    )
                                    is Screen.Project -> {
                                        BackHandler { screen = Screen.Panes }
                                        ProjectPageScreen(
                                            client = client,
                                            projectKey = current.key,
                                            state = paneListState,
                                            onOpenPane = { screen = paneScreen(it, back = current) },
                                            onOpenEpic = { screen = Screen.Epic(it, back = current) },
                                            onNewHere = { pickerDir = it },
                                            onBack = { screen = Screen.Panes },
                                            onOpenContract = { id, conversation ->
                                                screen = Screen.Contract(
                                                    id,
                                                    back = current,
                                                    live = conversationLive(conversation),
                                                    talk = conversation?.let { paneScreen(it, current) } ?: current,
                                                )
                                            },
                                        )
                                    }
                                    is Screen.Epic -> {
                                        BackHandler { screen = current.back }
                                        EpicScreen(
                                            client = client,
                                            epicId = current.id,
                                            onBack = { screen = current.back },
                                            onOpenStep = {
                                                screen = Screen.Step(
                                                    it.id,
                                                    held = it.held,
                                                    back = current,
                                                    label = it.task.ifEmpty { it.agent },
                                                    doing = it.doing,
                                                    epic = current.id,
                                                    task = it.task,
                                                )
                                            },
                                            onOpenContract = {
                                                screen = Screen.Contract(current.id, back = current.copy(open = null))
                                            },
                                            onOpenPhase = { task, phase ->
                                                screen = Screen.Phase(
                                                    current.id,
                                                    task,
                                                    phase,
                                                    back = current.copy(open = null),
                                                )
                                            },
                                            onTalk = openSteerer,
                                            listedSteerer = listedSteerer,
                                            onSteered = openSteerer,
                                            listState = epicListStates.getOrPut(current.id) { LazyListState() },
                                            openAt = current.open,
                                        )
                                    }
                                    is Screen.Phase -> {
                                        BackHandler { screen = current.back }
                                        PhaseOutputScreen(
                                            client = client,
                                            epicId = current.epicId,
                                            epic = current.epicId.substringAfterLast('/'),
                                            task = current.task,
                                            onOpenEpic = { screen = current.back },
                                            onOpenTask = { task, phase ->
                                                screen = Screen.Phase(current.epicId, task, phase, back = current.back)
                                            },
                                            onOpenSession = {
                                                screen = Screen.Step(
                                                    it.id,
                                                    held = it.held,
                                                    back = current,
                                                    label = it.task.ifEmpty { it.agent },
                                                    doing = it.doing,
                                                    epic = current.epicId,
                                                    task = it.task,
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
                                                epic = current.epic,
                                                task = current.task,
                                            ),
                                            onOpenEpic = { screen = Screen.Epic(it) },
                                            onOpenTask = { task, phase ->
                                                screen = Screen.Phase(current.epic, task, phase, back = Screen.Epic(current.epic))
                                            },
                                            about = aboutOf(current),
                                            onClearAbout = { conversationAbout = null },
                                            onOpenContract = { id, live ->
                                                screen = Screen.Contract(id, back = current, live = live)
                                            },
                                            onSteered = openSteerer,
                                        )
                                    }
                                    is Screen.Pane -> {
                                        BackHandler { screen = current.back }
                                        PaneScreen(
                                            paneId = current.id,
                                            client = client,
                                            onBack = { screen = current.back },
                                            onOpenEpic = { screen = Screen.Epic(it) },
                                            about = aboutOf(current),
                                            onClearAbout = { conversationAbout = null },
                                            onOpenContract = { id, live ->
                                                screen = Screen.Contract(id, back = current, live = live)
                                            },
                                        )
                                    }
                                    is Screen.Session -> {
                                        BackHandler { screen = current.back }
                                        SessionScreen(
                                            sessionId = current.id,
                                            client = client,
                                            onBack = { screen = current.back },
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
                                    Screen.Notices -> {
                                        BackHandler { screen = Screen.Panes }
                                        NoticesScreen(
                                            client = client,
                                            onBack = { screen = Screen.Panes },
                                            onOpen = { row ->
                                                targetScreen(
                                                    row.pane,
                                                    row.session,
                                                    row.epic,
                                                    row.blockKind,
                                                    back = Screen.Notices,
                                                )?.let { screen = it }
                                            },
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
    private fun openExtra(intent: Intent): Screen? = targetScreen(
        pane = intent.getStringExtra(PushNotifier.EXTRA_PANE).orEmpty(),
        session = intent.getStringExtra(PushNotifier.EXTRA_SESSION).orEmpty(),
        epic = intent.getStringExtra(PushNotifier.EXTRA_EPIC).orEmpty(),
        block = intent.getStringExtra(PushNotifier.EXTRA_BLOCK).orEmpty(),
    )

    /**
     * The screen a notice's target opens: the pane, else the headless session, else the epic at [block], each of them
     * empty when the notice does not name it. [back] is where the opened screen returns to; null keeps each screen's own,
     * which is the home list, or the epic for a contract.
     */
    private fun targetScreen(pane: String, session: String, epic: String, block: String, back: Screen? = null): Screen? =
        when {
            pane.isNotEmpty() -> Screen.Pane(pane, back ?: Screen.Panes)
            session.isNotEmpty() -> Screen.Session(session, back = back ?: Screen.Panes)
            epic.isNotEmpty() -> epicScreenOf(epic, block, back)
            else -> null
        }

    /**
     * The screen a notice opens its epic at: the contract for a contract block, the epic's now section for a question
     * or a release step, and its top when there is no block. [back] is as in [targetScreen].
     */
    private fun epicScreenOf(epicId: String, block: String, back: Screen?): Screen = when (block) {
        "contract" -> Screen.Contract(epicId, back = back ?: Screen.Epic(epicId))
        "question", "release_step" -> Screen.Epic(epicId, EpicOpening("now"), back ?: Screen.Panes)
        else -> Screen.Epic(epicId, back = back ?: Screen.Panes)
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

    /**
     * The screen a conversation's pane opens: its session screen when headless, else its pane screen, returning to
     * [back].
     */
    private fun paneScreen(pane: Pane, back: Screen = Screen.Panes): Screen =
        if (pane.headless) {
            Screen.Session(pane.id, reason = pane.summary, back = back)
        } else {
            Screen.Pane(pane.id, back = back)
        }

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
     * How deep a screen sits: the home list is 0; a project's page, the pools, the notices or the settings is 1; an epic's
     * contract or a task's phase output is 2; a pane, a session, an epic or a step's session is one deeper than the screen
     * it opened from.
     */
    private val Screen.depth: Int
        get() = when (this) {
            Screen.Panes -> 0
            Screen.Pools, Screen.Notices, Screen.Settings, is Screen.Project -> 1
            is Screen.Pane -> back.depth + 1
            is Screen.Session -> back.depth + 1
            is Screen.Epic -> back.depth + 1
            is Screen.Contract, is Screen.Phase -> 2
            is Screen.Step -> back.depth + 1
        }

    /** A row's About, and the conversation screen it was set on, which the About returns to. */
    private data class ConversationAbout(val screen: Screen, val about: RowAbout)

    private sealed interface Screen {
        data object Panes : Screen
        data object Settings : Screen
        data object Pools : Screen
        /** The notice history, reached from the bell on the home list: back returns to the home list. */
        data object Notices : Screen
        /** A conversation's pane, reached from the home tree or a project's page: back returns to [back]. */
        data class Pane(val id: String, val back: Screen = Panes) : Screen

        /** A headless conversation, reached like [Pane]: back returns to [back]. */
        data class Session(val id: String, val reason: String = "", val back: Screen = Panes) : Screen

        /** One project of the home tree as its page, reached from its name there: back returns to the tree. */
        data class Project(val key: String) : Screen
        /**
         * An epic, at its top or, when [open] is set, at that section. Back returns to [back], which is the home tree
         * unless a project's page opened it.
         */
        data class Epic(val id: String, val open: EpicOpening? = null, val back: Screen = Panes) : Screen

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

        /** A task's phase output, reached from its epic: back returns to [back], the epic, and [phase] is open. */
        data class Phase(val epicId: String, val task: String, val phase: TaskPhase, val back: Screen) : Screen

        /**
         * A step's session, reached from its epic or from a task's phase output: back returns to [back]. [held] is the
         * step's held state when it was opened, and [label] and [doing] name the step and say what it is doing in its
         * dock. [epic] and [task] are the step's epic and task, whose record the session's band and crumbs read.
         */
        data class Step(
            val id: String,
            val held: Boolean,
            val back: Screen,
            val label: String,
            val doing: String,
            val epic: String,
            val task: String,
        ) : Screen
    }
}
