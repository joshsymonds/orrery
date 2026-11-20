package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractDecisionRow
import io.github.joshsymonds.orrery.api.ContractRecordRow
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.ContractStateRow
import io.github.joshsymonds.orrery.api.ContractVersionRow
import io.github.joshsymonds.orrery.api.EpicContractRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneOffset

class ContractModelTest {
    private val contractId = "tidepool/tide-alerts"

    // 1700000000 is 22:13 UTC and 1700003600 is 23:13 UTC.
    private val t1 = 1700000000L
    private val t2 = t1 + 3600L

    // decision is one decision of a version: open while its answer is "", and it concerns the row given.
    private fun decision(id: String, row: String, answer: String = ""): ContractDecisionRow =
        ContractDecisionRow(
            id = id,
            question = "Question $id?",
            options = listOf("yes", "no"),
            recommendation = "yes",
            reason = "Needed",
            row = row,
            answer = answer,
        )

    // version is one stored version of the contract, with its decisions and the time it was stored.
    private fun version(n: Long, decisions: List<ContractDecisionRow> = emptyList(), time: Long = t1): ContractVersionRow =
        ContractVersionRow(
            version = n,
            contract = EpicContractRow(
                title = "Tide alerts",
                approved = "",
                intent = "",
                failures = emptyList(),
                care = "limited",
                additions = emptyList(),
                premises = emptyList(),
                exclusions = emptyList(),
                approach = "",
                alternatives = emptyList(),
                releaseSteps = emptyList(),
                releaseThen = "",
                checks = "",
                base = "",
                accepted = 0L,
            ),
            requirements = emptyList(),
            decisions = decisions,
            words = "",
            writer = "s1",
            time = time,
            changedRows = emptyList(),
            changedSections = emptyList(),
        )

    // stateChange is one change of the contract's state.
    private fun stateChange(
        state: String,
        version: Long,
        by: String,
        words: String = "",
        time: Long = t2,
        session: String = "",
    ): ContractStateRow = ContractStateRow(state, version, by, session, words, time)

    // record is a Get response for the contract at state and care, with its versions oldest first and its state changes.
    private fun record(
        versions: List<ContractVersionRow>,
        state: String = "proposed",
        care: String = "limited",
        states: List<ContractStateRow> = emptyList(),
    ): ContractRecordRow {
        val newest = versions.last()
        return ContractRecordRow(
            contract = ContractRow(
                id = contractId,
                repository = "tidepool",
                title = "Tide alerts",
                care = care,
                state = state,
                version = newest.version,
                openDecisions = newest.decisions.count { it.answer == "" }.toLong(),
                conversation = "s1",
                created = t1,
                changed = t2,
            ),
            versions = versions,
            states = states,
        )
    }

    @Test
    fun acceptIsOfferedForTheNewestProposedVersionOnceNothingIsOpen() {
        assertTrue(acceptable(record(listOf(version(1L))), 1L, emptySet(), false))
        assertFalse(acceptable(record(listOf(version(1L, listOf(decision("D1", "F1"))))), 1L, emptySet(), false))
        assertTrue(acceptable(record(listOf(version(1L, listOf(decision("D1", "F1", "yes"))))), 1L, emptySet(), false))
    }

    @Test
    fun aSevereProposalStaysUnacceptableUntilItsIrreversibleSectionWasRead() {
        val severe = record(listOf(version(1L)), care = "severe")
        assertFalse(acceptable(severe, 1L, emptySet(), false))
        assertTrue(acceptable(severe, 1L, setOf("tidepool/tide-alerts@1"), false))
        assertTrue(acceptable(record(listOf(version(1L)), care = "serious"), 1L, emptySet(), false))
    }

    @Test
    fun acceptIsNotOfferedForAnOlderVersionADecidedContractOrWhileSending() {
        val twoVersions = record(listOf(version(1L), version(2L)))
        assertFalse(acceptable(twoVersions, 1L, emptySet(), false))
        assertTrue(acceptable(twoVersions, 2L, emptySet(), false))
        assertFalse(acceptable(record(listOf(version(1L)), state = "accepted"), 1L, emptySet(), false))
        assertFalse(acceptable(record(listOf(version(1L)), state = "set_aside"), 1L, emptySet(), false))
        assertFalse(acceptable(record(listOf(version(1L))), 1L, emptySet(), true))
    }

    @Test
    fun acceptStepSendsWhileTheDrawnVersionIsStillTheNewestProposed() {
        assertEquals(AcceptStep.Send, acceptStep(1L, record(listOf(version(1L)))))
    }

