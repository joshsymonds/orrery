package io.github.joshsymonds.orrery.api

import androidx.annotation.VisibleForTesting
import com.connectrpc.Code
import com.connectrpc.ConnectException
import com.connectrpc.ProtocolClientConfig
import com.connectrpc.ResponseMessage
import com.connectrpc.ServerOnlyStreamInterface
import com.connectrpc.extensions.GoogleJavaLiteProtobufStrategy
import com.connectrpc.impl.ProtocolClient
import com.connectrpc.okhttp.ConnectOkHttpClient
import com.connectrpc.protocols.NetworkProtocol
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ControlClient
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineText as EngineTextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAddition as EpicAdditionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAlternative as EpicAlternativeProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask as EpicBuildTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract as EpicContractProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicDecision as EpicDecisionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicFailure as EpicFailureProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry as EpicHistoryEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNode as EpicNodeProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicNodeCheck as EpicNodeCheckProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicPremise as EpicPremiseProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep as EpicReleaseStepProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement as EpicRequirementProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicSlice as EpicSliceProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep as EpicStepProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck as EpicStepCheckProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepVerdict as EpicStepVerdictProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTask as EpicTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTaskFinding as EpicTaskFindingProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTaskReview as EpicTaskReviewProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTell as EpicTellProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTrigger as EpicTriggerProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTurn as EpicTurnProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsRetryRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifact as EvidenceArtifactProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceClient
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun as EvidenceRunProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostClient
import io.github.joshsymonds.orrery.gen.orrery.v1.HeadlessSession as HeadlessSessionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.OpenRequest as OpenRequestProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ListHeadlessRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesClient
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerClient
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolHold as PoolHoldProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolHolder as PoolHolderProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolMemberStatus as PoolMemberStatusProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolStatus as PoolStatusProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolWaiter as PoolWaiterProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsFreeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PoolsStatusRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PushClient
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UnregisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationResponse
import kotlinx.coroutines.channels.ReceiveChannel
import kotlinx.coroutines.runBlocking
import okhttp3.EventListener
import okhttp3.OkHttpClient
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.net.MalformedURLException
import java.util.UUID
import java.util.concurrent.TimeUnit
import javax.net.SocketFactory
import kotlin.time.Duration

private const val TIMEOUT_MS = 10_000
private const val CLIENT_HEADER = "orrery-android"
private val CLIENT_HEADERS: Map<String, List<String>> = mapOf("X-Orrery-Client" to listOf(CLIENT_HEADER))

/** The answers to ListHeadless that mean the server has no headless sessions to list. */
private val HEADLESS_ABSENT = setOf(Code.UNAVAILABLE, Code.UNIMPLEMENTED, Code.NOT_FOUND)

data class Pane(
    val id: String,
    val host: String,
    val session: String,
    val window: Int,
    val pane: Int,
    val cwd: String,
    val command: String,
    val state: String,
    val summary: String,
    val label: String,
    val changed: Long,
    val seen: Long,
    val question: String,
    val questionHash: String,
    val tool: String = "",
    /** Unix seconds of the pane's newest reply or question; 0 when the server predates the key. */
    val newestReply: Long = 0L,
    /** Unix seconds the pane's last turn ended; 0 when unknown or when the server predates the key. */
    val lastTurnEnd: Long = 0L,
    /** True for a headless session, whose [id] is the session's id; false for a tmux pane. */
    val headless: Boolean = false,
)

data class PaneTool(val id: String, val name: String, val input: String, val start: Long)

fun Pane.currentTool(): PaneTool? {
    if (tool.isEmpty()) return null
    val json = try {
        JSONObject(tool)
    } catch (e: JSONException) {
        return null
    }
    return PaneTool(
        id = json.optString("id", ""),
        name = json.optString("name", ""),
        input = json.optString("input", ""),
        start = json.optLong("start", 0L),
    )
}

data class ConversationItem(
    val kind: String,
    val time: Long,
    val text: String,
    val tool: String,
    val input: String,
    val id: String,
    val error: Boolean,
    /** A `notice` item's sort (`compaction`, `task`, `teammate`, `interrupted`); "" on other kinds. */
    val notice: String = "",
    /** A `compaction` item's trigger (`auto` or `manual`); "" when its boundary was not read, on other kinds, or from an older server. */
    val trigger: String = "",
    /** A `compaction` item's context tokens before it compacted; 0 when its boundary was not read, on other kinds, or from an older server. */
    val tokens: Long = 0L,
)

/** A message Claude Code's queue holds for the pane. [canceled] is true when Claude Code cleared it on cancel. */
data class WaitingItem(val text: String, val time: Long, val canceled: Boolean)

/** The pane's context usage: [tokens] in use out of a [window] of tokens. */
data class ConversationContext(val tokens: Long, val window: Long)

/**
 * [goal] is the pane's active Claude Code goal, or "" when there is none. [waiting] is the
 * queued messages, oldest first, or empty when there are none. [context] is the pane's
 * context usage, or null when the server predates it.
 */
data class Conversation(
    val origin: String,
    val items: List<ConversationItem>,
    val goal: String = "",
    val waiting: List<WaitingItem> = emptyList(),
    val context: ConversationContext? = null,
)

/**
 * One conversation as a WatchConversation stream gives it. [state] is null when the message carried no state, and
 * [context] and [pane] are null when the state did not. [request] is the open request of a headless session, and
 * null when none is open or the stream is a pane's.
 */
data class ConversationState(
    val origin: String,
    val goal: String,
    val waiting: List<WaitingItem>,
    val context: ConversationContext?,
    val pane: Pane?,
    val request: SessionRequest? = null,
)

/** The request a headless session waits on. [options] are the answers the phone may send, and may be empty. */
data class SessionRequest(val id: String, val kind: String, val question: String, val options: List<String>)

/**
 * The text so far of one unfinished content block of a headless session. [parentToolUseId] is "" for the main
 * conversation.
 */
data class UnfinishedText(
    val messageId: String,
    val index: Int,
    val parentToolUseId: String,
    val kind: String,
    val text: String,
)

/**
 * One step of a WatchConversation stream: the [items] after [seq] in [transcript]. A [reset] delta replaces what the
 * client holds. [unfinished] is the text so far of each unfinished block of a headless session, and empty on a pane
 * stream.
 */
data class ConversationDelta(
    val transcript: String,
    val seq: Long,
    val reset: Boolean,
    val items: List<ConversationItem>,
    val state: ConversationState?,
    val unfinished: List<UnfinishedText> = emptyList(),
)

data class HostStat(val label: String, val value: String, val percent: Double?, val level: String)

data class HostStatus(val host: String, val stats: List<HostStat>)

/** The grant that holds a pool member: its [project], its [command] (empty when the client sent none), and when it was granted. */
data class Holder(val project: String, val command: List<String>, val since: Long, val stopping: Boolean)

