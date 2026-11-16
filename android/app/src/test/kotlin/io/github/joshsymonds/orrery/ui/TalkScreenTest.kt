package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone
import java.util.concurrent.atomic.AtomicReference

private const val EPIC = "synthetic-epic"
private const val REPOSITORY = "/synthetic/orrery"
private const val START_PATH = "/orrery.v1.Control/Start"
private const val ANSWERED_ID = "fedcba9876543210fedcba9876543210"
private const val REFUSAL = "synthetic refusal reason"
private const val NEW_SESSION = "New session in orrery"
private const val PANE_ID = "%7"
private const val PANE_SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val BARE_SESSION_ID = "aaaaaaaa11112222333344445555666"
// 2023-11-14 22:13:20 UTC, and a minute later; the zone is pinned to UTC for the clock text.
private const val AT = 1_700_000_000L
private const val LATER = AT + 60L
private val ID_FORM = Regex("^[0-9a-f]{32}$")

private val PANE_SESSION = TalkSession(
    id = PANE_SESSION_ID,
    pane = PANE_ID,
    label = "synthetic pane",
    call = "synthetic call one",
    at = AT,
)
private val BARE_SESSION = TalkSession(
    id = BARE_SESSION_ID,
    pane = "",
    label = "",
    call = "synthetic call two",
    at = LATER,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class TalkScreenTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val server = FakeOrreryServer()

    private val opened = AtomicReference<TalkTarget?>(null)

    private var zone: TimeZone? = null

    @Before
    fun pinZone() {
        zone = TimeZone.getDefault()
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun tearDown() {
        zone?.let(TimeZone::setDefault)
        server.close()
    }

    private fun setScreen(sessions: List<TalkSession>, repository: String) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                TalkScreen(
                    epicId = EPIC,
                    sessions = sessions,
                    repository = repository,
                    client = client,
                    onOpen = { opened.set(it) },
                )
            }
        }
    }

    @Test
    fun talkPromptIsTheFixedFirstPrompt() {
        assertEquals(
            "This session is about orrery epic synthetic-epic. Read it with epic_get, then wait for my words.",
            talkPrompt(EPIC),
        )
    }

    @Test
    fun eachSessionRowShowsItsLabelCallAndTime() {
        setScreen(listOf(PANE_SESSION, BARE_SESSION), repository = "")

        val rows = composeRule.onAllNodesWithTag("talk-session").fetchSemanticsNodes()
        assertEquals(2, rows.size)
        composeRule.onAllNodesWithTag("talk-session")[0].assertTextEquals("synthetic pane · synthetic call one · 22:13")
        composeRule.onAllNodesWithTag("talk-session")[1].assertTextEquals("aaaaaaaa · synthetic call two · 22:14")
    }

    @Test
    fun tappingASessionRowOpensItsPaneOrSession() {
        setScreen(listOf(PANE_SESSION, BARE_SESSION), repository = "")

        composeRule.onAllNodesWithTag("talk-session")[0].performClick()
        composeRule.awaitUntil { opened.get() != null }
        assertEquals(TalkTarget(TalkKind.PANE, PANE_ID, "synthetic pane"), opened.get())

        opened.set(null)
        composeRule.onAllNodesWithTag("talk-session")[1].performClick()
        composeRule.awaitUntil { opened.get() != null }
        assertEquals(TalkTarget(TalkKind.SESSION, BARE_SESSION_ID, "aaaaaaaa"), opened.get())
        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun newSessionPostsStartInTheRepositoryAndOpensTheAnsweredId() {
        server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
        setScreen(listOf(PANE_SESSION), REPOSITORY)

        composeRule.onNodeWithTag("talk-new").assertTextEquals(NEW_SESSION)
        composeRule.onNodeWithTag("talk-new").performClick()
        composeRule.awaitUntil { opened.get() != null }

        val request = server.requests.single()
        assertEquals(START_PATH, request.rawPath)
        val start = StartRequest.parseFrom(request.body)
        assertEquals("/synthetic/orrery", start.dir)
        assertEquals(talkPrompt(EPIC), start.prompt)
        assertTrue(ID_FORM.matches(start.id))
        assertEquals(TalkTarget(TalkKind.SESSION, ANSWERED_ID, "orrery"), opened.get())
    }

    @Test
    fun aRefusedStartShowsItsReasonAndOpensNothing() {
        server.fail(START_PATH, Code.FAILED_PRECONDITION, REFUSAL)
        setScreen(emptyList(), REPOSITORY)

        composeRule.onNodeWithTag("talk-new").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithTag("talk-error").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("talk-error").assertTextEquals(REFUSAL)
        assertEquals(1, server.requests.size)
        assertEquals(null, opened.get())
    }

    @Test
    fun aRefusedStartRetriedPostsTheSameId() {
        server.fail(START_PATH, Code.FAILED_PRECONDITION, REFUSAL)
        setScreen(emptyList(), REPOSITORY)

        composeRule.onNodeWithTag("talk-new").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithTag("talk-error").fetchSemanticsNodes().isNotEmpty()
        }
        server.respond(START_PATH, StartResponse.newBuilder().setId(ANSWERED_ID).build())
        composeRule.onNodeWithTag("talk-new").performClick()
        composeRule.awaitUntil { opened.get() != null }

        val ids = server.requests.map { StartRequest.parseFrom(it.body).id }
        assertEquals(2, ids.size)
        assertEquals(ids[0], ids[1])
        assertEquals(TalkTarget(TalkKind.SESSION, ANSWERED_ID, "orrery"), opened.get())
    }

    @Test
    fun noRepositoryShowsNoNewSessionRow() {
        setScreen(listOf(PANE_SESSION), repository = "")

        composeRule.onAllNodesWithTag("talk-session").fetchSemanticsNodes().single()
        assertTrue(composeRule.onAllNodesWithTag("talk-new").fetchSemanticsNodes().isEmpty())
    }
}
