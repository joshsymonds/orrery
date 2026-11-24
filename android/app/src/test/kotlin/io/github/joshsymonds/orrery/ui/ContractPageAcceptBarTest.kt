package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import io.github.joshsymonds.orrery.api.ContractLeftRow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ContractStateRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordMakerRow
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractStateChange
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.RecordMaker
import io.github.joshsymonds.orrery.mainThreadComposeRule
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** The callbacks the bar was given, each counting the presses it heard. Every press runs on the test's main thread. */
private class Taps {
    var accepts = 0
    val setAsides = mutableListOf<String>()
    var reopens = 0
    var talks = 0
    var openEpics = 0
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class ContractPageAcceptBarTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private companion object {
        const val CONTRACT_ID = "synthetic-repo/synthetic-contract"
        const val CONTRACTS_GET_PATH = "/orrery.v1.Contracts/Get"
        const val VERSION = 3L
        const val LEFT_NOTE = "2 left: pick the store; confirm the cache. Each can be said in the conversation."
        const val READY_NOTE = "Nothing is left to settle. Accept here, or say so in the conversation."
        const val ACCEPTING = "accepting…"
        const val REASON = "example refusal from orreryd"
        const val ACCEPTED_LINE = "Accepted, and frozen at v3"
        const val SET_ASIDE_LINE = "Set aside"
        const val WORDS_LABEL = "WHY, IN YOUR WORDS"
    }

    private fun contract(state: String): ContractRow = ContractRow(
        id = CONTRACT_ID,
        repository = "synthetic-repo",
        title = "synthetic contract",
        care = "limited",
        state = state,
        version = VERSION,
        openDecisions = 0L,
        conversation = "sess-synthetic",
        created = 1L,
        changed = 2L,
    )

    private fun change(
        state: String,
        by: String = "owner",
        words: String = "",
        maker: RecordMakerRow? = null,
    ): ContractStateRow = ContractStateRow(
        state = state,
        version = VERSION,
        by = by,
        session = "",
        words = words,
        time = 4102444000L,
        maker = maker,
    )

    private fun leftItems(vararg texts: String): List<ContractLeftRow> = texts.map { ContractLeftRow(text = it) }

    private fun setBar(
        state: String = "proposed",
        left: List<ContractLeftRow> = emptyList(),
        states: List<ContractStateRow> = emptyList(),
        send: BarSend = BarSend.Idle,
        talk: Boolean = false,
        taps: Taps = Taps(),
    ) {
        val talkPress: (() -> Unit)? = if (talk) ({ taps.talks++ }) else null
        composeRule.setContent {
            MaterialTheme {
                ContractAcceptBar(
                    contract = contract(state),
                    left = left,
                    states = states,
                    send = send,
                    onAccept = { taps.accepts++ },
                    onSetAside = { taps.setAsides += it },
                    onReopen = { taps.reopens++ },
                    onTalk = talkPress,
                    onOpenEpic = { taps.openEpics++ },
                )
            }
        }
    }