/** One member of a pool. [holder] is null when no grant holds it, and [heldSince] is 0 when it is not held. */
data class PoolMember(val name: String, val state: String, val holder: Holder?, val heldSince: Long)

/** One request waiting for a pool, at [position] in line. */
data class Waiter(val position: Int, val project: String, val command: List<String>, val count: Int, val since: Long)

/** A pool in use: its [cap], its [members], and the requests [waiters] for it in queue order. */
data class Pool(val name: String, val cap: Int, val members: List<PoolMember>, val waiters: List<Waiter>)

/** An owner hold on [member], placed at [since]. */
data class Hold(val member: String, val since: Long)

/** The pools in use, and every owner hold, including holds on members of no pool in use. */
data class PoolsView(val pools: List<Pool>, val holds: List<Hold>)

/** One candidate the picker offers for the text it was asked. [prompt] is true when the row starts Claude with that text. */
data class PickRow(
    val token: String,
    val kind: String,
    val dir: String,
    val pane: String,
    val label: String,
    val note: String,
    val prompt: Boolean,
)

/**
 * One check of a step in [round]. [stdout] and [stderr] are the redacted tails of a failed gate check, empty otherwise.
 * [detail] is the check's redacted detail, and [tailStdout] and [tailStderr] are its redacted tails, set for any check.
 */
data class StepCheckRow(
    val round: Int,
    val name: String,
    val ok: Boolean,
    val stdout: String,
    val stderr: String,
    val detail: String = "",
    val tailStdout: String = "",
    val tailStderr: String = "",
)

/** One done-judge verdict a step got in [round]: [verdict] is met, unmet or empty, and [reason] is why, redacted. */
data class StepVerdictRow(val round: Int, val verdict: String, val reason: String)

/**
 * One labelled step as the epics view shows it. [end] is "" while the step runs. [stage] is the stage the step is in,
 * empty when it has none, and [stageStarted] is when that stage started, 0 when unknown. [limited] is whether a usage
 * limit of [limitKind] holds the step. [readOnly] is whether the step is read-only, [role] is the role it plays, and
 * [summary] is its redacted summary. [gateName] is the gate its return is checked against, empty when it runs none,
 * and [verdicts] are its done-judge verdicts, in round order.
 */
data class StepRow(
    val id: String,
    val task: String,
    val agent: String,
    val end: String,
    val status: String,
    val held: Boolean,
    val started: Long,
    val ended: Long,
    val doing: String,
    val checks: List<StepCheckRow>,
    val reason: String,
    val stage: String = "",
    val stageStarted: Long = 0L,
    val limited: Boolean = false,
    val limitKind: String = "",
    val readOnly: Boolean = false,
    val role: String = "",
    val summary: String = "",
    val gateName: String = "",
    val verdicts: List<StepVerdictRow> = emptyList(),
)

/**
 * One epic, from the record root or orreryd's database. [runningSteps] are the epic's steps with no end yet, held ones
 * included. [waiting] is true for a live epic accepted and waiting to start. [source] is "orreryd" for an epic
 * orreryd's database holds, "record" for an epic read from records only.
 */
data class EpicRow(
    val id: String,
    val repository: String,
    val slug: String,
    val outcome: String,
    val changed: Long,
    val live: Boolean,
    val waiting: Boolean = false,
    val runningSteps: List<StepRow>,
    val endedSteps: Int,
    val problem: String,
    val source: String = "",
)

/**
 * One requirement of an orreryd epic's contract: [kind] is "check" or "review". [conclusion] is what its evidence is
 * judged against, and [runs] are its evidence runs, oldest first.
 */
data class EpicRequirementRow(
    val id: String,
    val text: String,
    val evidence: String,
    val kind: String,
    val conclusion: String = "",
    val runs: List<EvidenceRunRow> = emptyList(),
)

/**
 * One evidence run of a requirement, for one [phase] (red or green), produced by [source] (captured or supplied).
 * [passed] is whether the run passes its phase, [logBytes] and [stderrBytes] are the sizes of its stdout and stderr logs,
 * and [artifacts] are the files it kept. [item] is the requirement or task whose evidence the run shows, set by an
 * epic's history and empty elsewhere.
 */
data class EvidenceRunRow(
    val id: String,
    val phase: String,
    val source: String,
    val state: String,
    val exit: Int,
    val timedOut: Boolean,
    val notCaptured: String,
    val verdict: String,
    val judgeReason: String,
    val passed: Boolean,
    val revision: String,
    val created: Long,
    val ended: Long,
    val logBytes: Long,
    val stderrBytes: Long,
    val artifacts: List<EvidenceArtifactRow>,
    val item: String = "",
)

/**
 * One entry of an epic's history: [time] is its time in unix seconds and [cursor] names it for the next page. [kind] is
 * the row the entry carries, one of turn, trigger, step, evidence, decision and tell, and "" when it carries none. Only
 * the row [kind] names is set.
 */
data class EpicHistoryRow(
    val time: Long,
    val cursor: String,
    val kind: String,
    val turn: EpicTurnRow? = null,
    val trigger: EpicTriggerRow? = null,
    val step: StepRow? = null,
    val evidence: EvidenceRunRow? = null,
    val decision: EpicDecisionRow? = null,
    val tell: EpicTellRow? = null,
)

/**
 * A planner turn in an epic's history: [seq] is its position on the epic, [decisions] are the decisions its return
 * recorded, and [started] and [ended] are unix seconds, [ended] 0 while the turn runs.
 */
data class EpicTurnRow(
    val seq: Long,
    val step: String,
    val state: String,
    val reason: String,
    val outline: String,
    val outcome: String,
    val decisions: List<String>,
    val started: Long,
    val ended: Long,
)

/**
 * A cause that asked an epic's planner for a turn. [turn] is the position of the turn that claimed it, 0 while pending.
 */
data class EpicTriggerRow(val id: String, val cause: String, val state: String, val turn: Long, val created: Long)

/** A decision recorded on an epic. [question] is the Q<n> it answers, empty for a note. */
data class EpicDecisionRow(val seq: Long, val id: String, val text: String, val time: Long, val question: String)

/** Something an epic told its owner. [told] is whether the owner has been told it. */
data class EpicTellRow(val seq: Long, val text: String, val at: Long, val told: Boolean)

/**
 * One page of an epic's history, oldest first. [next] is the cursor to pass as after for the next page, empty when none
 * follows.
 */
data class EpicHistoryPage(val entries: List<EpicHistoryRow>, val next: String)

/** One file an evidence run kept, with the [state] it was kept in. */
data class EvidenceArtifactRow(val path: String, val state: String, val size: Long, val mediaType: String)

/** The bytes of one artifact or log an evidence run kept, with their [mediaType]. */
data class EvidenceArtifactFile(val data: ByteArray, val mediaType: String)

/** One task from an effort's state.json. [requirements] are the requirement ids the task names, in order. */
data class EpicTaskRow(
    val effort: Int,
    val slug: String,
    val subject: String,
    val status: String,
    val requirements: List<String> = emptyList(),
)

