package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.HostEntry
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The inbox's unreachable rows and card heads on Android: a pane, a headless session, an epic and a step on a host that
// does not answer, and a card on that host, drawn by inboxTree from invented panes, sessions, an epic and a step. A
// reachable session on the serving host draws its own glyph as a control. Checks read semantics only: the state glyph's
// words and its colour on GlyphColourKey, the host mark's ring on HostRingColourKey, and the text a row says. The clock is
// fixed. Queries read the unmerged tree, since a clickable row merges its children.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class UnreachableRowsTest {
    private companion object {
        const val NOW = 100_000L
        const val SERVING = "example-host"
        const val REMOTE = "example-remote"
        const val DIR = "/home/user/lighthouse"
        const val REMOTE_CONVO = "remote-convo"
        const val REMOTE_PANE_ROW = "convo:pane:$REMOTE:%1"
        const val REMOTE_SESSION_ROW = "convo:session:remote-session"
        const val SERVING_SESSION_ROW = "convo:session:serving-session"
        const val EPIC_ID = "lighthouse/remote-epic"
        const val EPIC_ROW = "hung:epic:$EPIC_ID"
        const val REMOTE_STEP_ROW = "step:remote-step"
        const val SERVING_STEP_ROW = "step:serving-step"
        const val REMOTE_CARD = "card-host:$REMOTE|$DIR"
        const val SERVING_CARD = "card-host:$SERVING|$DIR"
        const val UNREACHABLE = "unreachable"
        const val HOST_UNREACHABLE = "host-unreachable"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    /** The host status: the serving host answers, and [REMOTE] does not. */
    private val hosts = listOf(
        HostEntry(name = SERVING, loadPercent = 12.0, memoryPercent = 40.0, reachable = true),
        HostEntry(name = REMOTE, loadPercent = 0.0, memoryPercent = 0.0, reachable = false),
    )

    private fun pane(
        id: String,
        host: String,
        headless: Boolean = false,
        orreryId: String = "",
        claudeSession: String = "",
        unreachable: Boolean = false,
    ) = Pane(
        id = id,
        host = host,
        session = "",
        window = 0,
        pane = 0,
        cwd = DIR,
        command = "",
        state = "working",
        summary = "",
        label = "Lamp $id",
        changed = NOW - 120,
        seen = 0L,
        question = "",
        questionHash = "",
        headless = headless,
        orreryId = orreryId,
        claudeSession = claudeSession,
        unreachable = unreachable,
    )

    /** A conversation on [REMOTE] whose host flags it unreachable, as serve does for a pane on a host that does not answer. */
    private val remotePane = pane(
        id = "%1",
        host = REMOTE,
        orreryId = REMOTE_CONVO,
        claudeSession = "claude-remote",
        unreachable = true,
    )

    /** A headless session on [REMOTE], which the host status alone makes unreachable. */
    private val remoteSession = pane(id = "remote-session", host = REMOTE, headless = true)

    /** A headless session on the serving host, a control that is reachable. */
    private val servingSession = pane(id = "serving-session", host = SERVING, headless = true)

    private fun step(id: String, host: String) = StepRow(
        id = id,
        task = "build",
        agent = "",
        end = "",
        status = "",
        held = false,
        started = 0L,
        ended = 0L,
        doing = "running the suite",
        checks = emptyList(),
        reason = "",
        host = host,
    )

    /** A live epic on [REMOTE], steered by the [REMOTE] conversation, with one running step on each host. */
    private val remoteEpic = EpicRow(
        id = EPIC_ID,
        repository = DIR,
        slug = "remote-epic",
        outcome = "accepted",
        changed = NOW - 30,
        live = true,
        runningSteps = listOf(step("remote-step", REMOTE), step("serving-step", SERVING)),
        endedSteps = 0,
        phaseAt = NOW - 600,
        steering = REMOTE_CONVO,
        path = DIR,
        host = REMOTE,
    )

    /** Draws the inbox's cards for the invented rows, as the inbox draws them: the tree placed from the host status. */
    private fun setInbox() {
        val projects = inboxTree(
            panes = listOf(remotePane, remoteSession, servingSession),
            archivedIds = emptySet(),
            epics = listOf(remoteEpic),
            details = emptyMap(),
            contracts = emptyList(),
            host = SERVING,
            view = TreeView.INBOX,
            hosts = hosts,
        )
        composeRule.setContent {
            Column {
                for (project in projects) {
                    ProjectCard(
                        project = project,
                        folded = false,
                        waiting = 0,
                        endedOpen = false,
                        onFold = {},
                        onEnded = {},
                        foldOpen = emptySet(),
                        onConvoFold = {},
                        convo = { convo, open, onFold ->
                            ConvoRowItem(
                                convo = convo,
                                now = NOW,
                                foldOpen = open,
                                onFold = onFold,
                                onOpen = {},
                                onLongPress = {},
                            )
                        },
                    )
                }
            }
        }
    }

    /** The nodes under the node tagged [tag] that match [matcher], read from the unmerged tree. */
    private fun under(tag: String, matcher: SemanticsMatcher) =
        composeRule.onAllNodes(matcher and hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true)

    /** The grey or state colour of each glyph named [word] under the node tagged [tag], on its colour key. */
    private fun glyphColours(tag: String, word: String): List<Color> =
        under(tag, hasContentDescription(word)).fetchSemanticsNodes().mapNotNull { it.config.getOrNull(GlyphColourKey) }

    /** The colour of each unreachable host mark's ring under the node tagged [tag], on its colour key. */
    private fun ringColours(tag: String): List<Color> =
        composeRule.onAllNodes(hasAnyAncestor(hasTestTag(tag)), useUnmergedTree = true)
            .fetchSemanticsNodes()
            .mapNotNull { it.config.getOrNull(HostRingColourKey) }

    @Test
    fun anUnreachablePaneRowDrawsTheGreyDashedRingNeverStoppedAndSaysUnreachableOnItsLine() {
        setInbox()

        assertEquals(listOf(OrreryTokens.Ink.three), glyphColours(REMOTE_PANE_ROW, UNREACHABLE))
        under(REMOTE_PANE_ROW, hasContentDescription("stopped")).assertCountEquals(0)
        under(REMOTE_PANE_ROW, hasText(UNREACHABLE)).assertCountEquals(1)
    }

    @Test
    fun aHeadlessSessionOnAnUnreachableHostDrawsTheSameAndAReachableOneDoesNot() {
        setInbox()

        assertEquals(listOf(OrreryTokens.Ink.three), glyphColours(REMOTE_SESSION_ROW, UNREACHABLE))
        under(REMOTE_SESSION_ROW, hasContentDescription("stopped")).assertCountEquals(0)
        under(REMOTE_SESSION_ROW, hasText(UNREACHABLE)).assertCountEquals(1)

        // The control: a session on the serving host keeps its own state, and says nothing of an unreachable host.
        assertEquals(listOf(OrreryTokens.State.working), glyphColours(SERVING_SESSION_ROW, "working"))
        under(SERVING_SESSION_ROW, hasText(UNREACHABLE)).assertCountEquals(0)
    }

    @Test
    fun anEpicOnAnUnreachableHostNamesItsHostWithTheGreyUnreachableMark() {
        setInbox()

        assertEquals(listOf(OrreryTokens.Ink.three), ringColours(EPIC_ROW))
        under(EPIC_ROW, hasTestTag(HOST_UNREACHABLE)).assertCountEquals(1)
        under(EPIC_ROW, hasText(REMOTE)).assertCountEquals(1)
    }

    @Test
    fun aStepOnAnUnreachableHostDrawsTheGreyRingNeverStoppedAndNamesItsHostUnreachable() {
        setInbox()

        assertEquals(listOf(OrreryTokens.Ink.three), glyphColours(REMOTE_STEP_ROW, UNREACHABLE))
        under(REMOTE_STEP_ROW, hasContentDescription("stopped")).assertCountEquals(0)
        assertEquals(listOf(OrreryTokens.Ink.three), ringColours(REMOTE_STEP_ROW))

        // The control: a step on the serving host draws its working glyph and no unreachable mark.
        assertEquals(listOf(OrreryTokens.State.working), glyphColours(SERVING_STEP_ROW, "working"))
        assertEquals(emptyList<Color>(), ringColours(SERVING_STEP_ROW))
    }

    @Test
    fun aCardHeadOnAnUnreachableHostShowsTheGreyUnreachableMark() {
        setInbox()

        assertEquals(listOf(OrreryTokens.Ink.three), ringColours(REMOTE_CARD))
        under(REMOTE_CARD, hasText(UNREACHABLE)).assertCountEquals(1)

        // The control: the serving host's card heading is drawn, and names its host with no mark.
        composeRule.onAllNodes(hasTestTag(SERVING_CARD), useUnmergedTree = true).assertCountEquals(1)
        under(SERVING_CARD, hasTestTag(HOST_UNREACHABLE)).assertCountEquals(0)
    }
}
