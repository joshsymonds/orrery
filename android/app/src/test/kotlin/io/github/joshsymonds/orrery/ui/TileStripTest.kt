package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.BackgroundTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val SESSION = "example-session"
private const val REPO = "example-repo"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w800dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TileStripTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val opened = mutableListOf<RailThing>()
    private val pickers = mutableListOf<RailGroup>()

    private fun step(id: String): StepRow = StepRow(
        id = id,
        task = "task-$id",
        agent = "agent-$id",
        end = "",
        status = "running",
        held = false,
        started = 0L,
        ended = 0L,
        doing = "",
        checks = emptyList(),
        reason = "",
    )

    private fun epic(
        id: String,
        slug: String = "$REPO-alpha-work",
        live: Boolean = true,
        steering: String = SESSION,
        archived: Boolean = false,
        running: List<StepRow> = emptyList(),
        outcome: String = "",
        phaseAt: Long = 0L,
        seen: Long = 0L,
    ): EpicRow = EpicRow(
        id = id,
        repository = REPO,
        slug = slug,
        outcome = outcome,
        changed = 0L,
        live = live,
        runningSteps = running,
        endedSteps = 0,
        problem = "",
        source = "orreryd",
        phaseAt = phaseAt,
        seen = seen,
        steering = steering,
        archived = archived,
    )

    private fun task(id: String, type: String, status: String): BackgroundTaskRow = BackgroundTaskRow(
        id = id,
        toolUseId = "tool-$id",
        description = "",
        type = type,
        subagentType = "",
        status = status,
        activity = "",
        started = 0L,
        ended = 0L,
    )

    private fun question(id: String, text: String): EpicQuestionRow = EpicQuestionRow(id = id, text = text, turn = 1L, asked = 0L)

    private fun detail(epic: EpicRow, questions: List<EpicQuestionRow> = emptyList()): EpicDetail = EpicDetail(
        epic = epic,
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        questions = questions,
    )

    private fun epicsOf(groups: List<RailGroup>): List<EpicThing> =
        groups.filter { it.kind == GroupKind.EPIC }.flatMap { group -> group.things.filterIsInstance<EpicThing>() }

    private fun keysOfEpics(groups: List<RailGroup>): List<String> = epicsOf(groups).map { it.key }

    private fun epicThing(
        id: String,
        asks: Boolean = false,
        tone: ThingTone = ThingTone.QUIET,
        detail: EpicDetail? = null,
    ): EpicThing = EpicThing(id = id, slug = id, short = "alpha", asks = asks, tone = tone, state = "", detail = detail)

    private fun countNodes(tag: String): Int =
        composeRule.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().size

    private fun setStrip(groups: List<RailGroup>, end: @Composable () -> Unit = {}) {
        composeRule.setContent {
            TileStrip(
                groups = groups,
                onOpen = { opened.add(it) },
                onOpenPicker = { pickers.add(it) },
                end = end,
            )
        }
    }

    @Test
    fun groupsListTheEpicsThenTheRunningAgentsThenTheRunningShells() {
        val groups = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = listOf(epic("e1")),
            details = emptyMap(),
            background = listOf(task("a1", "agent", "running"), task("s1", "shell", "running")),
        )

        assertEquals(listOf(GroupKind.EPIC, GroupKind.AGENTS, GroupKind.RUNNING), groups.map { it.kind })
        assertEquals(listOf("epic", "agents", "running"), groups.map { it.label })
    }

    @Test
    fun anEpicSteeredElsewhereArchivedOrEndedAndSeenIsLeftOutWhileAnEndedUnseenOneShows() {
        val groups = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = listOf(
                epic("steered-elsewhere", steering = "other-session"),
                epic("archived", archived = true),
                epic("ended-seen", live = false, phaseAt = 100L, seen = 200L),
                epic("ended-unseen", live = false, phaseAt = 300L, seen = 0L),
            ),
            details = emptyMap(),
            background = emptyList(),
        )

        assertEquals(listOf("ended-unseen"), keysOfEpics(groups))
    }

    @Test
    fun anEmptySessionSteersNoEpic() {
        val groups = workGroupsOf(
            session = "",
            pane = false,
            epics = listOf(epic("e1", steering = "")),
            details = emptyMap(),
            background = emptyList(),
        )

        assertEquals(emptyList<String>(), keysOfEpics(groups))
    }

    @Test
    fun theLiveEpicsComeFirstInListOrderThenTheEndedUnseenNewestEndingFirst() {
        val groups = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = listOf(
                epic("ended-older", live = false, phaseAt = 100L),
                epic("live-one"),
                epic("ended-newer", live = false, phaseAt = 300L),
                epic("live-two"),
            ),
            details = emptyMap(),
            background = emptyList(),
        )

        assertEquals(listOf("live-one", "live-two", "ended-newer", "ended-older"), keysOfEpics(groups))
    }

    @Test
    fun anEpicThatAsksIsAttentionAndReadsItsFirstNeed() {
        val asking = epic("asking")
        val groups = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = listOf(asking),
            details = mapOf("asking" to detail(asking, listOf(question("Q1", "Which branch?")))),
            background = emptyList(),
        )

        val thing = epicsOf(groups).single()
        assertTrue(thing.asks)
        assertEquals(ThingTone.ATTENTION, thing.tone)
        assertEquals("$REPO-alpha-work asks: Which branch?", thing.state)
    }

    @Test
    fun aLiveEpicWithARunningStepIsLiveAndAQuietOneIsQuietAndReadsItsOutcome() {
        val groups = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = listOf(
                epic("running", running = listOf(step("s1")), outcome = "Example outcome."),
                epic("quiet", outcome = "Example outcome."),
            ),
            details = emptyMap(),
            background = emptyList(),
        )

        val things = epicsOf(groups).associateBy { it.key }
        assertEquals(ThingTone.LIVE, things.getValue("running").tone)
        assertEquals(ThingTone.QUIET, things.getValue("quiet").tone)
        assertEquals(false, things.getValue("running").asks)
        assertEquals("Example outcome.", things.getValue("running").state)
    }

    @Test
    fun shortNameDropsTheRepositoryAndTakesTheFirstWordOfTheSlug() {
        val groups = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = listOf(epic("prefixed", slug = "$REPO-alpha-beta"), epic("bare", slug = "gamma-delta-epsilon")),
            details = emptyMap(),
            background = emptyList(),
        )

        val shorts = epicsOf(groups).associate { it.key to it.short }
        assertEquals("alpha", shorts["prefixed"])
        assertEquals("gamma", shorts["bare"])
    }

    @Test
    fun listsHoldTheRunningTasksWithCountsAndNoneForAPane() {
        val background = listOf(
            task("a1", "agent", "running"),
            task("a2", "agent", "running"),
            task("a3", "agent", "done"),
            task("s1", "shell", "running"),
            task("s2", "shell", "stopped"),
        )

        val groups = workGroupsOf(SESSION, pane = false, epics = emptyList(), details = emptyMap(), background = background)
        val agents = groups.first { it.kind == GroupKind.AGENTS }.things.single() as ListThing
        val shells = groups.first { it.kind == GroupKind.RUNNING }.things.single() as ListThing
        assertEquals(2, agents.count)
        assertEquals(ThingTone.LIVE, agents.tone)
        assertEquals(1, shells.count)
        assertEquals(ThingTone.QUIET, shells.tone)

        val paneGroups = workGroupsOf(SESSION, pane = true, epics = listOf(epic("e1")), details = emptyMap(), background = background)
        assertEquals(listOf(GroupKind.EPIC), paneGroups.map { it.kind })
    }

    @Test
    fun aGroupIsManyOnlyWhenItHoldsMoreThanThreeThings() {
        fun groupsOf(count: Int): List<RailGroup> = workGroupsOf(
            session = SESSION,
            pane = false,
            epics = (1..count).map { epic("e$it") },
            details = emptyMap(),
            background = emptyList(),
        )

        assertEquals(false, groupsOf(3).single().many)
        assertEquals(true, groupsOf(4).single().many)
    }

    @Test
    fun eachGroupNameDrawsOverItsTiles() {
        setStrip(
            listOf(
                RailGroup(GroupKind.EPIC, "epic", listOf(epicThing("epic-one")), many = false),
                RailGroup(GroupKind.AGENTS, "agents", listOf(ListThing(GroupKind.AGENTS, listOf(task("a1", "agent", "running")), ThingTone.LIVE)), many = false),
            ),
        )

        val labelBottom = composeRule.onNodeWithTag("rail-label:epic").getBoundsInRoot().bottom
        val tileTop = composeRule.onNodeWithTag("tile:epic-one").getBoundsInRoot().top
        assertTrue(labelBottom <= tileTop)
    }

    @Test
    fun anAskingEpicShowsAQuestionMark() {
        setStrip(listOf(RailGroup(GroupKind.EPIC, "epic", listOf(epicThing("epic-one", asks = true, tone = ThingTone.ATTENTION)), many = false)))

        composeRule.onNodeWithTag("tile-ask:epic-one").assertTextEquals("?")
    }

    @Test
    fun fourEpicsCollapseToOneDotsTileThatOpensThePicker() {
        val group = RailGroup(GroupKind.EPIC, "epics", (1..4).map { epicThing("epic-$it") }, many = true)
        setStrip(listOf(group))

        assertEquals(0, countNodes("tile:epic-1"))
        composeRule.onNodeWithTag("tile-dots:epic").assertTextEquals("4").performClick()
        assertEquals(listOf(group), pickers)
        assertTrue(opened.isEmpty())
    }

    @Test
    fun aDotsTileShowsAQuestionMarkWhenOneEpicAsks() {
        val things = listOf(epicThing("epic-1"), epicThing("epic-2", asks = true, tone = ThingTone.ATTENTION), epicThing("epic-3"), epicThing("epic-4"))
        setStrip(listOf(RailGroup(GroupKind.EPIC, "epics", things, many = true)))

        composeRule.onNodeWithTag("tile-dots:epic").assertTextEquals("?")
    }

    @Test
    fun aListTileTapCallsOnOpenWithItsThing() {
        val agents = ListThing(GroupKind.AGENTS, listOf(task("a1", "agent", "running")), ThingTone.LIVE)
        setStrip(listOf(RailGroup(GroupKind.AGENTS, "agents", listOf(agents), many = false)))

        composeRule.onNodeWithTag("tile:agents").performClick()
        assertEquals(listOf<RailThing>(agents), opened)
        assertTrue(pickers.isEmpty())
    }

    @Test
    fun anEpicTileDrawsItsMiniOrreryWhenItsDetailIsHeld() {
        val held = epic("epic-one")
        setStrip(listOf(RailGroup(GroupKind.EPIC, "epic", listOf(epicThing("epic-one", detail = detail(held))), many = false)))

        assertEquals(1, countNodes("mini-orrery:epic-one"))
    }

    @Test
    fun anEpicTileWithoutItsDetailShowsItsShortNameAndNoMiniOrrery() {
        setStrip(listOf(RailGroup(GroupKind.EPIC, "epic", listOf(epicThing("epic-one")), many = false)))

        assertEquals(0, countNodes("mini-orrery:epic-one"))
        composeRule.onNodeWithText("alpha").assertIsDisplayed()
    }

    @Test
    fun theEndControlIsDrawnAfterTheLastGroup() {
        setStrip(
            groups = listOf(
                RailGroup(GroupKind.EPIC, "epic", listOf(epicThing("epic-one")), many = false),
                RailGroup(GroupKind.AGENTS, "agents", listOf(ListThing(GroupKind.AGENTS, listOf(task("a1", "agent", "running")), ThingTone.LIVE)), many = false),
            ),
            end = { Text("end-control") },
        )

        val groupRight = composeRule.onNodeWithTag("rail-group:agents").getBoundsInRoot().right
        val endLeft = composeRule.onNodeWithTag("strip-end").getBoundsInRoot().left
        assertTrue(endLeft >= groupRight)
        composeRule.onNodeWithText("end-control").assertIsDisplayed()
    }
}