/**
 * One task's slice of the epic chart as serve sends it: rings outermost first. [at] is the ring a running or held task
 * is working toward, the first ring not reached, 0 once it ended. [held] is whether its step is held. [errorAt] is the
 * ring that errored, 0 unless it ended short.
 */
data class EpicSliceRow(
    val task: String,
    val hue: String,
    val redTest: Boolean,
    val working: Boolean,
    val testsPass: Boolean,
    val checks: Boolean,
    val gateAccepted: Boolean,
    val at: Int,
    val held: Boolean,
    val errorAt: Int,
)

/**
 * One workflow node run of a stored epic. [path] is the run's path, unique on the epic. [round] is the innermost
 * enclosing loop's round, 0 outside a loop, and [loopRound] is a loop's current round from 1, 0 for any other kind.
 * [turns] are a plan's turn seqs and [tasks] its task seqs, or a build's task seq. [runs] are an evidence node's runs
 * for its attempt. [check] is a gate or command node's check, null until it is stored. [step] is an agent node's step
 * id, empty until it is stored.
 */
data class EpicNodeRow(
    val path: String,
    val node: String,
    val kind: String,
    val state: String,
    val outcome: String,
    val round: Long,
    val loopRound: Long,
    val attempt: Long,
    val output: String,
    val revision: String,
    val turns: List<Long>,
    val tasks: List<Long>,
    val runs: List<EvidenceRunRow>,
    val check: EpicNodeCheckRow?,
    val step: String,
)

/**
 * A gate or command node's check: [state] is running until it ends, [exit] and [timedOut] say how it ended, [notRun]
 * says why it did not run, and [stdout] and [stderr] are the newest bytes of each.
 */
data class EpicNodeCheckRow(
    val state: String,
    val exit: Int,
    val timedOut: Boolean,
    val notRun: String,
    val stdout: String,
    val stderr: String,
)

/**
 * One task of an orreryd epic's plan, with its build: [stage] is the stage the build is on, empty before its first
 * unit. [red] and [green] are the evidence runs the build names, null until it names one. [requirements] are the
 * requirement ids the task covers, in order. [reviews] are the build's reviews, in attempt then position order.
 */
data class EpicBuildTaskRow(
    val id: String,
    val title: String,
    val status: String,
    val stage: String,
    val red: EvidenceRunRow?,
    val green: EvidenceRunRow?,
    val requirements: List<String> = emptyList(),
    val reviews: List<TaskReviewRow> = emptyList(),
)

/**
 * One review of a task's build: its [attempt], the [gate] it ran, its [step], its [verdict] (holds or fails) and its
 * [findings], in position order.
 */
data class TaskReviewRow(
    val attempt: Long,
    val gate: String,
    val step: String,
    val verdict: String,
    val findings: List<TaskFindingRow>,
)

/**
 * One finding a review raised: its [id], [claim] and [location], the [step] that verified it, its [result]
 * (confirmed, dropped or undecided) and why, in [reason]. [step], [result] and [reason] are empty until it is verified.
 */
data class TaskFindingRow(
    val id: String,
    val claim: String,
    val location: String,
    val step: String,
    val result: String,
    val reason: String,
)

/** One open question an orreryd epic's planner asked, in the order they were asked. */
data class EpicQuestionRow(val id: String, val text: String, val turn: Long, val asked: Long)

/** One failure an orreryd epic's contract guards against, with its [rating] and what the epic does about it. */
data class EpicFailureRow(val id: String, val text: String, val rating: String, val response: String)

/** One thing an orreryd epic's contract adds that the owner did not ask for. */
data class EpicAdditionRow(val location: String, val what: String, val why: String, val cost: String)

/** One assumption an orreryd epic's contract makes, with what changes if it is wrong. */
data class EpicPremiseRow(val id: String, val text: String, val ifWrong: String)

/** One approach an orreryd epic's contract considered and did not choose. */
data class EpicAlternativeRow(val alternative: String, val whyNot: String, val reconsider: String)

/** One step of an orreryd epic's contract that leaves the machine on release, with how it is [undo]ne. */
data class EpicReleaseStepRow(val step: String, val target: String, val undo: String)

/**
 * An orreryd epic's accepted contract, its sections in the contract's order, without its requirements. [accepted] is
 * when orreryd stored the epic, in unix seconds.
 */
data class EpicContractRow(
    val title: String,
    val approved: String,
    val intent: String,
    val failures: List<EpicFailureRow>,
    val care: String,
    val additions: List<EpicAdditionRow>,
    val premises: List<EpicPremiseRow>,
    val exclusions: List<String>,
    val approach: String,
    val alternatives: List<EpicAlternativeRow>,
    val releaseSteps: List<EpicReleaseStepRow>,
    val releaseThen: String,
    val checks: String,
    val base: String,
    val accepted: Long,
)

/**
 * One epic with its tasks, its [slices] in serve's order, and every step labelled with it, oldest step first.
 * [requirements] are an orreryd epic's requirements in contract order, empty for a record epic. [nodes] are its
 * workflow node runs in the order the server lists them. [buildTasks] are an orreryd epic's planned tasks in the order
 * they were planned, empty for a record epic. [summary] is the epic's summary paragraph, empty until it is written.
 * [contract] is the accepted contract, null for a record epic. [questions] are the open questions in the order they
 * were asked. [paused] is whether the epic's planner starts no turn, and [parked] is why the epic is parked, empty
 * when it is not.
 */
data class EpicDetail(
    val epic: EpicRow,
    val tasks: List<EpicTaskRow>,
    val slices: List<EpicSliceRow>,
    val steps: List<StepRow>,
    val requirements: List<EpicRequirementRow> = emptyList(),
    val nodes: List<EpicNodeRow> = emptyList(),
    val buildTasks: List<EpicBuildTaskRow> = emptyList(),
    val summary: String = "",
    val contract: EpicContractRow? = null,
    val questions: List<EpicQuestionRow> = emptyList(),
    val paused: Boolean = false,
    val parked: String = "",
)

/** What a hold, nudge, stop or epic call came to: it landed, or orreryd refused it with its reason. */
sealed interface StepCallResult {
    data object Landed : StepCallResult
    data class Refused(val message: String) : StepCallResult
}

sealed interface KeysResult {
    data object Sent : KeysResult
    data object PromptChanged : KeysResult
}

/** The server answered a call with the Connect [code]. [message] is its message, or the code name when it has none. */
class OrreryCallException(val code: Code, message: String) : IOException(message)

/**
 * The request was written, but no answer came within the read timeout, or the connection dropped
 * while waiting. The server may still have acted on the request.
 */
class NoAnswerException(cause: IOException) : IOException("no answer to the request", cause)

/** A transport failure after the connection opened: the request may have reached the server. */
private class RequestSentException(val transport: IOException) : IOException(transport)

/**
 * The socket factory every [OrreryClient] opens its connections with. The app keeps the default; the unit tests swap
 * in one that tells them whether a request or an answer is still on its way, so a wait can tell "still coming" from
 * "never coming".
 */