    @Test
    fun acceptStepRedrawsWhenANewerVersionCameIn() {
        val reread = record(listOf(version(1L), version(2L)))
        assertEquals(AcceptStep.Redraw(reread), acceptStep(1L, reread))
    }

    @Test
    fun acceptStepRedrawsWhenTheStateChangedUnderTheDrawnVersion() {
        val reread = record(listOf(version(1L)), state = "accepted")
        assertEquals(AcceptStep.Redraw(reread), acceptStep(1L, reread))
    }

    @Test
    fun bandAndFootWordsForNoItems() {
        val none = leftOf(0, "limited", false)
        assertEquals("nothing left to settle", bandText(none))
        assertEquals("Nothing is left to settle.", footText(none))
    }

    @Test
    fun bandAndFootWordsForOneItem() {
        val oneDecision = leftOf(1, "limited", false)
        assertEquals("1 decision is yours", bandText(oneDecision))
        assertEquals("1 thing left: 1 decision is yours.", footText(oneDecision))
        val unread = leftOf(0, "severe", false)
        assertEquals("read “can’t be undone” before Accept", bandText(unread))
        assertEquals("1 thing left: read “can’t be undone” before Accept.", footText(unread))
    }

    @Test
    fun bandAndFootWordsForTwoItems() {
        val two = leftOf(2, "severe", false)
        assertEquals("2 decisions are yours; read “can’t be undone” before Accept", bandText(two))
        assertEquals("2 things left: 2 decisions are yours; read “can’t be undone” before Accept.", footText(two))
        assertEquals("1 thing left: 2 decisions are yours.", footText(leftOf(2, "severe", true)))
    }

    @Test
    fun concernedRowsNamesTheRowsOfOpenDecisionsOnly() {
        val decisions = listOf(
            decision("D1", "F1"),
            decision("D2", "F2", "yes"),
            decision("D3", ""),
            decision("D4", "R2"),
        )
        assertEquals(setOf("F1", "R2"), concernedRows(decisions))
    }

    @Test
    fun aboutMessageLeadsTheDraftWithTheRowItIsAbout() {
        assertEquals("About F1: Keep the rows\n\nmake it smaller", aboutMessage("F1", "Keep the rows", "make it smaller"))
    }

    @Test
    fun recordLineForOneProposedVersion() {
        assertEquals("v1 proposed 22:13", recordLine(record(listOf(version(1L, time = t1))), ZoneOffset.UTC))
    }

    @Test
    fun recordLineForAVersionWithNoTimeHasNoClock() {
        assertEquals("v1 proposed ", recordLine(record(listOf(version(1L, time = 0L))), ZoneOffset.UTC))
    }

    @Test
    fun recordLineForTwoProposedVersionsNamesTheFirstWrite() {
        val two = record(listOf(version(1L, time = t1), version(2L, time = t2)))
        assertEquals("v2 proposed 23:13 · first written 22:13", recordLine(two, ZoneOffset.UTC))
    }

    @Test
    fun recordLineForAnAcceptedContractWithWords() {
        val byOwner = record(
            listOf(version(1L)),
            state = "accepted",
            states = listOf(stateChange("accepted", 1L, "owner", words = "ship it")),
        )
        assertEquals("v1 accepted by you 23:13 with “ship it”", recordLine(byOwner, ZoneOffset.UTC))
    }

    @Test
    fun recordLineForAnAcceptedContractWithoutWords() {
        val bySession = record(
            listOf(version(1L)),
            state = "accepted",
            states = listOf(stateChange("accepted", 1L, "session", session = "s1")),
        )
        assertEquals("v1 accepted by the session 23:13", recordLine(bySession, ZoneOffset.UTC))
    }

    @Test
    fun recordLineForASetAsideContractNamesItsWords() {
        val setAside = record(
            listOf(version(1L), version(2L)),
            state = "set_aside",
            states = listOf(stateChange("set_aside", 2L, "owner", words = "not now")),
        )
        assertEquals("v2 set aside by you 23:13 with “not now”", recordLine(setAside, ZoneOffset.UTC))
    }

    @Test
    fun recordLineWithoutAMatchingChangeWritesTheStateWords() {
        val noChange = record(listOf(version(1L), version(2L)), state = "set_aside")
        assertEquals("v2 set aside", recordLine(noChange, ZoneOffset.UTC))
    }

    @Test
    fun statePillWordsEachState() {
        assertEquals("proposed · waits for you", statePill("proposed"))
        assertEquals("accepted", statePill("accepted"))
        assertEquals("set aside", statePill("set_aside"))
    }

    @Test
    fun contractSlugIsTheIdPastItsLastSlash() {
        assertEquals("tide-alerts", contractSlug("tidepool/tide-alerts"))
    }
}
