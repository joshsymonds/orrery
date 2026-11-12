package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.Conversation
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.ConversationDelta
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.ConversationState
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.WaitingItem
import org.junit.Assert.assertEquals
import org.junit.Test

class ConversationStreamTest {
    private fun item(text: String) = ConversationItem("reply", 0L, text, "", "", "", false)

    private fun numbered(from: Int, to: Int) = (from..to).map { item("item-$it") }

    private fun pane(id: String) = Pane(id, "host", "session", 0, 0, "/cwd", "claude", "idle", "", "label", 0L, 0L, "", "")

    private fun state(origin: String, pane: Pane? = null) = ConversationState(
        origin = origin,
        goal = "goal-$origin",
        waiting = listOf(WaitingItem("queued-$origin", 0L, false)),
        context = ConversationContext(tokens = 10L, window = 100L),
        pane = pane,
    )

    private fun delta(
        seq: Long,
        reset: Boolean = false,
        items: List<ConversationItem> = emptyList(),
        state: ConversationState? = null,
        transcript: String = "t1",
    ) = ConversationDelta(transcript, seq, reset, items, state)

    private fun held(
        items: List<ConversationItem> = listOf(item("old-1")),
        pane: Pane? = pane("old-pane"),
    ) = StreamedConversation(
        transcript = "t1",
        seq = 1L,
        conversation = Conversation(
            origin = "old-origin",
            items = items,
            goal = "old-goal",
            waiting = listOf(WaitingItem("old-queued", 0L, true)),
            context = ConversationContext(tokens = 5L, window = 50L),
        ),
        pane = pane,
    )

    @Test
    fun firstDeltaWithNothingHeldHoldsItsItemsAndStateAndPane() {
        val absent: StreamedConversation? = null

        val result = absent.applyDelta(
            delta(seq = 3L, items = listOf(item("a"), item("b")), state = state("new", pane("p1"))),
        )

        assertEquals(
            StreamedConversation(
                transcript = "t1",
                seq = 3L,
                conversation = Conversation(
                    origin = "new",
                    items = listOf(item("a"), item("b")),
                    goal = "goal-new",
                    waiting = listOf(WaitingItem("queued-new", 0L, false)),
                    context = ConversationContext(tokens = 10L, window = 100L),
                ),
                pane = pane("p1"),
            ),
            result,
        )
    }

    @Test
    fun firstDeltaWithNothingHeldHoldsOnlyNewestKeepItems() {
        val absent: StreamedConversation? = null

        val result = absent.applyDelta(delta(seq = 2L, items = numbered(1, 5)), keep = 3)

        assertEquals(numbered(3, 5), result.conversation.items)
    }

    @Test
    fun resetReplacesHeldItemsAndStateAndTakesNewTranscript() {
        val result = held().applyDelta(
            delta(seq = 7L, reset = true, items = listOf(item("c")), state = state("new", pane("p2")), transcript = "t2"),
        )

        assertEquals(
            StreamedConversation(
                transcript = "t2",
                seq = 7L,
                conversation = Conversation(
                    origin = "new",
                    items = listOf(item("c")),
                    goal = "goal-new",
                    waiting = listOf(WaitingItem("queued-new", 0L, false)),
                    context = ConversationContext(tokens = 10L, window = 100L),
                ),
                pane = pane("p2"),
            ),
            result,
        )
    }

    @Test
    fun resetWithoutStateClearsStateFields() {
        val result = held().applyDelta(delta(seq = 8L, reset = true, items = listOf(item("c")), state = null))

        assertEquals(
            StreamedConversation(
                transcript = "t1",
                seq = 8L,
                conversation = Conversation(origin = "", items = listOf(item("c")), goal = "", waiting = emptyList(), context = null),
                pane = null,
            ),
            result,
        )
    }

    @Test
    fun resetHoldsOnlyNewestKeepItems() {
        val result = held().applyDelta(delta(seq = 9L, reset = true, items = numbered(1, 5)), keep = 3)

        assertEquals(numbered(3, 5), result.conversation.items)
    }

    @Test
    fun laterDeltaAppendsItemsInOrder() {
        val result = held(items = listOf(item("old-1"))).applyDelta(
            delta(seq = 2L, items = listOf(item("n1"), item("n2"))),
        )

        assertEquals(listOf(item("old-1"), item("n1"), item("n2")), result.conversation.items)
        assertEquals(2L, result.seq)
    }

    @Test
    fun laterDeltaTrimsToNewestKeepItems() {
        val result = held(items = listOf(item("o1"), item("o2"))).applyDelta(
            delta(seq = 3L, items = listOf(item("n1"), item("n2"))),
            keep = 3,
        )

        assertEquals(listOf(item("o2"), item("n1"), item("n2")), result.conversation.items)
    }

    @Test
    fun defaultKeepIsFiftyNewestItems() {
        val result = held(items = emptyList()).applyDelta(delta(seq = 4L, items = numbered(1, 51)))

        assertEquals(numbered(2, 51), result.conversation.items)
    }

    @Test
    fun laterDeltaWithoutStateKeepsHeldStateAndPane() {
        val heldState = held()

        val result = heldState.applyDelta(delta(seq = 4L, items = listOf(item("n1")), state = null))

        assertEquals(heldState.conversation.origin, result.conversation.origin)
        assertEquals(heldState.conversation.goal, result.conversation.goal)
        assertEquals(heldState.conversation.waiting, result.conversation.waiting)
        assertEquals(heldState.conversation.context, result.conversation.context)
        assertEquals(heldState.pane, result.pane)
    }

    @Test
    fun laterDeltaWithStateReplacesEveryStateFieldAndPane() {
        val result = held().applyDelta(
            delta(seq = 5L, items = listOf(item("n1")), state = state("new", pane("p2"))),
        )

        assertEquals(
            StreamedConversation(
                transcript = "t1",
                seq = 5L,
                conversation = Conversation(
                    origin = "new",
                    items = listOf(item("old-1"), item("n1")),
                    goal = "goal-new",
                    waiting = listOf(WaitingItem("queued-new", 0L, false)),
                    context = ConversationContext(tokens = 10L, window = 100L),
                ),
                pane = pane("p2"),
            ),
            result,
        )
    }

    @Test
    fun laterDeltaWithStateAndNoPaneClearsHeldPane() {
        val result = held(pane = pane("old-pane")).applyDelta(delta(seq = 6L, state = state("new", pane = null)))

        assertEquals(null, result.pane)
    }

    @Test
    fun laterDeltaWithEmptyStateFieldsClearsHeldOriginGoalAndWaiting() {
        val emptyState = state("new").copy(origin = "", goal = "", waiting = emptyList())

        val result = held().applyDelta(delta(seq = 6L, state = emptyState))

        assertEquals("", result.conversation.origin)
        assertEquals("", result.conversation.goal)
        assertEquals(emptyList<WaitingItem>(), result.conversation.waiting)
    }

    @Test
    fun laterDeltaWithStateWithoutContextClearsHeldContext() {
        val stateWithoutContext = state("new").copy(context = null)

        val result = held().applyDelta(delta(seq = 6L, state = stateWithoutContext))

        assertEquals(null, result.conversation.context)
    }

    @Test
    fun keepaliveChangesOnlyTranscriptAndSeq() {
        val heldState = held()

        val result = heldState.applyDelta(delta(seq = 9L, transcript = "t2"))

        assertEquals(heldState.copy(transcript = "t2", seq = 9L), result)
    }
}
