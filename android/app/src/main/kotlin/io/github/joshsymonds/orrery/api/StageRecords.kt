package io.github.joshsymonds.orrery.api

/**
 * Who made one record of a design or a contract, the maker round 8 draws as a ByMark or a WhoTag (O.by and O.who in
 * design.js). [kind] is owner_serve (the owner through serve's call), owner_conversation (the owner in the conversation),
 * conversation (the Director, for the owner, under his delegation), owner, or orreryd. [quote] is the owner's words a
 * decision quotes, set for owner_conversation; [reason] is why the conversation decided and [delegation] the owner's words
 * that let it, both set for conversation. [session] is the session the record was made in. Each is empty when its kind
 * does not set it.
 */
data class RecordMakerRow(
    val kind: String = "",
    val quote: String = "",
    val reason: String = "",
    val delegation: String = "",
    val session: String = "",
)

/**
 * One step a design's stage started, oldest first, as the design's Research and Steelman sections list it (O.researchList
 * and O.steelman in design.js). [step] is the step's id and [kind] the stage tool that started it. [subject] is what the
 * step looks into, [repository] and [revision] where it runs, and [delta] what a closure pass checks. [created] is when it
 * started, in unix seconds. [status] and [report] are empty until it returns, [verdict] is set only for a steelman pass,
 * and [counted] is whether it counts toward the design's budget of passes.
 */
data class DesignStepRow(
    val step: String = "",
    val kind: String = "",
    val subject: String = "",
    val repository: String = "",
    val revision: String = "",
    val delta: String = "",
    val created: Long = 0L,
    val status: String = "",
    val report: String = "",
    val verdict: String = "",
    val counted: Boolean = false,
)

/**
 * One question a design's stage asked, numbered by [id] within [design], as the Questions section and the question thread
 * draw it (O.questionList and O.qthread in design.js). [contract] is the id of the contract it concerns, "" when none.
 * [options] are what the owner may choose and [recommendation] is the option the planner recommends. [asked] is when it was
 * asked, in unix seconds. [answer] is the answer's words, "" while it is open; [accepted] is whether the answer is the
 * recommendation; [maker] who answered, and [answered] when, in unix seconds, 0 while it is open.
 */
data class DesignQuestionRow(
    val design: String = "",
    val id: Long = 0L,
    val contract: String = "",
    val text: String = "",
    val options: List<String> = emptyList(),
    val recommendation: String = "",
    val asked: Long = 0L,
    val answer: String = "",
    val accepted: Boolean = false,
    val maker: RecordMakerRow? = null,
    val answered: Long = 0L,
) {
    /** The question's label in round 8, Q and its [id], which its thread and its options are tagged with (O.idc in design.js). */
    val key: String get() = "Q$id"
}

/**
 * One item of a design's ledger of findings, as the Ledger draws each finding with its one disposition and who gave it
 * (O.ledger and O.finding in design.js). [id] is the item's id within its design and [contract] the contract it concerns.
 * [origin] is discovery or closure, the pass that raised it. [impact] is what it would change if it held, [evidence] what it
 * rests on and [change] the change it proposes. [disposition] is open, adopted, rejected or deferred; [reason] says why it
 * was rejected or deferred and [boundary] what it was deferred to. [maker] is who gave the disposition, null while it is
 * open. [closureWord] and [closureNote] are closure's word and note on the item, empty until closure gives them.
 */
data class DesignLedgerItemRow(
    val id: String = "",
    val contract: String = "",
    val origin: String = "",
    val impact: String = "",
    val evidence: String = "",
    val change: String = "",
    val disposition: String = "",
    val reason: String = "",
    val boundary: String = "",
    val maker: RecordMakerRow? = null,
    val closureWord: String = "",
    val closureNote: String = "",
)

/**
 * One approach a design's stage weighed, numbered by [seq] within the design, with the facts the Approach section sets side
 * by side (O.approaches in design.js): its [name], [shape], [cost], what it [wins] and [loses], what would make the planner
 * [reconsider] it, the ids of the research steps it rests on ([research]), and whether the planner [recommended] it, with its
 * [reason]. [time] is when it was stored, in unix seconds.
 */
data class DesignApproachRow(
    val seq: Long = 0L,
    val name: String = "",
    val shape: String = "",
    val cost: String = "",
    val wins: String = "",
    val loses: String = "",
    val reconsider: String = "",
    val research: List<String> = emptyList(),
    val recommended: Boolean = false,
    val reason: String = "",
    val time: Long = 0L,
)

