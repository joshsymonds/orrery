package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Quiescence
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ListedNotice
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeBlock
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class PaneListNoticesTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val HOST = "example-host"
        const val HOST_STATUS = "/orrery.v1.Host/Status"
        const val PANES_LIST = "/orrery.v1.Panes/List"
        const val LIST_HEADLESS = "/orrery.v1.Sessions/ListHeadless"
        const val EPICS_LIST = "/orrery.v1.Epics/List"
        const val NOTICES_LIST = "/orrery.v1.Notices/List"
        const val SIXTY_SECONDS = 60_000L
        const val BELL = "open-notices"
        const val UNREAD = "notices-unread"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var opened = 0

    /** A limit notice, which the home's bell counts as unread when [read] is false. */
    private fun limit(index: Int, read: Boolean): ListedNotice = ListedNotice.newBuilder()
        .setKey(NoticeKey.newBuilder().setLimit("five_hour@${NOW + index}"))
        .setState("limited")
        .setTitle("Limit reached")
        .setBody("")
        .setBlock(NoticeBlock.getDefaultInstance())
        .setTime(NOW - index)
        .setRead(read)
        .build()

    private fun noticesOf(vararg read: Boolean): NoticesListResponse {
        val response = NoticesListResponse.newBuilder()
        read.forEachIndexed { index, isRead -> response.addNotices(limit(index, isRead)) }
        return response.build()
    }

    /** Serves what the home reads: the host, the lists, and the history the bell counts. */
    private fun serve(server: FakeOrreryServer, notices: NoticesListResponse) {
        server.respond(HOST_STATUS, HostStatusResponse.newBuilder().setHost(HOST).build())
        server.respond(PANES_LIST, PanesListResponse.getDefaultInstance())
        server.respond(LIST_HEADLESS, ListHeadlessResponse.getDefaultInstance())
        server.respond(EPICS_LIST, EpicsListResponse.getDefaultInstance())
        server.respond(NOTICES_LIST, notices)
    }

    private fun setScreen(server: FakeOrreryServer, owner: LifecycleOwner? = null) {
        // Made once, outside the composition, so a recomposition does not hand the screen a client it has not read with.
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalLifecycleOwner provides (owner ?: LocalLifecycleOwner.current)) {
                PaneListScreen(
                    client = client,
                    onOpenPane = {},
                    onOpenPaneId = {},
                    onStartedSession = {},
                    onOpenSettings = {},
                    onOpenNotices = { opened++ },
                    now = { NOW },
                )
            }
        }
    }

    private fun countNodes(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun requestsTo(server: FakeOrreryServer, path: String) = server.requests.filter { it.rawPath == path }

    private fun boundsOfTag(tag: String) = composeRule.onNodeWithTag(tag, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot

    private fun boundsOfText(text: String) = composeRule.onNodeWithText(text).fetchSemanticsNode().boundsInRoot

    /** Waits until the bell's count reads [text] and nothing else. */
    private fun awaitUnread(text: String) {
        composeRule.awaitUntil("the bell reads $text") { countNodes(hasTestTag(UNREAD) and hasText(text)) == 1 }
    }

    /** Lets the main thread and every request it started finish, so the list the screen read has answered. */
    private fun settle() {
        Quiescence.awaitUntil("the screen's work settles", step = { composeRule.waitForIdle() }) {
            Quiescence.inFlight() == null
        }
    }

    @Test
    fun theBellSitsBetweenPoolsAndSettingsAndCarriesTheUnreadCount() {
        FakeOrreryServer().use { server ->
            serve(server, noticesOf(false, true, false))
            setScreen(server)
            awaitUnread("2")

            composeRule.onNodeWithTag(UNREAD, useUnmergedTree = true).assertTextEquals("2")
            composeRule.onNodeWithText("New").assertExists()
            val bell = boundsOfTag(BELL)
            assertTrue("the bell sits after Pools", boundsOfText("Pools").right <= bell.left)
            assertTrue("the bell sits before Settings", bell.right <= boundsOfText("Settings").left)
            assertEquals(boundsOfText("Settings").center.y, bell.center.y, 1f)
        }
    }

    @Test
    fun theBellShowsNoNumberWhenEveryNoticeIsRead() {
        FakeOrreryServer().use { server ->
            serve(server, noticesOf(true, true))
            setScreen(server)
            composeRule.awaitUntil("the list is read") { requestsTo(server, NOTICES_LIST).size == 1 }
            settle()

            composeRule.onNodeWithTag(BELL).assertExists()
            assertEquals(0, countNodes(hasTestTag(UNREAD)))
        }
    }

    @Test
    fun theHomeDrawsNoNoticeRows() {
        FakeOrreryServer().use { server ->
            serve(server, noticesOf(false))
            setScreen(server)
            awaitUnread("1")

            assertEquals(0, countNodes(hasText("Limit reached")))
        }
    }

    @Test
    fun tappingTheBellOpensNotices() {
        FakeOrreryServer().use { server ->
            serve(server, noticesOf(false))
            setScreen(server)
            awaitUnread("1")

            composeRule.onNodeWithTag(BELL).performClick()

            assertEquals(1, opened)
        }
    }

    @Test
    fun theListIsReadOnStartAndEverySixtySecondsOnlyWhileStarted() {
        val owner = HandLifecycleOwner()
        owner.lifecycle.currentState = Lifecycle.State.STARTED
        FakeOrreryServer().use { server ->
            serve(server, noticesOf(false))
            composeRule.mainClock.autoAdvance = false
            setScreen(server, owner = owner)
            // The clock stands still, so no frame draws the bell: the read is awaited on the server, then settled.
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
}