@VisibleForTesting
internal var socketFactory: SocketFactory = SocketFactory.getDefault()

/** The event listener every [OrreryClient] call reports to. The app keeps none; the unit tests count calls with it. */
@VisibleForTesting
internal var eventListenerFactory: EventListener.Factory = EventListener.Factory { EventListener.NONE }

/**
 * Blocking client for the orrery phone API over Connect. Each call is one unary request. A write
 * call is never retried, so a dropped connection cannot send it twice.
 */
class OrreryClient(
    baseUrl: String,
    private val composeReadTimeoutMillis: Int = 25_000,
    private val pickerReadTimeoutMillis: Int = 90_000,
    private val streamReadTimeoutMillis: Int = 10_000,
    private val standardReadTimeoutMillis: Int = TIMEOUT_MS,
) {
    private val root = baseUrl.removeSuffix("/")

    /** Each tier's read timeout, by tier, so the tests can check the defaults without waiting them out. */
    @VisibleForTesting
    internal val readTimeoutsMillis: Map<String, Int>
        get() = mapOf(
            "standard" to standardReadTimeoutMillis,
            "compose" to composeReadTimeoutMillis,
            "picker" to pickerReadTimeoutMillis,
            "stream" to streamReadTimeoutMillis,
        )

    private val http: OkHttpClient = OkHttpClient.Builder()
        .socketFactory(socketFactory)
        .eventListenerFactory(eventListenerFactory)
        .connectTimeout(TIMEOUT_MS.toLong(), TimeUnit.MILLISECONDS)
        .retryOnConnectionFailure(false)
        .addNetworkInterceptor { chain ->
            try {
                chain.proceed(chain.request())
            } catch (e: IOException) {
                throw RequestSentException(e)
            }
        }
        .build()

    /**
     * The generated clients, with the read timeout of one tier of calls. connect-kotlin's default
     * call deadline is 10 s on every call, so it is turned off: the tier's read timeout is the limit.
     */
    private inner class Services(readTimeoutMillis: Int) {
        private val protocol = ProtocolClient(
            ConnectOkHttpClient(http.newBuilder().readTimeout(readTimeoutMillis.toLong(), TimeUnit.MILLISECONDS).build()),
            ProtocolClientConfig(
                root,
                GoogleJavaLiteProtobufStrategy(),
                NetworkProtocol.CONNECT,
                timeoutOracle = { Duration.INFINITE },
            ),
        )
        val panes = PanesClient(protocol)
        val host = HostClient(protocol)
        val push = PushClient(protocol)
        val picker = PickerClient(protocol)
        val control = ControlClient(protocol)
        val sessions = SessionsClient(protocol)
        val epics = EpicsClient(protocol)
        val evidence = EvidenceClient(protocol)
        val steps = StepsClient(protocol)
        val pools = PoolsClient(protocol)
    }

    /**
     * Built on first use: connect-kotlin rejects an unusable address when a tier is built, with
     * IllegalArgumentException for a scheme other than http or https and ktor's URLParserException
     * (an IllegalStateException) for an address it cannot parse, and that must not crash the
     * constructor. Each use of an unusable address raises [MalformedURLException].
     */
    private val standardTier = lazy { Services(standardReadTimeoutMillis) }
    private val composeTier = lazy { Services(composeReadTimeoutMillis) }
    private val pickingTier = lazy { Services(pickerReadTimeoutMillis) }
    private val streamingTier = lazy { Services(streamReadTimeoutMillis) }
    private val standard: Services get() = built(standardTier)
    private val compose: Services get() = built(composeTier)
    private val picking: Services get() = built(pickingTier)
    private val streaming: Services get() = built(streamingTier)

    private fun built(tier: Lazy<Services>): Services =
        try {
            tier.value
        } catch (e: IllegalArgumentException) {
            throw MalformedURLException(e.message.orEmpty()).apply { initCause(e) }
        } catch (e: IllegalStateException) {
            throw MalformedURLException(e.message.orEmpty()).apply { initCause(e) }
        }

    fun panes(): List<Pane> {
        val response = call(write = false) { standard.panes.list(PanesListRequest.getDefaultInstance(), CLIENT_HEADERS) }
        return response.panesList.map { it.toPane() }
    }

    /**
     * The headless sessions of the last 14 days, each as a pane with [Pane.headless] set. A server that answers
     * unavailable, unimplemented or not_found has none to list, so it gives an empty list.
     */
    fun headless(): List<Pane> {
        val response = try {
            call(write = false) { standard.sessions.listHeadless(ListHeadlessRequest.getDefaultInstance(), CLIENT_HEADERS) }
        } catch (e: OrreryCallException) {
            if (e.code in HEADLESS_ABSENT) return emptyList()
            throw e
        }
        return response.sessionsList.map { it.toHeadlessPane() }
    }

    fun conversation(paneId: String): Conversation? {
        val request = ConversationRequest.newBuilder().setPane(paneId).setLimit(50).build()
        val response = notFoundAsNull { call(write = false) { standard.panes.conversation(request, CLIENT_HEADERS) } }
            ?: return null
        return Conversation(
            origin = response.origin,
            items = response.itemsList.map { it.toConversationItem() },
            goal = response.goal,
            waiting = response.waitingList.map { it.toWaitingItem() },
            context = if (response.hasContext()) response.context.toConversationContext() else null,
        )
    }

    /**
     * Opens the conversation stream of [paneId] after the seq [after] of [transcript], and hands each delta to
     * [onDelta] in order. Returns when the server ends the stream without error. A Connect error end throws
     * [OrreryCallException]; a transport failure throws its IOException, and so does a stream with no message for
     * [streamReadTimeoutMillis]. Cancelling the caller closes the connection.
     */
    suspend fun watchConversation(
        paneId: String,
        transcript: String,
        after: Long,
        onDelta: suspend (ConversationDelta) -> Unit,
    ) {
        val request = WatchConversationRequest.newBuilder()
            .setPane(paneId)
            .setLimit(50)
            .setTranscript(transcript)
            .setAfter(after)
            .build()
        streamDeltas(
            streaming.panes.watchConversation(CLIENT_HEADERS),
            request,
            { it.toConversationDelta() },
            onDelta,
        )
    }

    /**
     * Opens the stream of headless session [sessionId] after the seq [after], and hands each delta to [onDelta] in
     * order. It ends, fails and cancels as [watchConversation] does.
     */
    suspend fun watchSession(
        sessionId: String,
        after: Long,
        onDelta: suspend (ConversationDelta) -> Unit,
    ) {
        val request = SessionsWatchConversationRequest.newBuilder()
            .setId(sessionId)
            .setLimit(50)
            .setAfter(after)
            .build()
        streamDeltas(
            streaming.sessions.watchConversation(CLIENT_HEADERS),
            request,
            { it.toSessionConversationDelta() },
            onDelta,
        )
    }

    /** Sends [request] on [stream], then hands each message of the answer to [onDelta] as a delta. */
    internal suspend fun <Req : Any, Resp : Any> streamDeltas(
        stream: ServerOnlyStreamInterface<Req, Resp>,
        request: Req,
        toDelta: (Resp) -> ConversationDelta,
        onDelta: suspend (ConversationDelta) -> Unit,
    ) {
        val responses = stream.responseChannel()
        // The finally also covers the send, so a cancel or a failure while the request is sent closes the stream.
        try {
            stream.sendAndClose(request).onFailure {
                throw if (it is IllegalStateException) closedFailure(responses) else streamFailure(it)
            }
            for (message in responses) {
                onDelta(toDelta(message))
            }
        } catch (e: ConnectException) {
            throw failure(e, write = false)
        } finally {
            responses.cancel()
        }
    }

    fun host(): HostStatus? {
        val response = notFoundAsNull {
            call(write = false) { standard.host.status(HostStatusRequest.getDefaultInstance(), CLIENT_HEADERS) }
        } ?: return null
        return HostStatus(
            host = response.host,
            stats = response.statsList.map { stat ->
                HostStat(
                    label = stat.label,
                    value = stat.value,
                    percent = if (stat.hasPercent()) stat.percent else null,
                    level = stat.level,
                )
            },
        )
    }

    /** Every epic under the record root, live or folded, sorted by id. */
    fun epics(): List<EpicRow> {
        val response = call(write = false) { standard.epics.list(EpicsListRequest.getDefaultInstance(), CLIENT_HEADERS) }
        return response.epicsList.map { it.toEpicRow() }
    }

    /** One epic with its tasks and steps, or null when no epic has [id]. */
    fun epic(id: String): EpicDetail? {
        val request = EpicsGetRequest.newBuilder().setId(id).build()
        val response = notFoundAsNull { call(write = false) { standard.epics.get(request, CLIENT_HEADERS) } }
            ?: return null
        return EpicDetail(
            epic = response.epic.toEpicRow(),
            tasks = response.tasksList.map { it.toEpicTaskRow() },
            slices = response.slicesList.map { it.toEpicSliceRow() },
            steps = response.stepsList.map { it.toStepRow() },
            requirements = response.requirementsList.map { it.toEpicRequirementRow() },
            nodes = response.nodesList.map { it.toEpicNodeRow() },
            buildTasks = response.buildTasksList.map { it.toEpicBuildTaskRow() },
            summary = response.summary,
            contract = if (response.hasContract()) response.contract.toEpicContractRow() else null,
            questions = response.questionsList.map { it.toEpicQuestionRow() },
            paused = response.paused,
            parked = response.parked,
        )
    }

    /**
     * One page of an epic's history, oldest first: the entries after the cursor [after] names, at most [limit] of them.
     * A zero [limit] asks the server for its default. [EpicHistoryPage.next] is the cursor to pass for the next page.
     */
    fun history(id: String, after: String = "", limit: Int = 0): EpicHistoryPage {
        val request = EpicsHistoryRequest.newBuilder().setId(id).setAfter(after).setLimit(limit).build()
        val response = call(write = false) { standard.epics.history(request, CLIENT_HEADERS) }
        return EpicHistoryPage(
            entries = response.entriesList.map { it.toEpicHistoryRow() },
            next = response.next,
        )
    }

    /**
     * One artifact of an evidence run, or one of its logs when [name] is empty: the stderr log when [stderr] is set,
     * the stdout log otherwise. Null when the server answered not_found.
     */
    fun artifact(run: String, name: String, stderr: Boolean = false): EvidenceArtifactFile? {
        val request = EvidenceArtifactRequest.newBuilder().setRun(run).setName(name).setStderr(stderr).build()
        val response = notFoundAsNull { call(write = false) { standard.evidence.artifact(request, CLIENT_HEADERS) } }
            ?: return null
        return EvidenceArtifactFile(data = response.data.toByteArray(), mediaType = response.mediaType)
    }

    /** The pools in use and every owner hold. */
    fun pools(): PoolsView {
        val response = call(write = false) { standard.pools.status(PoolsStatusRequest.getDefaultInstance(), CLIENT_HEADERS) }
        return PoolsView(
            pools = response.poolsList.map { it.toPool() },
            holds = response.holdsList.map { it.toHold() },
        )
    }

    /** Holds [member] so no grant takes it. A write, so it is never retried. */
    fun holdMember(member: String) {
        val request = PoolsHoldRequest.newBuilder().setMember(member).build()
        call(write = true) { standard.pools.hold(request, CLIENT_HEADERS) }
    }

    /** Frees the held [member]. A write, so it is never retried. */
    fun freeMember(member: String) {
        val request = PoolsFreeRequest.newBuilder().setMember(member).build()
        call(write = true) { standard.pools.free(request, CLIENT_HEADERS) }
    }

    fun sendText(paneId: String, text: String) {
        val request = textRequest(paneId, text)
        call(write = true) { standard.panes.sendText(request, CLIENT_HEADERS) }
    }

    /**
     * Sends the same request as [sendText], but waits up to [composeReadTimeoutMillis] for the answer.
     * Throws [OrreryCallException] for an error answer, [NoAnswerException] when the request was
     * written and no answer came, and the transport's [IOException] when it never reached the server.
     */
    fun sendComposed(paneId: String, text: String) {
        val request = textRequest(paneId, text)
        call(write = true) { compose.panes.sendText(request, CLIENT_HEADERS) }
    }

    fun sendKeys(paneId: String, keys: List<String>, expectQuestion: String?): KeysResult {
        val request = SendKeysRequest.newBuilder()
            .setPane(paneId)
            .addAllKeys(keys)
            .also { builder -> if (expectQuestion != null) builder.setExpectQuestion(expectQuestion) }
            .build()
        return try {
            call(write = true) { standard.panes.sendKeys(request, CLIENT_HEADERS) }
            KeysResult.Sent
        } catch (e: OrreryCallException) {
            if (e.code == Code.ABORTED) KeysResult.PromptChanged else throw e
        }
    }

    fun ask(text: String): List<PickRow> {
        val request = AskRequest.newBuilder().setText(text).build()
        val response = call(write = true) { picking.picker.ask(request, CLIENT_HEADERS) }
        return response.rowsList.map { row ->
            PickRow(
                token = row.token,
                kind = row.kind,
                dir = row.dir,
                pane = row.pane,
                label = row.label,
                note = row.note,
                prompt = row.prompt,
            )
        }
    }

    fun run(token: String): String {
        val request = RunRequest.newBuilder().setToken(token).build()
        return call(write = true) { standard.picker.run(request, CLIENT_HEADERS) }.pane
    }

    /** Reports the pane read through [readUpTo], unix seconds. */
    fun markSeen(paneId: String, readUpTo: Long) {
        val request = MarkSeenRequest.newBuilder().setPane(paneId).setReadUpTo(readUpTo).build()
        call(write = true) { standard.panes.markSeen(request, CLIENT_HEADERS) }
    }

    /**
     * Sends [text] to a headless session as a prompt named [promptId], and waits up to
     * [composeReadTimeoutMillis] for the answer. Failures are as for [sendComposed].
     */
    fun say(sessionId: String, text: String, promptId: String) {
        val request = SayRequest.newBuilder().setId(sessionId).setText(text).setPromptId(promptId).build()
        call(write = true) { compose.control.say(request, CLIENT_HEADERS) }
    }

    /**
     * Starts a headless session in [dir] with [prompt] as its first message, under the client-chosen [id], and
     * waits up to [composeReadTimeoutMillis] for the answer. Returns the id the server answers. Failures are as
     * for [say].
     */
    fun start(dir: String, prompt: String, id: String): String {
        val request = StartRequest.newBuilder().setDir(dir).setPrompt(prompt).setId(id).build()
        return call(write = true) { compose.control.start(request, CLIENT_HEADERS) }.id
    }

    /** Answers the open request [requestId] of a headless session with [answer]. */
    fun answer(sessionId: String, requestId: String, answer: String) {
        val request = AnswerRequest.newBuilder().setId(sessionId).setRequestId(requestId).setAnswer(answer).build()
        call(write = true) { standard.control.answer(request, CLIENT_HEADERS) }
    }

    /** Interrupts the turn a headless session is running. */
    fun interrupt(sessionId: String) {
        val request = InterruptRequest.newBuilder().setId(sessionId).build()
        call(write = true) { standard.control.interrupt(request, CLIENT_HEADERS) }
    }

    /** Resumes an interrupted headless session's turn. A refused resume answers failed_precondition with the reason. */
    fun resume(sessionId: String) {
        val request = ResumeRequest.newBuilder().setId(sessionId).build()
        call(write = true) { standard.control.resume(request, CLIENT_HEADERS) }
    }

    /** Stops a headless session. */
    fun stop(sessionId: String) {
        val request = StopRequest.newBuilder().setId(sessionId).build()
        call(write = true) { standard.control.stop(request, CLIENT_HEADERS) }
    }

    /** Records that the user has seen a headless session. */
    fun sessionSeen(sessionId: String) {
        val request = SeenRequest.newBuilder().setId(sessionId).build()
        call(write = true) { standard.control.seen(request, CLIENT_HEADERS) }
    }

    /** Holds the labelled step [id]. A refused hold answers [StepCallResult.Refused] with the server's reason. */
    fun holdStep(id: String): StepCallResult {
        val request = StepsHoldRequest.newBuilder().setId(id).build()
        return stepCall { call(write = true) { standard.steps.hold(request, CLIENT_HEADERS) } }
    }

    /** Sends [text] to the labelled step [id] as a nudge. A refused nudge answers [StepCallResult.Refused]. */
    fun nudgeStep(id: String, text: String): StepCallResult {
        val request = StepsNudgeRequest.newBuilder().setId(id).setText(text).build()
        return stepCall { call(write = true) { standard.steps.nudge(request, CLIENT_HEADERS) } }
    }

    /** Stops the labelled step [id]. A refused stop answers [StepCallResult.Refused] with the server's reason. */
    fun stopStep(id: String): StepCallResult {
        val request = StepsStopRequest.newBuilder().setId(id).build()
        return stepCall { call(write = true) { standard.steps.stop(request, CLIENT_HEADERS) } }
    }

    /**
     * Pauses the orreryd epic [id], so its planner starts no turn. A refused pause answers [StepCallResult.Refused].
     */
    fun pauseEpic(id: String): StepCallResult {
        val request = EpicsPauseRequest.newBuilder().setId(id).build()
        return stepCall { call(write = true) { standard.epics.pause(request, CLIENT_HEADERS) } }
    }

    /** Clears the pause of the orreryd epic [id]. A refused resume answers [StepCallResult.Refused]. */
    fun resumeEpic(id: String): StepCallResult {
        val request = EpicsResumeRequest.newBuilder().setId(id).build()
        return stepCall { call(write = true) { standard.epics.resume(request, CLIENT_HEADERS) } }
    }

    /**
     * Stops the orreryd epic [id]: it is paused, and the step of its running turn stops. A refused stop answers
     * [StepCallResult.Refused].
     */
    fun stopEpic(id: String): StepCallResult {
        val request = EpicsStopRequest.newBuilder().setId(id).build()
        return stepCall { call(write = true) { standard.epics.stop(request, CLIENT_HEADERS) } }
    }

    /**
     * Ends the park of the orreryd epic [id], or with [node] set reruns the parked runs of that workflow node. A
     * refused retry answers [StepCallResult.Refused].
     */
    fun retryEpic(id: String, node: String = ""): StepCallResult {
        val request = EpicsRetryRequest.newBuilder().setId(id).setNode(node).build()
        return stepCall { call(write = true) { standard.epics.retry(request, CLIENT_HEADERS) } }
    }

    /**
     * Records [text] as the owner's answer on the orreryd epic [epicId], tied to the question [questionId] names, or as
     * a note when [questionId] is empty. A refused answer answers [StepCallResult.Refused].
     */
    fun answerEpic(epicId: String, questionId: String, text: String): StepCallResult {
        // The decision id is the caller's, and orreryd refuses an empty one. A fresh id makes each answer a decision.
        val request = EpicsAnswerRequest.newBuilder()
            .setEpicId(epicId)
            .setId(UUID.randomUUID().toString())
            .setText(text)
            .setQuestion(questionId)
            .build()
        return stepCall { call(write = true) { standard.epics.answer(request, CLIENT_HEADERS) } }
    }

    fun registerPush(endpoint: String, p256dh: String, auth: String) {
        val request = RegisterRequest.newBuilder().setEndpoint(endpoint).setP256Dh(p256dh).setAuth(auth).build()
        call(write = true) { standard.push.register(request, CLIENT_HEADERS) }
    }

    fun unregisterPush(endpoint: String) {
        val request = UnregisterRequest.newBuilder().setEndpoint(endpoint).build()
        call(write = true) { standard.push.unregister(request, CLIENT_HEADERS) }
    }

    private fun textRequest(paneId: String, text: String): SendTextRequest =
        SendTextRequest.newBuilder().setPane(paneId).setText(text).build()

    private fun EpicProto.toEpicRow(): EpicRow =
        EpicRow(
            id = id,
            repository = repository,
            slug = slug,
            outcome = outcome,
            changed = changed,
            live = live,
            waiting = waiting,
            runningSteps = runningStepsList.map { it.toStepRow() },
            endedSteps = endedSteps,
            problem = problem,
            source = source,
        )

    private fun EpicRequirementProto.toEpicRequirementRow(): EpicRequirementRow =
        EpicRequirementRow(
            id = id,
            text = text,
            evidence = evidence,
            kind = kind,
            conclusion = conclusion,
            runs = runsList.map { it.toEvidenceRunRow() },
        )

    private fun EpicNodeProto.toEpicNodeRow(): EpicNodeRow =
        EpicNodeRow(
            path = path,
            node = node,
            kind = kind,
            state = state,
            outcome = outcome,
            round = round,
            loopRound = loopRound,
            attempt = attempt,
            output = output,
            revision = revision,
            turns = turnsList.toList(),
            tasks = tasksList.toList(),
            runs = runsList.map { it.toEvidenceRunRow() },
            check = if (hasCheck()) check.toEpicNodeCheckRow() else null,
            step = step,
        )

    private fun EpicNodeCheckProto.toEpicNodeCheckRow(): EpicNodeCheckRow =
        EpicNodeCheckRow(
            state = state,
            exit = exit,
            timedOut = timedOut,
            notRun = notRun,
            stdout = stdout,
            stderr = stderr,
        )

    private fun EvidenceRunProto.toEvidenceRunRow(): EvidenceRunRow =
        EvidenceRunRow(
            id = id,
            phase = phase,
            source = source,
            state = state,
            exit = exit,
            timedOut = timedOut,
            notCaptured = notCaptured,
            verdict = verdict,
            judgeReason = judgeReason,
            passed = passed,
            revision = revision,
            created = created,
            ended = ended,
            logBytes = logBytes,
            stderrBytes = stderrBytes,
            artifacts = artifactsList.map { it.toEvidenceArtifactRow() },
            item = item,
        )

    private fun EvidenceArtifactProto.toEvidenceArtifactRow(): EvidenceArtifactRow =
        EvidenceArtifactRow(path = path, state = state, size = size, mediaType = mediaType)

    private fun EpicHistoryEntryProto.toEpicHistoryRow(): EpicHistoryRow {
        val row = EpicHistoryRow(time = time, cursor = cursor, kind = "")
        return when {
            hasTurn() -> row.copy(kind = "turn", turn = turn.toEpicTurnRow())
            hasTrigger() -> row.copy(kind = "trigger", trigger = trigger.toEpicTriggerRow())
            hasStep() -> row.copy(kind = "step", step = step.toStepRow())
            hasEvidence() -> row.copy(kind = "evidence", evidence = evidence.toEvidenceRunRow())
            hasDecision() -> row.copy(kind = "decision", decision = decision.toEpicDecisionRow())
            hasTell() -> row.copy(kind = "tell", tell = tell.toEpicTellRow())
            else -> row
        }
    }

    private fun EpicTurnProto.toEpicTurnRow(): EpicTurnRow =
        EpicTurnRow(
            seq = seq,
            step = step,
            state = state,
            reason = reason,
            outline = outline,
            outcome = outcome,
            decisions = decisionsList.toList(),
            started = started,
            ended = ended,
        )

    private fun EpicTriggerProto.toEpicTriggerRow(): EpicTriggerRow =
        EpicTriggerRow(id = id, cause = cause, state = state, turn = turn, created = created)

    private fun EpicDecisionProto.toEpicDecisionRow(): EpicDecisionRow =
        EpicDecisionRow(seq = seq, id = id, text = text, time = time, question = question)

    private fun EpicTellProto.toEpicTellRow(): EpicTellRow =
        EpicTellRow(seq = seq, text = text, at = at, told = told)

    private fun EpicStepProto.toStepRow(): StepRow =
        StepRow(
            id = id,
            task = task,
            agent = agent,
            end = end,
            status = status,
            held = held,
            started = started,
            ended = ended,
            doing = doing,
            checks = checksList.map { it.toStepCheckRow() },
            reason = reason,
            stage = stage,
            stageStarted = stageStarted,
            limited = limited,
            limitKind = limitKind,
            readOnly = readOnly,
            role = role,
            summary = summary,
            gateName = gateName,
            verdicts = verdictsList.map { it.toStepVerdictRow() },
        )

    private fun EpicStepCheckProto.toStepCheckRow(): StepCheckRow =
        StepCheckRow(
            round = round,
            name = name,
            ok = ok,
            stdout = stdout,
            stderr = stderr,
            detail = detail,
            tailStdout = tailStdout,
            tailStderr = tailStderr,
        )

    private fun EpicStepVerdictProto.toStepVerdictRow(): StepVerdictRow =
        StepVerdictRow(round = round, verdict = verdict, reason = reason)

    private fun EpicBuildTaskProto.toEpicBuildTaskRow(): EpicBuildTaskRow =
        EpicBuildTaskRow(
            id = id,
            title = title,
            status = status,
            stage = stage,
            red = if (hasRed()) red.toEvidenceRunRow() else null,
            green = if (hasGreen()) green.toEvidenceRunRow() else null,
            requirements = requirementsList.toList(),
            reviews = reviewsList.map { it.toTaskReviewRow() },
        )

    private fun EpicTaskReviewProto.toTaskReviewRow(): TaskReviewRow =
        TaskReviewRow(
            attempt = attempt,
            gate = gate,
            step = step,
            verdict = verdict,
            findings = findingsList.map { it.toTaskFindingRow() },
        )

    private fun EpicTaskFindingProto.toTaskFindingRow(): TaskFindingRow =
        TaskFindingRow(
            id = id,
            claim = claim,
            location = location,
            step = step,
            result = result,
            reason = reason,
        )

    private fun EpicTaskProto.toEpicTaskRow(): EpicTaskRow =
        EpicTaskRow(
            effort = effort,
            slug = slug,
            subject = subject,
            status = status,
            requirements = requirementsList.toList(),
        )

    private fun EpicQuestionProto.toEpicQuestionRow(): EpicQuestionRow =
        EpicQuestionRow(id = id, text = text, turn = turn, asked = asked)

    private fun EpicContractProto.toEpicContractRow(): EpicContractRow =
        EpicContractRow(
            title = title,
            approved = approved,
            intent = intent,
            failures = failuresList.map { it.toEpicFailureRow() },
            care = care,
            additions = additionsList.map { it.toEpicAdditionRow() },
            premises = premisesList.map { it.toEpicPremiseRow() },
            exclusions = exclusionsList.toList(),
            approach = approach,
            alternatives = alternativesList.map { it.toEpicAlternativeRow() },
            releaseSteps = releaseStepsList.map { it.toEpicReleaseStepRow() },
            releaseThen = releaseThen,
            checks = checks,
            base = base,
            accepted = accepted,
        )

    private fun EpicFailureProto.toEpicFailureRow(): EpicFailureRow =
        EpicFailureRow(id = id, text = text, rating = rating, response = response)

    private fun EpicAdditionProto.toEpicAdditionRow(): EpicAdditionRow =
        EpicAdditionRow(location = location, what = what, why = why, cost = cost)

    private fun EpicPremiseProto.toEpicPremiseRow(): EpicPremiseRow =
        EpicPremiseRow(id = id, text = text, ifWrong = ifWrong)

    private fun EpicAlternativeProto.toEpicAlternativeRow(): EpicAlternativeRow =
        EpicAlternativeRow(alternative = alternative, whyNot = whyNot, reconsider = reconsider)

    private fun EpicReleaseStepProto.toEpicReleaseStepRow(): EpicReleaseStepRow =
        EpicReleaseStepRow(step = step, target = target, undo = undo)

    private fun EpicSliceProto.toEpicSliceRow(): EpicSliceRow =
        EpicSliceRow(
            task = task,
            hue = hue,
            redTest = redTest,
            working = working,
            testsPass = testsPass,
            checks = checks,
            gateAccepted = gateAccepted,
            at = at,
            held = held,
            errorAt = errorAt,
        )

    private fun PoolStatusProto.toPool(): Pool =
        Pool(
            name = name,
            cap = cap,
            members = membersList.map { it.toPoolMember() },
            waiters = waitersList.map { it.toWaiter() },
        )

    private fun PoolMemberStatusProto.toPoolMember(): PoolMember =
        PoolMember(
            name = name,
            state = state,
            holder = if (hasHolder()) holder.toHolder() else null,
            heldSince = heldSince,
        )

    private fun PoolHolderProto.toHolder(): Holder =
        Holder(project = project, command = commandList.toList(), since = since, stopping = stopping)

    private fun PoolWaiterProto.toWaiter(): Waiter =
        Waiter(position = position, project = project, command = commandList.toList(), count = count, since = since)

    private fun PoolHoldProto.toHold(): Hold = Hold(member = member, since = since)

    private fun PaneProto.toPane(): Pane =
        Pane(
            id = id,
            host = host,
            session = session,
            window = window,
            pane = pane,
            cwd = cwd,
            command = command,
            state = state,
            summary = summary,
            label = label,
            changed = changed,
            seen = seen,
            question = question,
            questionHash = questionHash,
            tool = tool,
            newestReply = newestReply,
            lastTurnEnd = lastTurnEnd,
        )

    /** A headless session as the pane list shows it: its id is the session's, and it has no tmux pane. */
    private fun HeadlessSessionProto.toHeadlessPane(): Pane =
        Pane(
            id = id,
            host = "",
            session = "",
            window = 0,
            pane = 0,
            cwd = dir,
            command = "",
            state = state,
            summary = summary,
            label = label,
            changed = changed,
            seen = seen,
            question = question,
            questionHash = "",
            newestReply = newestReply,
            headless = true,
        )

    private fun ConversationItemProto.toConversationItem(): ConversationItem =
        ConversationItem(
            kind = kind,
            time = time,
            text = text,
            tool = tool,
            input = input,
            id = id,
            error = error,
            notice = notice,
            trigger = trigger,
            tokens = tokens,
        )

    private fun WaitingMessage.toWaitingItem(): WaitingItem =
        WaitingItem(text = text, time = time, canceled = canceled)

    private fun ConversationContextProto.toConversationContext(): ConversationContext =
        ConversationContext(tokens = tokens, window = window)

    private fun ConversationStateProto.toConversationState(pane: Pane?, request: SessionRequest?): ConversationState =
        ConversationState(
            origin = origin,
            goal = goal,
            waiting = waitingList.map { it.toWaitingItem() },
            context = if (hasContext()) context.toConversationContext() else null,
            pane = pane,
            request = request,
        )

    private fun WatchConversationResponse.toConversationDelta(): ConversationDelta =
        ConversationDelta(
            transcript = transcript,
            seq = seq,
            reset = reset,
            items = itemsList.map { it.toConversationItem() },
            state = if (hasState()) {
                state.toConversationState(
                    pane = if (state.hasPane()) state.pane.toPane() else null,
                    request = null,
                )
            } else {
                null
            },
        )

    private fun SessionsWatchConversationResponse.toSessionConversationDelta(): ConversationDelta =
        ConversationDelta(
            transcript = transcript,
            seq = seq,
            reset = reset,
            items = itemsList.map { it.toConversationItem() },
            state = if (hasState()) {
                state.toConversationState(
                    pane = if (state.hasSession()) state.session.toHeadlessPane() else null,
                    request = if (state.hasRequest()) state.request.toSessionRequest() else null,
                )
            } else {
                null
            },
            unfinished = unfinishedList.map { it.toUnfinishedText() },
        )

    private fun OpenRequestProto.toSessionRequest(): SessionRequest =
        SessionRequest(id = id, kind = kind, question = question, options = optionsList.toList())

    private fun EngineTextProto.toUnfinishedText(): UnfinishedText =
        UnfinishedText(
            messageId = messageId,
            index = index,
            parentToolUseId = parentToolUseId,
            kind = kind,
            text = text,
        )

    /**
     * Runs one Connect call and returns its message, or throws the exception its failure describes.
     * A tier that cannot be built, because the saved address is not a usable http or https URL, throws
     * an IOException before any request is written, as a bad address did at the base.
     */
    private fun <T> call(write: Boolean, rpc: suspend () -> ResponseMessage<T>): T =
        when (val response = runBlocking { rpc() }) {
            is ResponseMessage.Success -> response.message
            is ResponseMessage.Failure -> throw failure(response.cause, write)
        }

    /**
     * The exception for a failed call. A transport failure is the IOException itself, except that a
     * write whose request was sent is a [NoAnswerException]. A server answer is an [OrreryCallException].
     */
    private fun failure(error: ConnectException, write: Boolean): IOException {
        val transport = error.exception
        return when {
            transport is RequestSentException -> if (write) NoAnswerException(transport.transport) else transport.transport
            transport is IOException -> transport
            else -> OrreryCallException(error.code, error.message.orEmpty().ifEmpty { error.code.codeName })
        }
    }

    /** The failure a stream's send raised, as a call's failure is: a server answer, or the transport's IOException. */
    private fun streamFailure(error: Throwable): Throwable =
        if (error is ConnectException) failure(error, write = false) else error

    /**
     * The failure of a send that raised IllegalStateException because its stream was already closed. The stream
     * closes with the failure of the connection, so this waits for that close and maps its cause as [streamFailure]
     * does; a close with no cause is an IOException. No answer can arrive for a request that was not sent.
     */
    private suspend fun closedFailure(responses: ReceiveChannel<*>): Throwable {
        var result = responses.receiveCatching()
        while (!result.isClosed) result = responses.receiveCatching()
        val cause = result.exceptionOrNull() ?: return IOException("the stream closed before its request was sent")
        return streamFailure(cause)
    }

    /**
     * The answer of a step or epic call: [Refused] when the server answered failed_precondition, which carries the
     * reason the call was refused. Any other failure is thrown as [call] throws it.
     */
    private fun stepCall(rpc: () -> Any): StepCallResult =
        try {
            rpc()
            StepCallResult.Landed
        } catch (e: OrreryCallException) {
            if (e.code == Code.FAILED_PRECONDITION) StepCallResult.Refused(e.message.orEmpty()) else throw e
        }

    /** The answer, or null when the server answered not_found. */
    private inline fun <T> notFoundAsNull(read: () -> T): T? =
        try {
            read()
        } catch (e: OrreryCallException) {
            if (e.code == Code.NOT_FOUND) null else throw e
        }
}
