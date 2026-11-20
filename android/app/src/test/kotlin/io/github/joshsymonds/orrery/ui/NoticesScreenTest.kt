package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.NoticeRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListedNotice
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeBlock
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesReadAllResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.time.LocalDateTime
import java.time.ZoneOffset
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class NoticesScreenTest {
    private companion object {
        val NOW: Long = LocalDateTime.of(2026, 11, 20, 14, 0).toEpochSecond(ZoneOffset.UTC)
        const val ALPHA = "/home/user/alpha"
        const val BETA = "/home/user/beta"
        const val PANE_ID = "%7"
        const val EPIC_ID = "example-repo/example-epic"
        const val SESSION_ID = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
        const val LIMIT_KEY = "five_hour@1700000000"
        const val NOTICES_LIST = "/orrery.v1.Notices/List"
        const val NOTICES_SEEN = "/orrery.v1.Notices/Seen"
        const val NOTICES_READ_ALL = "/orrery.v1.Notices/ReadAll"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val UNREAD_WORDS = "unread"
        const val SIXTY_SECONDS = 60_000L
        const val NO_NOTICES = "No notices."
        const val NOTHING_COLLECTS = "Questions, endings and failures from sessions and epics you are not looking at collect here."
        const val FAILED = "The notices could not be read."
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val opened = mutableListOf<NoticeRow>()
    private var backed = 0

    private fun notice(
        keyOf: (NoticeKey.Builder) -> Unit,
        state: String,
        title: String,
        time: Long,
        read: Boolean,
        blockKind: String = "",
    ): ListedNotice {
        val key = NoticeKey.newBuilder().also(keyOf).build()
        return ListedNotice.newBuilder()
            .setKey(key)
            .setState(state)
            .setTitle(title)
            .setBody("")
            .setBlock(NoticeBlock.newBuilder().setKind(blockKind))
            .setTime(time)
            .setRead(read)
            .build()
    }

    /** The history: a waiting pane and a limit today, an epic yesterday, a session on Monday 16 November. */
    private fun noticesBody(allRead: Boolean = false): NoticesListResponse = NoticesListResponse.newBuilder()
        .addNotices(
            notice({ it.setPane(PANE_ID) }, "waiting", "$ALPHA · $PANE_ID", NOW - 600, allRead, blockKind = "question"),
        )
        .addNotices(notice({ it.setLimit(LIMIT_KEY) }, "limited", "Limit reached", NOW - 1200, read = true))
        .addNotices(
            notice({ it.setEpic(EPIC_ID) }, "waiting", "example-repo · example-epic", NOW - 86_400 - 60, allRead, "question"),
        )
        .addNotices(
            notice(
                { it.setSession(SESSION_ID) },
                "done",
                "$BETA · $SESSION_ID",
                LocalDateTime.of(2026, 11, 16, 14, 31).toEpochSecond(ZoneOffset.UTC),
                read = true,
            ),
        )
        .build()

    private fun panesBody(): PanesListResponse = PanesListResponse.newBuilder()
        .addPanes(
            PaneProto.newBuilder()
                .setId(PANE_ID)
                .setHost("example-host")
                .setCwd(ALPHA)
                .setState("waiting")
                .setChanged(NOW - 600),
        )
        .build()

    private fun headlessBody(): ListHeadlessResponse = ListHeadlessResponse.newBuilder()
        .addSessions(
            HeadlessSession.newBuilder()
                .setId(SESSION_ID)
                .setDir(BETA)
                .setLabel("beta-agent")
                .setState("done")
                .setChanged(LocalDateTime.of(2026, 11, 16, 14, 31).toEpochSecond(ZoneOffset.UTC)),
        )
        .build()

    private fun epicsBody(): EpicsListResponse = EpicsListResponse.newBuilder()
        .addEpics(
            Epic.newBuilder()
                .setId(EPIC_ID)
                .setRepository("example-repo")
                .setSlug("example-epic")
                .setOutcome("released")
                .setChanged(NOW - 86_400)
                .setLive(false),
        )
        .build()

    /** Serves the history and the tree it leads from. Procedures not served answer not found, which the tree reads as none. */
    private fun serve(server: FakeOrreryServer, notices: NoticesListResponse = noticesBody()) {
        server.respond(NOTICES_LIST, notices)
        server.respond(NOTICES_SEEN, NoticesSeenResponse.getDefaultInstance())
        server.respond(NOTICES_READ_ALL, NoticesReadAllResponse.getDefaultInstance())
        server.respond(PANES_LIST, panesBody())
        server.respond(LIST_HEADLESS, headlessBody())
        server.respond(EPICS_LIST, epicsBody())
    }

    private fun setScreen(server: FakeOrreryServer, owner: LifecycleOwner? = null) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides (owner ?: LocalLifecycleOwner.current)) {
                NoticesScreen(
                    client = client,
                    onBack = { backed++ },
                    onOpen = { opened.add(it) },
                    now = { NOW },
                    zone = ZoneOffset.UTC,
                )
            }
        }
    }

    /** A lifecycle the test moves by hand, as the activity's would move. */
    private class HandLifecycleOwner : LifecycleOwner {
        private val registry = LifecycleRegistry(this)
        override val lifecycle: LifecycleRegistry get() = registry
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun requestsTo(server: FakeOrreryServer, path: String) = server.requests.filter { it.rawPath == path }

    private fun topOf(text: String): Float = composeRule.onNodeWithText(text).fetchSemanticsNode().boundsInRoot.top

    private fun awaitText(text: String) {
        composeRule.awaitUntil("$text is shown") { countNodes(hasText(text)) > 0 }
    }

    /** Lets the main thread and every request it started finish, so a Seen or ReadAll the screen sent is on the server. */
    private fun settle() {
        Quiescence.awaitUntil("the screen's work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    @Test
    fun theHistoryIsGroupedByDayNewestFirstWithItsDayHeads() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Monday 16 November")

            assertTrue(topOf("Today") < topOf("alpha"))
            assertTrue(topOf("alpha") < topOf("Limit reached"))
            assertTrue(topOf("Limit reached") < topOf("Yesterday"))
            assertTrue(topOf("Yesterday") < topOf("example-epic"))
            assertTrue(topOf("example-epic") < topOf("Monday 16 November"))
            assertTrue(topOf("Monday 16 November") < topOf("beta"))
        }
    }

    @Test
    fun aKeyListedTwiceShowsBothRows() {
        // The history keeps a row per delivery: a pane that asks again after its first notice was read lists twice.
        val twice = NoticesListResponse.newBuilder()
            .addNotices(notice({ it.setPane(PANE_ID) }, "waiting", "$ALPHA · $PANE_ID", NOW - 600, read = false))
            .addNotices(notice({ it.setPane(PANE_ID) }, "done", "$ALPHA · $PANE_ID", NOW - 1200, read = true))
            .build()
        FakeOrreryServer().use { server ->
            serve(server, twice)
            setScreen(server)
            awaitText("waiting on you")

            assertEquals(1, countNodes(hasText("waiting on you")))
            assertEquals(1, countNodes(hasText("done")))
        }
    }

    @Test
    fun aRowIsLedAndWordedAsTheWebWordsItAndShowsItsClock() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Monday 16 November")

            // An epic leads with its gauge, which its inbox card draws, then its slug and its block's words.
            composeRule.onNodeWithTag("gauge:$EPIC_ID", useUnmergedTree = true).assertExists()
            assertEquals(1, countNodes(hasText("asks you a question")))
            // A pane leads with its state's glyph, then its directory's last element and its state's words.
            assertEquals(1, countNodes(hasText("waiting on you")))
            assertEquals(1, countNodes(hasText("13:50")))
            assertEquals(1, countNodes(hasText("13:40")))
            assertEquals(1, countNodes(hasText("13:59")))
            assertEquals(1, countNodes(hasText("14:31")))
        }
    }

    @Test
    fun aSessionIsNamedByItsDirectoryAndNeverShowsItsId() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Monday 16 November")

            assertEquals(1, countNodes(hasText("beta")))
            assertEquals(0, countNodes(hasText(SESSION_ID, substring = true)))
        }
    }

    @Test
    fun unreadRowsAreMarkedAndReadRowsAreNot() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Monday 16 November")

            // The pane and the epic are unread; the limit and the session are read.
            assertEquals(2, countNodes(hasContentDescription(UNREAD_WORDS)))
        }
    }

    @Test
    fun aTapSendsSeenForTheRowAndOpensIt() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("alpha")

            composeRule.onNodeWithText("alpha").performClick()
            composeRule.awaitUntil("the pane is marked seen") { requestsTo(server, NOTICES_SEEN).isNotEmpty() }
            settle()

            assertEquals(PANE_ID, NoticesSeenRequest.parseFrom(requestsTo(server, NOTICES_SEEN).single().body).key.pane)
            assertEquals(listOf(PANE_ID), opened.map { it.pane })
        }
    }

    @Test
    fun aTapOnALimitRowSendsSeenAndOpensNothing() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Limit reached")

            composeRule.onNodeWithText("Limit reached").performClick()
            composeRule.awaitUntil("the limit is marked seen") { requestsTo(server, NOTICES_SEEN).isNotEmpty() }
            settle()

            assertEquals(LIMIT_KEY, NoticesSeenRequest.parseFrom(requestsTo(server, NOTICES_SEEN).single().body).key.limit)
            assertEquals(0, opened.size)
        }
    }

    @Test
    fun markAllReadIsShownWhileARowIsUnreadAndSendsReadAll() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Monday 16 November")

            composeRule.onNodeWithText("Mark all read").performClick()
            composeRule.awaitUntil("ReadAll is sent") { requestsTo(server, NOTICES_READ_ALL).isNotEmpty() }
            settle()

            assertEquals(1, requestsTo(server, NOTICES_READ_ALL).size)
        }
    }

    @Test
    fun markAllReadIsNotShownWhenEveryRowIsRead() {
        FakeOrreryServer().use { server ->
            serve(server, noticesBody(allRead = true))
            setScreen(server)
            awaitText("Monday 16 November")

            assertEquals(0, countNodes(hasText("Mark all read")))
        }
    }

    @Test
    fun theListIsReadOnStartAndEverySixtySecondsOnlyWhileStarted() {
        val owner = HandLifecycleOwner()
        owner.lifecycle.currentState = Lifecycle.State.STARTED
        FakeOrreryServer().use { server ->
            serve(server)
            composeRule.mainClock.autoAdvance = false
            setScreen(server, owner = owner)
            // The clock stands still, so no frame draws the rows: the read is awaited on the server, then settled.
            composeRule.awaitUntil("the list is read") { requestsTo(server, NOTICES_LIST).size == 1 }
            settle()
            assertEquals(1, requestsTo(server, NOTICES_LIST).size)

            // Frames would step past the minute, so the clock moves by the exact span.
            composeRule.mainClock.advanceTimeBy(SIXTY_SECONDS - 1, ignoreFrameDuration = true)
            settle()
            assertEquals(1, requestsTo(server, NOTICES_LIST).size)
            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            composeRule.awaitUntil("the list is read again") { requestsTo(server, NOTICES_LIST).size == 2 }
            settle()

            composeRule.runOnIdle { owner.lifecycle.currentState = Lifecycle.State.CREATED }
            composeRule.mainClock.advanceTimeBy(SIXTY_SECONDS * 3, ignoreFrameDuration = true)
            settle()
            assertEquals(2, requestsTo(server, NOTICES_LIST).size)
        }
    }

    @Test
    fun aSeenAndAReadAllReadTheListAgain() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("alpha")
            assertEquals(1, requestsTo(server, NOTICES_LIST).size)

            composeRule.onNodeWithText("alpha").performClick()
            composeRule.awaitUntil("the list is read after Seen") { requestsTo(server, NOTICES_LIST).size == 2 }
            settle()

            composeRule.onNodeWithText("Mark all read").performClick()
            composeRule.awaitUntil("the list is read after ReadAll") { requestsTo(server, NOTICES_LIST).size == 3 }
            settle()
        }
    }

    @Test
    fun aFailedReadSaysTheNoticesCouldNotBeRead() {
        FakeOrreryServer().use { server ->
            server.fail(NOTICES_LIST, Code.UNAVAILABLE, "synthetic outage")
            setScreen(server)
            awaitText(FAILED)

            assertEquals(0, countNodes(hasText(NO_NOTICES)))
        }
    }

    @Test
    fun anEmptyHistorySaysWhatCollectsThere() {
        FakeOrreryServer().use { server ->
            serve(server, NoticesListResponse.getDefaultInstance())
            setScreen(server)
            awaitText(NO_NOTICES)

            assertEquals(1, countNodes(hasText(NOTHING_COLLECTS)))
        }
    }

    @Test
    fun backReturnsToTheScreenBehind() {
        FakeOrreryServer().use { server ->
            serve(server)
            setScreen(server)
            awaitText("Monday 16 November")

            composeRule.onNodeWithText("Back").performClick()

            assertEquals(1, backed)
        }
    }
}