/**
 * The approach the owner chose for a design: the [approach], the seq of the approach chosen, the owner's [words] it is made
 * with, [maker] who made the choice, and [time] when it was made, in unix seconds. The Approach section's chosen card and its
 * by-line draw it (O.approaches and O.by in design.js).
 */
data class DesignChoiceRow(
    val approach: Long = 0L,
    val words: String = "",
    val maker: RecordMakerRow? = null,
    val time: Long = 0L,
)

/**
 * One line of a design's log, numbered by [seq] within the design, newest first on the page (O.dlog in design.js). [time] is
 * when it was logged and [maker] who did what it records, in unix seconds; [session] is the session that logged it. [what]
 * says what was done, [ids] are the records it touched, and [words] are the words it was logged with.
 */
data class DesignLogRow(
    val seq: Long = 0L,
    val time: Long = 0L,
    val maker: RecordMakerRow? = null,
    val session: String = "",
    val what: String = "",
    val ids: List<String> = emptyList(),
    val words: String = "",
)

/**
 * One decision the conversation made for the owner under a delegation, with its choice and its reason, as the Decided for
 * you section lists it (O.decided in design.js). [kind] is the kind of decision, [ref] the id of the thing it concerns and
 * [text] its text. [reason] is why the conversation decided, and [time] when, in unix seconds.
 */
data class DesignDecisionRow(
    val kind: String = "",
    val ref: String = "",
    val text: String = "",
    val reason: String = "",
    val time: Long = 0L,
)

/**
 * The owner's delegation of a design's decisions to the conversation, numbered by [seq] within the design. It is the line
 * at the top of the design that says he handed the decisions over, in his words (O.delegated in design.js). [kinds] are the
 * kinds of decision it covers, [message] is the owner's delegating message whole and [session] the session that stored it.
 * [time] is when it was stored, in unix seconds, and [decisions] are the decisions the conversation made under it, oldest
 * first.
 */
data class DesignDelegationRow(
    val seq: Long = 0L,
    val kinds: List<String> = emptyList(),
    val message: String = "",
    val session: String = "",
    val time: Long = 0L,
    val decisions: List<DesignDecisionRow> = emptyList(),
)

/**
 * A design's stage: its [steps], [questions], and [ledger] in the order they were made, and the flags that say where the
 * stage stands. [frozen] is whether the ledger is frozen, closure having started or been recorded as skipped, and
 * [closureSkipped] whether closure was recorded as skipped. [discoveryCounted] and [closureCounted] are whether each pass
 * counts toward the design's budget. The stage strip, the Steelman section and the Ledger draw it (O.dtrack, O.steelman
 * and O.ledger in design.js).
 */
data class DesignStageRow(
    val steps: List<DesignStepRow> = emptyList(),
    val questions: List<DesignQuestionRow> = emptyList(),
    val ledger: List<DesignLedgerItemRow> = emptyList(),
    val frozen: Boolean = false,
    val closureSkipped: Boolean = false,
    val discoveryCounted: Boolean = false,
    val closureCounted: Boolean = false,
)

/**
 * A design as its page reads it: the [design] row, its [approaches] in seq order, the [choice] made among them (null until
 * one is stored), its [log], its [delegations], and its [stage]. [stage] is null when the server served none. The design
 * page draws all of it (DesignPage, and O.designBody in design.js).
 */
data class DesignRecordRow(
    val design: DesignRow,
    val approaches: List<DesignApproachRow> = emptyList(),
    val choice: DesignChoiceRow? = null,
    val log: List<DesignLogRow> = emptyList(),
    val delegations: List<DesignDelegationRow> = emptyList(),
    val stage: DesignStageRow? = null,
)

/**
 * The working copy of a contract the stage is writing, over [version]: its [contract] body, its [requirements] and
 * [decisions], and the [writer] it was written by. [updated] is when it was last written, in unix seconds, and [changedRows]
 * are the ids whose text differs from the version before. The contract page draws the working copy and its changed marks
 * (ContractPage, and O.contractBody and O.dline in contract.js).
 */
data class ContractWorkingRow(
    val version: Long = 0L,
    val contract: EpicContractRow,
    val requirements: List<EpicRequirementRow> = emptyList(),
    val decisions: List<ContractDecisionRow> = emptyList(),
    val writer: String = "",
    val updated: Long = 0L,
    val changedRows: List<String> = emptyList(),
)