    /**
     * Presses the sheet's control tagged [tag] through its click action. The set-aside sheet slides up as it opens, so a
     * touch at the control's place on the screen can miss it; the action reaches the control wherever it is drawn.
     */
    private fun pressOnSheet(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    @Test
    fun `the bar's root carries its tag`() {
        setBar()

        composeRule.onNodeWithTag(ACCEPT_BAR_TAG).assertIsDisplayed()
    }

    @Test
    fun `a proposed contract with left items names them and disables Accept`() {
        setBar(left = leftItems("pick the store", "confirm the cache"))

        composeRule.onNodeWithText(LEFT_NOTE).assertIsDisplayed()
        composeRule.onAllNodesWithText(READY_NOTE).assertCountEquals(0)
        composeRule.onNodeWithTag(ACCEPT_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(ACCEPT_TAG).assertTextEquals("Accept v3")
    }

    @Test
    fun `a proposed contract with nothing left enables Accept, which calls onAccept once`() {
        val taps = Taps()
        setBar(taps = taps)

        composeRule.onNodeWithText(READY_NOTE).assertIsDisplayed()
        composeRule.onNodeWithTag(ACCEPT_TAG).assertIsEnabled()
        composeRule.onNodeWithTag(ACCEPT_TAG).performClick()

        assertEquals(1, taps.accepts)
        assertEquals(emptyList<String>(), taps.setAsides)
        assertEquals(0, taps.reopens)
    }

    @Test
    fun `Talk about it is absent when no talk is given`() {
        setBar(talk = false)

        composeRule.onAllNodesWithTag(TALK_TAG).assertCountEquals(0)
    }

    @Test
    fun `Talk about it calls onTalk once`() {
        val taps = Taps()
        setBar(talk = true, taps = taps)

        composeRule.onNodeWithText("Talk about it").performClick()

        assertEquals(1, taps.talks)
        assertEquals(0, taps.accepts)
    }

    @Test
    fun `Set aside opens the form, and Set aside stays disabled until words are typed`() {
        setBar()

        composeRule.onNodeWithTag(SET_ASIDE_TAG).performClick()

        composeRule.onNodeWithText(WORDS_LABEL, ignoreCase = true).assertIsDisplayed()
        composeRule.onNodeWithTag(SET_ASIDE_CONFIRM_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(SET_ASIDE_WORDS_TAG).performTextInput("too early for this")
        composeRule.onNodeWithTag(SET_ASIDE_CONFIRM_TAG).assertIsEnabled()
    }

    @Test
    fun `Set aside with words calls onSetAside with them once and closes the form`() {
        val taps = Taps()
        setBar(taps = taps)

        composeRule.onNodeWithTag(SET_ASIDE_TAG).performClick()
        composeRule.onNodeWithTag(SET_ASIDE_WORDS_TAG).performTextInput("too early for this")
        pressOnSheet(SET_ASIDE_CONFIRM_TAG)

        assertEquals(listOf("too early for this"), taps.setAsides)
        assertEquals(0, taps.accepts)
        composeRule.awaitNone(hasTestTag(SET_ASIDE_WORDS_TAG))
    }

    @Test
    fun `Keep it proposed closes the form without setting the contract aside`() {
        val taps = Taps()
        setBar(taps = taps)

        composeRule.onNodeWithTag(SET_ASIDE_TAG).performClick()
        composeRule.onNodeWithTag(SET_ASIDE_WORDS_TAG).performTextInput("not yet")
        pressOnSheet(SET_ASIDE_KEEP_TAG)

        composeRule.awaitNone(hasTestTag(SET_ASIDE_WORDS_TAG))
        composeRule.onNodeWithTag(SET_ASIDE_TAG).assertIsEnabled()
        assertEquals(emptyList<String>(), taps.setAsides)
    }

    @Test
    fun `a sending accept shows accepting and disables Accept`() {
        setBar(send = BarSend.Sending)

        composeRule.onNodeWithText(ACCEPTING).assertIsDisplayed()
        composeRule.onNodeWithTag(ACCEPT_TAG).assertIsNotEnabled()
    }

    @Test
    fun `a failed accept shows its reason and enables Accept, which calls onAccept once`() {
        val taps = Taps()
        setBar(send = BarSend.Failed(REASON), taps = taps)

        composeRule.onNodeWithText(REASON).assertIsDisplayed()
        composeRule.onAllNodesWithText(READY_NOTE).assertCountEquals(0)
        composeRule.onNodeWithTag(ACCEPT_TAG).assertIsEnabled()
        composeRule.onNodeWithTag(ACCEPT_TAG).performClick()

        assertEquals(1, taps.accepts)
    }

    @Test
    fun `an accepted contract names the Director as its maker, with its words, and Open its epic calls onOpenEpic once`() {
        val taps = Taps()
        setBar(
            state = "accepted",
            states = listOf(
                change(
                    state = "accepted",
                    by = "session",
                    words = "ship it",
                    maker = RecordMakerRow(kind = "conversation", reason = "the owner delegated it"),
                ),
            ),
            taps = taps,
        )

        composeRule.onNodeWithText(ACCEPTED_LINE).assertIsDisplayed()
        composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).assertTextEquals("the Director")
        assertEquals("director", composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).fetchSemanticsNode().config[MakerKey])
        composeRule.onNodeWithText("ship it").assertIsDisplayed()
        composeRule.onAllNodesWithTag(ACCEPT_TAG).assertCountEquals(0)
        composeRule.onNodeWithTag(OPEN_EPIC_TAG).performClick()

        assertEquals(1, taps.openEpics)
        assertEquals(0, taps.reopens)
    }

    @Test
    fun `the newest accept names its own maker, not an older one`() {
        setBar(
            state = "accepted",
            states = listOf(
                change(state = "accepted", by = "session", words = "older", maker = RecordMakerRow(kind = "conversation")),
                change(state = "accepted", by = "owner", words = "newer", maker = RecordMakerRow(kind = "owner_serve")),
            ),
        )

        composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).assertTextEquals("you")
        assertEquals("you", composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).fetchSemanticsNode().config[MakerKey])
        composeRule.onNodeWithText("newer").assertIsDisplayed()
        composeRule.onAllNodesWithText("older").assertCountEquals(0)
    }

    @Test
    fun `an accept with no maker is attributed to orreryd`() {
        setBar(state = "accepted", states = listOf(change(state = "accepted", by = "session")))

        composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).assertTextEquals("orreryd")
        assertEquals("orreryd", composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).fetchSemanticsNode().config[MakerKey])
    }

    @Test
    fun `a set-aside contract names its maker and words, and Reopen calls onReopen once`() {
        val taps = Taps()
        setBar(
            state = "set_aside",
            states = listOf(
                change(
                    state = "set_aside",
                    by = "owner",
                    words = "not now",
                    maker = RecordMakerRow(kind = "owner_conversation", quote = "not now"),
                ),
            ),
            taps = taps,
        )

        composeRule.onNodeWithText(SET_ASIDE_LINE).assertIsDisplayed()
        composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).assertTextEquals("you")
        assertEquals("you", composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).fetchSemanticsNode().config[MakerKey])
        composeRule.onNodeWithText("not now").assertIsDisplayed()
        composeRule.onAllNodesWithTag(ACCEPT_TAG).assertCountEquals(0)
        composeRule.onAllNodesWithTag(SET_ASIDE_TAG).assertCountEquals(0)
        composeRule.onNodeWithTag(REOPEN_TAG).performClick()

        assertEquals(1, taps.reopens)
        assertEquals(0, taps.openEpics)
    }

    @Test
    fun `Reopen is disabled while a send is out`() {
        setBar(state = "set_aside", send = BarSend.Sending)

        composeRule.onNodeWithTag(REOPEN_TAG).assertIsNotEnabled()
    }

    @Test
    fun `an accept the client reads names its maker on the bar`() {
        FakeOrreryServer().use { server ->
            server.respond(
                CONTRACTS_GET_PATH,
                ContractsGetResponse.newBuilder()
                    .setContract(
                        Contract.newBuilder()
                            .setId(CONTRACT_ID)
                            .setRepository("synthetic-repo")
                            .setTitle("synthetic contract")
                            .setCare("limited")
                            .setState("accepted")
                            .setVersion(VERSION)
                            .setConversation("sess-synthetic")
                            .setCreated(1L)
                            .setChanged(2L),
                    )
                    .addStates(
                        ContractStateChange.newBuilder()
                            .setState("accepted")
                            .setVersion(VERSION)
                            .setBy("session")
                            .setSession("sess-maker")
                            .setWords("ship it")
                            .setTime(4102444000L)
                            .setMaker(
                                RecordMaker.newBuilder()
                                    .setKind("conversation")
                                    .setReason("the owner delegated it")
                                    .setSession("sess-maker"),
                            ),
                    )
                    .build(),
            )

            val record = checkNotNull(runBlocking(Dispatchers.IO) { OrreryClient(server.baseUrl).contract(CONTRACT_ID) })

            assertEquals(
                RecordMakerRow(kind = "conversation", reason = "the owner delegated it", session = "sess-maker"),
                record.states.single().maker,
            )
            setBar(state = "accepted", states = record.states)
            composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).assertTextEquals("the Director")
            assertEquals("director", composeRule.onNodeWithTag(ACCEPT_BAR_WHO_TAG).fetchSemanticsNode().config[MakerKey])
        }
    }
}
