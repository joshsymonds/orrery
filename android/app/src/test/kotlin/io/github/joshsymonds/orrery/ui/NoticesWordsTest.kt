package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.NoticeRow
import io.github.joshsymonds.orrery.api.Pane
import java.time.LocalDate
import java.time.LocalDateTime
import java.time.ZoneId
import java.time.ZoneOffset
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class NoticesWordsTest {
    private companion object {
        const val SESSION_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        val NOW: Long = LocalDateTime.of(2026, 11, 20, 14, 0).toEpochSecond(ZoneOffset.UTC)
    }

    private fun row(
        pane: String = "",
        session: String = "",
        epic: String = "",
        limit: String = "",
        alert: String = "",
        state: String = "",
        title: String = "",
        blockKind: String = "",
    ): NoticeRow = NoticeRow(
        pane = pane,
        session = session,
        epic = epic,
        limit = limit,
        alert = alert,
        state = state,
        title = title,
        body = "",
        blockKind = blockKind,
        blockRef = "",
        time = NOW,
        read = false,
    )

    private fun epic(id: String, outcome: String, live: Boolean): EpicRow = EpicRow(
        id = id,
        repository = "example-repo",
        slug = id.substringAfter('/'),
        outcome = outcome,
        changed = NOW,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
    )

    private fun pane(
        id: String,
        cwd: String,
        state: String,
        headless: Boolean,
        orreryId: String = "",
        archived: Boolean = false,
        putOff: Boolean = false,
    ): Pane = Pane(
        id = id,
        host = "example-host",
        session = "example-session",
        window = 0,
        pane = 0,
        cwd = cwd,
        command = "claude",
        state = state,
        summary = "",
        label = "",
        changed = NOW,
        seen = 0L,
        question = "",
        questionHash = "",
        headless = headless,
        orreryId = orreryId,
        archived = archived,
        putOff = putOff,
    )

    private fun treeOf(
        vararg epics: EpicRow,
        panes: List<Pane> = emptyList(),
        archived: Set<String> = emptySet(),
    ): TreeData = TreeData(panes, archived, epics.toList(), emptyMap(), emptyList(), "")

    @Test
    fun aShippedEpicLeadsWithItsCheckGaugeSlugAndBlockWords() {
        val tree = treeOf(epic("example-repo/example-epic", outcome = "released", live = false))

        val words = noticeWordsOf(row(epic = "example-repo/example-epic", blockKind = "question"), tree, NOW)

        assertEquals(NoticeLead.Gauge(GaugeShare(0, 0, GaugeTone.OK, GaugeInner.CHECK)), words.lead)
        assertEquals("example-epic", words.name)
        assertEquals("asks you a question", words.words)
    }

    @Test
    fun anEpicEndedShortLeadsWithItsDashGauge() {
        val tree = treeOf(epic("example-repo/example-epic", outcome = "stopped", live = false))

        val words = noticeWordsOf(row(epic = "example-repo/example-epic"), tree, NOW)

        assertEquals(NoticeLead.Gauge(GaugeShare(0, 0, GaugeTone.NONE, GaugeInner.DASH)), words.lead)
    }

    @Test
    fun anEpicsBlockKindIsWordedAsTheWebWordsIt() {
        val tree = treeOf(epic("example-repo/example-epic", outcome = "released", live = false))
        val kinds = listOf("question", "release_step", "report", "contract", "")

        val words = kinds.map { kind ->
            noticeWordsOf(row(epic = "example-repo/example-epic", blockKind = kind), tree, NOW).words
        }

        assertEquals(
            listOf("asks you a question", "a release step failed", "finished", "has a contract for you", "waits on you"),
            words,
        )
    }

    @Test
    fun anUnlistedEpicLeadsWithTheBellAndTheTextAfterTheLastSlash() {
        val words = noticeWordsOf(row(epic = "example-repo/example-epic"), treeOf(), NOW)

        assertEquals(NoticeLead.Bell, words.lead)
        assertEquals("example-epic", words.name)
        assertEquals("waits on you", words.words)
    }

    @Test
    fun aListedSessionLeadsWithItsStateAndItsDirectoryLastElement() {
        val tree = treeOf(panes = listOf(pane(SESSION_ID, "/home/user/alpha/", state = "waiting", headless = true)))

        val words = noticeWordsOf(
            row(session = SESSION_ID, state = "waiting", title = "/home/user/alpha · a1b2c3d4"),
            tree,
            NOW,
        )

        assertEquals(NoticeLead.State("waiting"), words.lead)
        assertEquals("alpha", words.name)
        assertEquals("waiting on you", words.words)
        assertFalse(SESSION_ID in words.name || SESSION_ID in words.words)
    }

    @Test
    fun aListedPaneLeadsWithItsStateAndItsDirectoryLastElement() {
        val tree = treeOf(panes = listOf(pane("%7", "/home/user/beta", state = "done", headless = false)))

        val words = noticeWordsOf(row(pane = "%7", state = "done", title = "beta · %7"), tree, NOW)

        assertEquals(NoticeLead.State("done"), words.lead)
        assertEquals("beta", words.name)
        assertEquals("done", words.words)
    }

    @Test
    fun aWaitingPaneThatWaitsOnYouKeepsItsStateAndItsWords() {
        val tree = treeOf(panes = listOf(pane("%7", "/home/user/beta", state = "waiting", headless = false)))

        val words = noticeWordsOf(row(pane = "%7", state = "waiting", title = "beta · %7"), tree, NOW)

        assertEquals(NoticeLead.State("waiting"), words.lead)
        assertEquals("waiting on you", words.words)
    }

    @Test
    fun aWaitingPaneNotListedKeepsTheNoticesOwnStateAndWords() {
        val words = noticeWordsOf(row(pane = "%9", state = "waiting", title = "beta · %9"), treeOf(), NOW)

        assertEquals(NoticeLead.State("waiting"), words.lead)
        assertEquals("waiting on you", words.words)
    }

    @Test
    fun aPutOffWaitingPaneLeadsWithTheIdleStateAndSaysItWaitsOnAQuestionPutOff() {
        val tree = treeOf(panes = listOf(pane("%7", "/home/user/beta", state = "waiting", headless = false, putOff = true)))

        val words = noticeWordsOf(row(pane = "%7", state = "waiting", title = "beta · %7"), tree, NOW)

        assertEquals(NoticeLead.State("idle"), words.lead)
        assertEquals("beta", words.name)
        assertEquals("waiting on a question you put off", words.words)
    }

    @Test
    fun anArchivedWaitingHeadlessSessionLeadsWithTheIdleStateAndItsWord() {
        val tree = treeOf(panes = listOf(pane(SESSION_ID, "/home/user/alpha", state = "waiting", headless = true, archived = true)))

        val words = noticeWordsOf(row(session = SESSION_ID, state = "waiting", title = "/home/user/alpha · $SESSION_ID"), tree, NOW)

        assertEquals(NoticeLead.State("idle"), words.lead)
        assertEquals("idle", words.words)
    }

    @Test
    fun anArchivedWaitingTmuxPaneLeadsWithTheIdleStateAndItsWord() {
        val orreryId = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        val tree = treeOf(
            panes = listOf(pane("%7", "/home/user/beta", state = "waiting", headless = false, orreryId = orreryId)),
            archived = setOf(orreryId),
        )

        val words = noticeWordsOf(row(pane = "%7", state = "waiting", title = "beta · %7"), tree, NOW)

        assertEquals(NoticeLead.State("idle"), words.lead)
        assertEquals("idle", words.words)
    }

    @Test
    fun aDoneNoticeWhosePaneIsWaitingAndPutOffKeepsItsStateAndWords() {
        val tree = treeOf(panes = listOf(pane("%7", "/home/user/beta", state = "waiting", headless = false, putOff = true)))

        val words = noticeWordsOf(row(pane = "%7", state = "done", title = "beta · %7"), tree, NOW)

        assertEquals(NoticeLead.State("done"), words.lead)
        assertEquals("done", words.words)
    }

    @Test
    fun aPaneBothArchivedAndPutOffReadsPutOff() {
        val orreryId = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        val tree = treeOf(
            panes = listOf(
                pane("%7", "/home/user/beta", state = "waiting", headless = false, orreryId = orreryId, archived = true, putOff = true),
            ),
            archived = setOf(orreryId),
        )
        val listed = tree.panes.single()
        assertTrue(archivedOf(listed, tree.archived))

        val words = noticeWordsOf(row(pane = "%7", state = "waiting", title = "beta · %7"), tree, NOW)

        assertEquals(NoticeLead.State("idle"), words.lead)
        assertEquals("waiting on a question you put off", words.words)
    }

    @Test
    fun anUnlistedSessionNamesItsTitleBeforeTheSeparatorAndNeverItsId() {
        val words = noticeWordsOf(
            row(session = SESSION_ID, state = "failed", title = "/home/user/gamma · $SESSION_ID"),
            treeOf(),
            NOW,
        )

        assertEquals(NoticeLead.State("failed"), words.lead)
        assertEquals("/home/user/gamma", words.name)
        assertEquals("failed", words.words)
        assertFalse(SESSION_ID in words.name || SESSION_ID in words.words)
    }

    @Test
    fun aSessionStateIsWordedAsTheSystemStateTableWordsIt() {
        val states = listOf("waiting", "done", "failed", "interrupted", "stopped")

        val words = states.map { state ->
            noticeWordsOf(row(session = SESSION_ID, state = state, title = "/home/user/gamma · $SESSION_ID"), treeOf(), NOW).words
        }

        assertEquals(listOf("waiting on you", "done", "failed", "cut off", "stopped"), words)
    }

    @Test
    fun aLimitLeadsWithTheClockAndItsTitle() {
        val words = noticeWordsOf(row(limit = "five_hour@1700000000", title = "Limit reached"), treeOf(), NOW)

        assertEquals(NoticeLead.Clock, words.lead)
        assertEquals("Limit reached", words.name)
        assertEquals("", words.words)
    }

    @Test
    fun anAlertLeadsWithTheBellAndItsTitle() {
        val words = noticeWordsOf(row(alert = "disk", title = "Disk alert"), treeOf(), NOW)

        assertEquals(NoticeLead.Bell, words.lead)
        assertEquals("Disk alert", words.name)
        assertEquals("", words.words)
    }

    @Test
    fun dayHeadsAreTodayYesterdayThenTheWeekdayAndDate() {
        val today = LocalDate.of(2026, 11, 20)

        assertEquals("Today", dayHeadOf(today, today))
        assertEquals("Yesterday", dayHeadOf(today.minusDays(1), today))
        assertEquals("Monday 16 November", dayHeadOf(LocalDate.of(2026, 11, 16), today))
    }

    @Test
    fun theClockIsHoursAndMinutesInTheGivenZone() {
        val time = LocalDateTime.of(2026, 11, 16, 14, 31).toEpochSecond(ZoneOffset.UTC)

        assertEquals("14:31", noticeClockOf(time, ZoneOffset.UTC))
        assertEquals("23:31", noticeClockOf(time, ZoneId.of("Asia/Tokyo")))
    }
}