/**
 * A propose the store refused, numbered by [seq] within the contract: the [version] it would have made, the [field] it was
 * refused on and the [text] it was refused with, the [session] that proposed it, and [time] when, in unix seconds. The
 * contract page draws each refusal beside the row or section it refused (O.contractBody's refused line in contract.js).
 */
data class ContractRefusalRow(
    val seq: Long = 0L,
    val version: Long = 0L,
    val field: String = "",
    val text: String = "",
    val session: String = "",
    val time: Long = 0L,
)

/**
 * One write to a row of a contract, numbered by [seq] within the contract. [version] is the version it belongs to, 0 for a
 * draft, and [row] the row it concerns. [action] is write, remove, keep or strike, and [depends] the row the written row
 * depends on. [words] are the owner's words the write is made with and [maker] who made it, null when the writing session
 * made it on its own account. [time] is when it was stored, in unix seconds. The contract's rows and additions draw each
 * write with its by-line (O.contractBody and O.by in contract.js).
 */
data class ContractRowWriteRow(
    val seq: Long = 0L,
    val version: Long = 0L,
    val row: String = "",
    val action: String = "",
    val depends: String = "",
    val words: String = "",
    val maker: RecordMakerRow? = null,
    val time: Long = 0L,
)

/**
 * One thing a contract still needs before it can be accepted, as the accept bar lists it: its [kind], the [ref] it concerns
 * and its [text]. The bar's "left" note says how many there are and each one (O.acceptBar in contract.js).
 */
data class ContractLeftRow(
    val kind: String = "",
    val ref: String = "",
    val text: String = "",
)

/**
 * A row of a contract's [version] the owner was shown, with its [row] id and [time], in unix seconds. The contract page
 * reports what it showed through its onShown callback. The irreversible section's "read this before Accept" mark is the
 * unread option of O.contractBody in contract.js.
 */
data class ContractShownRow(
    val version: Long = 0L,
    val row: String = "",
    val time: Long = 0L,
)

/**
 * One task working a requirement of a contract: its [id], [title], [status] and [place]. The work table's tasks column draws
 * each one with its pips and its words (O.contractWork in contract.js).
 */
data class ContractRequirementTaskRow(
    val id: String = "",
    val title: String = "",
    val status: String = "",
    val place: String = "",
)

/**
 * One requirement of a contract's work: its [id] and [text], its [state] (proven, being worked, held on you, failed, or not
 * reached), the [tasks] working it, and the [proof] of the evidence run that decided it, null while none has. The work
 * table's rows and their red and green proof draw it (ContractWork, and O.contractWork in contract.js).
 */
data class ContractRequirementWorkRow(
    val id: String = "",
    val text: String = "",
    val state: String = "",
    val tasks: List<ContractRequirementTaskRow> = emptyList(),
    val proof: EvidenceRunRow? = null,
)

/**
 * A contract's length in lines of rows and prose above its checks: [lines] is its count, [warn] the count from which it is
 * long and [strong] the count from which it is very long, never refused (O.lengthMeter and O.LENGTH in design.js).
 */
data class ContractLengthRow(
    val lines: Long = 0L,
    val warn: Long = 0L,
    val strong: Long = 0L,
)

/**
 * A contract's stage as its page reads it: the [working] copy being written (null when there is none), its [refusals],
 * [writes] and [left] to settle, its [work] once it is accepted, the rows it [shown] the owner, and its [length] (null when
 * it is not measured). The contract page draws all of it (ContractPage, and O.contractPage in contract.js).
 */
data class ContractStageRow(
    val working: ContractWorkingRow? = null,
    val refusals: List<ContractRefusalRow> = emptyList(),
    val writes: List<ContractRowWriteRow> = emptyList(),
    val left: List<ContractLeftRow> = emptyList(),
    val work: List<ContractRequirementWorkRow> = emptyList(),
    val shown: List<ContractShownRow> = emptyList(),
    val length: ContractLengthRow? = null,
)

/** The answer to an owner's answer to a design's question: the question as the stage holds it now, or the server's refusal. */
sealed interface DesignAnswerResult {
    /** The answer landed: [question] is the question as the stage holds it after it, settled. */
    data class Done(val question: DesignQuestionRow) : DesignAnswerResult

    /** The server refused the answer, for the [reason] it gave. */
    data class Refused(val reason: String) : DesignAnswerResult
}
