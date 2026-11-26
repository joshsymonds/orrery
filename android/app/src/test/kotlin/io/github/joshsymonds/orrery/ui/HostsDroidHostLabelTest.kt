package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * R1 of the hosts revision, end to end: every pane, headless session, epic and step names the host it runs on. A card
 * names its host once on its heading, the serving host's included, and the session rows under it name none. An epic
 * names its own host on its row, and each of its steps names its own, an empty one reading as the serving host. A
 * headless session keeps its host on its card. The panes, sessions, epics and steps are invented and sit on two hosts,
 * or on one, the clock is fixed, and every check reads semantics only.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class HostsDroidHostLabelTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val SERVING = "example-host"
        const val REMOTE = "example-remote"
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val REPOSITORY = "example-repo"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val SESSIONS_LIST = "/orrery.v1.Sessions/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val CONTRACTS_LIST = "/orrery.v1.Contracts/List"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val SERVING_SESSION = "10000000000000000000000000000001"
        const val REMOTE_SESSION = "10000000000000000000000000000002"
        const val REMOTE_HEADLESS = "20000000000000000000000000000001"
        const val EMPTY_HEADLESS = "20000000000000000000000000000002"
        const val REMOTE_EPIC = "$REPOSITORY/remote-epic"
        const val SERVING_EPIC = "$REPOSITORY/serving-epic"
        const val ONE_HOST_EPIC = "$REPOSITORY/one-host-epic"
        const val REMOTE_STEP = "step-remote"
        const val EMPTY_STEP = "step-empty"
        const val SERVING_STEP = "step-serving"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun pane(id: String, host: String, cwd: String, orreryId: String): PaneProto = PaneProto.newBuilder()
        .setId(id)
        .setHost(host)
        .setSession("example-session")
        .setWindow(1)
        .setPane(0)
        .setCwd(cwd)
        .setCommand("claude")
        .setState("working")
        .setSummary("Synthetic summary.")
        .setLabel("label-$id")
        .setChanged(NOW - 60)
        .setSeen(NOW - 60)
        .setQuestion("")
        .setQuestionHash("")
        .setNewestReply(NOW - 60)
        .setOrreryId(orreryId)
        .setClaudeSession("synthetic-claude-$id")
        .build()

    private fun headless(id: String, host: String, dir: String): HeadlessSession = HeadlessSession.newBuilder()
        .setId(id)
        .setHost(host)
        .setDir(dir)
        .setLabel("synthetic-$id")
        .setState("working")
        .setChanged(NOW - 120)
        .setSeen(NOW - 120)
        .setSummary("Synthetic working session.")
        .setNewestReply(NOW - 120)
        .build()

    private fun step(id: String, host: String): EpicStep = EpicStep.newBuilder()
        .setId(id)
        .setTask("synthetic-task")
        .setAgent("synthetic-agent")
        .setPurpose("purpose-$id")
        .setDoing("synthetic doing")
        .setHost(host)
        .setStarted(NOW - 300)
        .build()

    private fun epic(
        id: String,
        host: String,
        path: String,
        steering: String,
        steps: List<EpicStep> = emptyList(),
    ): Epic =
        Epic.newBuilder()
            .setId(id)
            .setRepository(REPOSITORY)
            .setPath(path)
            .setSlug(id.substringAfter('/'))
            .setOutcome("")
            .setChanged(NOW - 30)
            .setLive(true)
            .setEndedSteps(0)
            .setSteering(steering)
            .setHost(host)
            .addAllRunningSteps(steps)
            .build()

    /**
     * Serves the panes, headless sessions and epics given, each epic's detail answering its own read, and [SERVING] as
     * the host the server names.
     */
    private fun serve(
        server: FakeOrreryServer,
        panes: List<PaneProto>,
        sessions: List<HeadlessSession>,
        epics: List<Epic>,
    ) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(SERVING).build())
        server.respond(PANES_LIST, PanesListResponse.newBuilder().addAllPanes(panes).build())
        server.respond(SESSIONS_LIST, ListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.newBuilder().addAllSessions(sessions).build())
        server.respond(EPICS_LIST, EpicsListResponse.newBuilder().addAllEpics(epics).build())
        val byId = epics.associateBy { it.id }
        server.route(EPICS_GET) { request ->
            val epic = byId.getValue(EpicsGetRequest.parseFrom(request.body).id)
            Reply.Message(EpicsGetResponse.newBuilder().setEpic(epic).build(), 0L)
        }
        server.respond(CONTRACTS_LIST, ContractsListResponse.getDefaultInstance())
    }

    /** The inbox on a fixed clock, made once outside the composition as the other screen tests make it. */
    private fun setScreen(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            PaneListScreen(
                client = client,
                onOpenPane = {},
                onOpenPaneId = {},
                onStartedSession = {},
                onOpenSettings = {},
                now = { NOW },
            )
        }
    }

    /** The project page of [projectKey] on a fixed clock. */
    private fun setPage(server: FakeOrreryServer, projectKey: String) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            ProjectPageScreen(
                client = client,
                projectKey = projectKey,
                state = PaneListState(),
                onOpenPane = {},
                onOpenEpic = {},
                onNewHere = {},
                onBack = {},
                now = { NOW },
            )
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun awaitNode(matcher: SemanticsMatcher) {
        composeRule.awaitUntil { countNodes(matcher) > 0 }
    }

    /** How many times [host]'s name is drawn under the node tagged [tag]. */
    private fun namedUnder(tag: String, host: String): Int =
        countNodes(hasText(host) and hasAnyAncestor(hasTestTag(tag)))

    /** A card's heading: the host and folder its key carries. */
    private fun headTag(host: String, dir: String): String = "card-host:$host|$dir"

    private fun cardTag(host: String, dir: String): String = "project:$host|$dir"

    private fun convoTag(key: String): String = "convo:$key"

    private fun hungTag(key: String): String = "hung:$key"

    private fun stepTag(id: String): String = "step:$id"

    @Test
    fun twoHostsInboxNamesEachCardsHostOnItsHeadOnceAndEachRowsHostInline() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                panes = listOf(pane("%1", SERVING, ALPHA, SERVING_SESSION), pane("%2", REMOTE, ALPHA, REMOTE_SESSION)),
                sessions = listOf(headless(REMOTE_HEADLESS, REMOTE, ALPHA), headless(EMPTY_HEADLESS, "", ALPHA)),
                epics = listOf(
                    epic(
                        REMOTE_EPIC,
                        REMOTE,
                        ALPHA,
                        steering = SERVING_SESSION,
                        steps = listOf(step(REMOTE_STEP, REMOTE), step(EMPTY_STEP, ""), step(SERVING_STEP, SERVING)),
                    ),
                    epic(SERVING_EPIC, "", BETA, steering = ""),
                ),
            )
            setScreen(server)
            awaitNode(hasTestTag(headTag(SERVING, BETA)))

            // Each card names its host once on its heading: the serving host's cards and a headless session's card.
            assertEquals(1, namedUnder(headTag(SERVING, ALPHA), SERVING))
            assertEquals(1, namedUnder(headTag(REMOTE, ALPHA), REMOTE))
            assertEquals(1, namedUnder(headTag(SERVING, BETA), SERVING))
            assertEquals(1, namedUnder(cardTag(REMOTE, ALPHA), REMOTE))

            // A session row under a card names no host of its own: a pane's, and a headless session's on either host.
            assertEquals(0, namedUnder(convoTag("pane:$SERVING:%1"), SERVING))
            assertEquals(0, namedUnder(convoTag("pane:$REMOTE:%2"), REMOTE))
            assertEquals(0, namedUnder(convoTag("session:$REMOTE_HEADLESS"), REMOTE))
            assertEquals(0, namedUnder(convoTag("session:$EMPTY_HEADLESS"), SERVING))

            // An epic names its own host on its row, not the serving host of the conversation that steers it.
            assertEquals(1, namedUnder(hungTag("epic:$REMOTE_EPIC"), REMOTE))
            assertEquals(0, namedUnder(hungTag("epic:$REMOTE_EPIC"), SERVING))
            assertEquals(1, namedUnder(hungTag("epic:$SERVING_EPIC"), SERVING))

            // Each step names its own host, and a step with none names the serving host.
            assertEquals(1, namedUnder(stepTag(REMOTE_STEP), REMOTE))
            assertEquals(1, namedUnder(stepTag(EMPTY_STEP), SERVING))
            assertEquals(1, namedUnder(stepTag(SERVING_STEP), SERVING))
        }
    }

    @Test
    fun twoHostsProjectPageNamesItsHostOnItsHeadAndItsWorkCardsHost() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                panes = listOf(pane("%1", SERVING, ALPHA, SERVING_SESSION)),
                sessions = listOf(headless(EMPTY_HEADLESS, "", ALPHA)),
                epics = listOf(epic(REMOTE_EPIC, REMOTE, ALPHA, steering = SERVING_SESSION)),
            )
            setPage(server, "$SERVING|$ALPHA")
            awaitNode(hasTestTag("card:epic:$REMOTE_EPIC"))

            // The page's head names its host once, and the WorkCard under a conversation names the epic's own host.
            assertEquals(1, namedUnder(headTag(SERVING, ALPHA), SERVING))
            assertEquals(1, namedUnder("card:epic:$REMOTE_EPIC", REMOTE))
            assertEquals(0, namedUnder("project-convo:pane:$SERVING:%1", SERVING))
        }
    }

    @Test
    fun oneHostInboxStillNamesTheServingHostOnEachCardHeadEpicRowAndStep() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                panes = listOf(pane("%1", SERVING, ALPHA, SERVING_SESSION)),
                sessions = listOf(headless(EMPTY_HEADLESS, "", ALPHA)),
                epics = listOf(
                    epic(
                        ONE_HOST_EPIC,
                        "",
                        ALPHA,
                        steering = SERVING_SESSION,
                        steps = listOf(step(EMPTY_STEP, ""), step(SERVING_STEP, SERVING)),
                    ),
                ),
            )
            setScreen(server)
            awaitNode(hasTestTag(headTag(SERVING, ALPHA)))

            assertEquals(1, namedUnder(headTag(SERVING, ALPHA), SERVING))
            assertEquals(1, namedUnder(hungTag("epic:$ONE_HOST_EPIC"), SERVING))
            assertEquals(1, namedUnder(stepTag(EMPTY_STEP), SERVING))
            assertEquals(1, namedUnder(stepTag(SERVING_STEP), SERVING))
            assertEquals(0, namedUnder(convoTag("pane:$SERVING:%1"), SERVING))
            assertEquals(0, namedUnder(convoTag("session:$EMPTY_HEADLESS"), SERVING))
        }
    }

    @Test
    fun oneHostProjectPageStillNamesTheServingHostOnItsHeadAndItsWorkCard() {
        FakeOrreryServer().use { server ->
            serve(
                server,
                panes = listOf(pane("%1", SERVING, ALPHA, SERVING_SESSION)),
                sessions = emptyList(),
                epics = listOf(epic(ONE_HOST_EPIC, "", ALPHA, steering = SERVING_SESSION)),
            )
            setPage(server, "$SERVING|$ALPHA")
            awaitNode(hasTestTag("card:epic:$ONE_HOST_EPIC"))

            assertEquals(1, namedUnder(headTag(SERVING, ALPHA), SERVING))
            assertEquals(1, namedUnder("card:epic:$ONE_HOST_EPIC", SERVING))
        }
    }

    @Test
    fun theNowTableNamesEachStepsHostAndTheTitleRowsNameTheirHostsInline() {
        val lines = listOf(
            StepLine(
                id = REMOTE_STEP,
                glyph = StepGlyph.WORKING,
                label = "purpose-$REMOTE_STEP",
                task = "",
                doing = "synthetic doing",
                since = 0L,
                host = REMOTE,
            ),
            StepLine(
                id = SERVING_STEP,
                glyph = StepGlyph.HELD,
                label = "purpose-$SERVING_STEP",
                task = "",
                doing = "synthetic doing",
                since = 0L,
                host = SERVING,
            ),
        )
        composeRule.setContent {
            Column {
                StepRows(lines = lines, tasks = emptyList(), now = NOW)
                TitleRow(crumbs = listOf(Crumb("alpha")), onBack = null, host = SERVING)
                TitleRow(crumbs = listOf(Crumb("beta")), onBack = null, host = REMOTE)
            }
        }

        assertEquals(1, namedUnder("step-row:$REMOTE_STEP", REMOTE))
        assertEquals(1, namedUnder("step-row:$SERVING_STEP", SERVING))
        // Each title row names its host once, so the two rows and the two step rows name each host twice.
        assertEquals(2, countNodes(hasText(SERVING)))
        assertEquals(2, countNodes(hasText(REMOTE)))
    }

    @Test
    fun aNowStepWithNoHostOfItsOwnNamesItsEpicsHost() {
        val epic = EpicRow(
            id = REMOTE_EPIC,
            repository = REPOSITORY,
            slug = "remote-epic",
            outcome = "",
            changed = NOW,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            host = REMOTE,
        )
        val running = StepRow(
            id = REMOTE_STEP,
            task = "synthetic-task",
            agent = "synthetic-agent",
            end = "",
            status = "",
            held = false,
            started = NOW - 300,
            ended = 0L,
            doing = "synthetic doing",
            checks = emptyList(),
            reason = "",
        )
        val detail = EpicDetail(
            epic = epic,
            steps = listOf(running.copy(host = ""), running.copy(id = SERVING_STEP, host = SERVING)),
        )

        assertEquals(listOf(REMOTE, SERVING), stepLinesOf(detail).map { it.host })
    }
}
