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
import io.github.joshsymonds.orrery.gen.orrery.v1.Account as AccountProto
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsAddRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRemoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AccountsRenameRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.BackgroundTask as BackgroundTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.BringBackRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Contract as ContractProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractDecision as ContractDecisionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractLeft as ContractLeftProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractLength as ContractLengthProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRefusal as ContractRefusalProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRequirementTask as ContractRequirementTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRequirementWork as ContractRequirementWorkProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractRowWrite as ContractRowWriteProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractShown as ContractShownProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractStateChange as ContractStateChangeProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractVersion as ContractVersionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractWorking as ContractWorkingProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsAcceptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsKeepRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsReopenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsSetAsideRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsShownRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ContractsStrikeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ControlClient
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DeclineRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Design as DesignProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignApproach as DesignApproachProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignChoice as DesignChoiceProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignContract as DesignContractProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignDecision as DesignDecisionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignDelegation as DesignDelegationProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignLedgerItem as DesignLedgerItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignLogLine as DesignLogLineProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignQuestion as DesignQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignStage as DesignStageProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignStep as DesignStepProto
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DesignsListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EngineText as EngineTextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic as EpicProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAddition as EpicAdditionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicAlternative as EpicAlternativeProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicBuildTask as EpicBuildTaskProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicContract as EpicContractProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicDecision as EpicDecisionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicFailure as EpicFailureProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicHistoryEntry as EpicHistoryEntryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicPremise as EpicPremiseProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicQuestion as EpicQuestionProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicReleaseStep as EpicReleaseStepProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement as EpicRequirementProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStep as EpicStepProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepCheck as EpicStepCheckProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicStepVerdict as EpicStepVerdictProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTaskFinding as EpicTaskFindingProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTaskReview as EpicTaskReviewProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTell as EpicTellProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTrigger as EpicTriggerProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicTurn as EpicTurnProto
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsAnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsCloseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsContinueRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsHistoryRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsMoveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsPauseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsReleaseRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsRetryRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsUnarchiveRequest
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
import io.github.joshsymonds.orrery.gen.orrery.v1.ListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ListedNotice as ListedNoticeProto
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticeKey
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesClient
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesReadAllRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NoticesSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NotNowRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesClient
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PendingEvent as PendingEventProto
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
import io.github.joshsymonds.orrery.gen.orrery.v1.PullQueuedRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PushClient
import io.github.joshsymonds.orrery.gen.orrery.v1.QueuedMessage as QueuedMessageProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RecordMaker as RecordMakerProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteContext as RouteContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteRegistry as RouteRegistryProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RouteSeat as RouteSeatProto
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesClient
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesGetRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RoutesSetContextAccountRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SayRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsOlderRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SessionsWatchConversationResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StartRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepFacts as StepFactsProto
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsClient
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UnregisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.WithdrawRequest
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

/** The choice an answer to a question names when the owner types the answer rather than picking a declared option. */
private const val CHOICE_OTHER = "other"

/** The answers to a list call (ListHeadless, Sessions.List) that mean the server has no such rows to list. */
private val ABSENT_LIST = setOf(Code.UNAVAILABLE, Code.UNIMPLEMENTED, Code.NOT_FOUND)

/**
 * The answers to Contracts.List that mean the server has no contracts to list. Unavailable is not among them: a
 * failed call keeps what the caller showed, so it throws.
 */
private val CONTRACTS_ABSENT = setOf(Code.UNIMPLEMENTED, Code.NOT_FOUND)

/** The answers to a move or a continue that mean no answer came back, so the write may or may not have landed. */
private val STEER_NO_ANSWER = setOf(Code.UNAVAILABLE, Code.DEADLINE_EXCEEDED, Code.UNKNOWN)

/** The answers to an accept, set aside or reopen that mean the server refused it, with its reason. */
private val CONTRACT_REFUSED = setOf(
    Code.FAILED_PRECONDITION,
    Code.INVALID_ARGUMENT,
    Code.ABORTED,
    Code.PERMISSION_DENIED,
    Code.NOT_FOUND,
)

/** The answers to a design's answer that mean the server refused it, with its reason: the codes its stage refuses with. */
private val DESIGN_REFUSED = setOf(
    Code.FAILED_PRECONDITION,
    Code.INVALID_ARGUMENT,
    Code.PERMISSION_DENIED,
    Code.NOT_FOUND,
)

/** The provider every account added from the phone is with. */
private const val LLM_PROVIDER = "anthropic"

/** The answers to an account or route write that mean the server refused it, with its reason. */
private val LLM_REFUSED = setOf(Code.INVALID_ARGUMENT, Code.NOT_FOUND, Code.FAILED_PRECONDITION)

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
    /** The pane's @orrery_id option, the id its session carries; "" when unset. A headless session's is its [id]. */
    val orreryId: String = "",
    /** True while a headless session is archived, by its own flag; a tmux pane's flag comes from Sessions.List. */
    val archived: Boolean = false,
    /** True while the owner has put this question off and not brought it back. */
    val putOff: Boolean = false,
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
    /** A `prompt` item's client-chosen prompt id, which a headless session's queue names; "" on other kinds or when the prompt has none. */
    val promptId: String = "",
    /** The id of the contract a `tool` item's call writes; "" when the item names none, on other kinds, or from an older server. */
    val contract: String = "",
    /** Who sent a `prompt` item's prompt (`brief`, `engine`, `owner` or `event`); "" on other kinds or when the prompt was stored without a sender. */
    val sender: String = "",
    /** The check round a send-back returns on a `prompt` item; 0 on other kinds or when the item is not a send-back. */
    val round: Int = 0,
    /** The id of the epic a `tool` item's epic_create call creates; "" when the item names none, on other kinds, or from an older server. */
    val epic: String = "",
    /** The facts of a step's end; null when the item carries none, on other kinds, or from an older server. */
    val stepFacts: StepFacts? = null,
    /** The lines a `result` item's successful file edit added; 0 otherwise, or from an older server. */
    val added: Long = 0L,
    /** The lines a `result` item's successful file edit removed; 0 otherwise, or from an older server. */
    val removed: Long = 0L,
    /** The X-Orrery-Client of a `prompt` item's prompt, such as orrery-android; "" on other kinds, when none was recorded, or from an older server. */
    val client: String = "",
    /** The id of the design a `design` item's stage record belongs to; "" on other kinds or when the item carries none. */
    val design: String = "",
    /** What a `design` item's stage record says was done; "" on other kinds or when the item carries none. */
    val designWhat: String = "",
    /** The ids a `design` item's stage record touched; empty on other kinds or when the item carries none. */
    val designIds: List<String> = emptyList(),
)

/**
 * The facts of a step's end: its loop [rounds] (0 when the step never returned), the [seconds] it ran, and whether its
 * changed lines were [counted]. [files], [added] and [removed] are set only when [counted].
 */
data class StepFacts(
    val rounds: Int,
    val seconds: Long,
    val counted: Boolean,
    val files: Long,
    val added: Long,
    val removed: Long,
)

/** A message Claude Code's queue holds for the pane. [canceled] is true when Claude Code cleared it on cancel. */
data class WaitingItem(val text: String, val time: Long, val canceled: Boolean)

/** An owner message a headless session has not started to Claude: its [state] and what it [waits] on. */
data class QueuedItem(val promptId: String, val text: String, val state: String, val waits: String)

/** A caller event held for a headless session, with what it [waits] on: speak, request or free. */
data class PendingItem(val id: String, val kind: String, val waits: String)

/** The pane's context usage: [tokens] in use out of a [window] of tokens. */
data class ConversationContext(val tokens: Long, val window: Long)

/**
 * A background task a headless session reported. [type] is "agent" or "shell"; [status] is "running", "done", "failed"
 * or "stopped". [activity] is its latest activity, [subagentType] the sub-agent an agent task runs, and [started] and
 * [ended] are unix seconds, [ended] 0 while it runs.
 */
data class BackgroundTask(
    val id: String,
    val type: String,
    val status: String,
    val description: String,
    val toolUseId: String = "",
    val subagentType: String = "",
    val activity: String = "",
    val started: Long = 0L,
    val ended: Long = 0L,
)

/**
 * [goal] is the pane's active Claude Code goal, or "" when there is none. [waiting] is the
 * queued messages, oldest first, or empty when there are none. [context] is the pane's
 * context usage, or null when the server predates it. [background] is the background tasks
 * a headless session reported, or empty when there are none.
 */
data class Conversation(
    val origin: String,
    val items: List<ConversationItem>,
    val goal: String = "",
    val waiting: List<WaitingItem> = emptyList(),
    val context: ConversationContext? = null,
    val background: List<BackgroundTask> = emptyList(),
)

/**
 * One conversation as a WatchConversation stream gives it. [state] is null when the message carried no state, and
 * [context] and [pane] are null when the state did not. [request] is the open request of a headless session, and
 * null when none is open or the stream is a pane's. [interrupting] is true while the owner's interrupt is recorded and
 * the session still works, and [interruptingSince] and [turnSince] are unix seconds, 0 when unknown. [pending] are the
 * caller events held for the session, oldest first. [background] is the background tasks of a headless session, and
 * empty when none are reported or the stream is a pane's.
 */
data class ConversationState(
    val origin: String,
    val goal: String,
    val waiting: List<WaitingItem>,
    val context: ConversationContext?,
    val pane: Pane?,
    val request: SessionRequest? = null,
    val interrupting: Boolean = false,
    val interruptingSince: Long = 0L,
    val turnSince: Long = 0L,
    val pending: List<PendingItem> = emptyList(),
    val background: List<BackgroundTask> = emptyList(),
)

/**
 * The request a headless session waits on. [options] are the answers the phone may send, and may be empty. [putOff] is
 * true when the owner has put the request off.
 */
data class SessionRequest(
    val id: String,
    val kind: String,
    val question: String,
    val options: List<String>,
    val putOff: Boolean = false,
)

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
 * client holds. [oldest] is the seq of the oldest item a reset carries, which the older pages are asked back from; it is
 * 0 on a delta that is not a reset, and on a reset with no items. [unfinished] is the text so far of each unfinished
 * block of a headless session, and empty on a pane stream. [queued] is the whole queue of owner messages a headless
 * session holds, and empty on a pane stream.
 */
data class ConversationDelta(
    val transcript: String,
    val seq: Long,
    val reset: Boolean,
    val items: List<ConversationItem>,
    val state: ConversationState?,
    val unfinished: List<UnfinishedText> = emptyList(),
    val queued: List<QueuedItem> = emptyList(),
    val oldest: Long = 0L,
)

/**
 * One page of a headless session's main-thread items, older than the seq a request gave, oldest first. [oldest] is the
 * seq of the page's oldest item, 0 when it holds none; [more] is whether items older than the page remain.
 */
data class SessionOlderPage(val items: List<ConversationItem>, val oldest: Long, val more: Boolean)

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

/** One stored LLM account. [fingerprint] tells its token apart without showing it; [created] is unix seconds. */
data class LlmAccount(
    val id: String,
    val name: String,
    val provider: String,
    val plan: String,
    val fingerprint: String,
    val created: Long,
)

/** One seat a request may go through: how it authenticates upstream ([authMode]) and the options that shape its requests. */
data class LlmSeat(
    val id: String,
    val authMode: String,
    val upstream: String,
    val model: String,
    val billing: String,
    val speed: String,
    val awsProfile: String,
    val awsRegion: String,
    val modelMapEnvFile: String,
)

/**
 * One context that chooses seats: [defaultSeat] when no entry in [models] names the request's model, and [account],
 * whose token the context's forward seats send. An empty [account] means the session's own login.
 */
data class LlmContext(val id: String, val defaultSeat: String, val models: Map<String, String>, val account: String)

/** The route registry: the context a request uses when it names none, and the seats and contexts among which it chooses. */
data class LlmRoutes(val defaultContext: String, val seats: List<LlmSeat>, val contexts: List<LlmContext>)

/** The accounts the server stores, and the route registry they are used in. */
data class LlmAccounts(val accounts: List<LlmAccount>, val routes: LlmRoutes)

/**
 * The answer to an account or route write: [Done] with what the server stored, [Refused] when the server refused it
 * with [code] and its [reason], or [NoAnswer] when no answer came, so the write may or may not have landed.
 */
sealed interface LlmWrite<out T> {
    data class Done<out T>(val answer: T) : LlmWrite<T>
    data class Refused(val code: Code, val reason: String) : LlmWrite<Nothing>
    data object NoAnswer : LlmWrite<Nothing>
}

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
 * limit of [limitKind] holds the step, and [limitResets] is when that limit resets, in unix seconds, 0 when unknown.
 * [readOnly] is whether the step is read-only, [role] is the role it plays, and
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
    val limitResets: Long = 0L,
    val readOnly: Boolean = false,
    val role: String = "",
    val summary: String = "",
    val gateName: String = "",
    val verdicts: List<StepVerdictRow> = emptyList(),
)

/**
 * One epic from orreryd. [runningSteps] are the epic's steps with no end yet, held ones included. [phaseAt] is when the
 * epic's phase was last set, in unix seconds; 0 when unknown. [seen] is when the owner last saw the epic, in unix
 * seconds, from its latest seen call; 0 when the owner has not seen it. [archived] is true while the owner has archived
 * the epic. [steering] is the id of the conversation that steers the epic, empty when none; [path] is the absolute
 * repository path. [paused] and [pauseSource] say whether, and why, the epic is paused. [parked] is why the epic is
 * parked, empty when it is not. [openQuestions] counts the questions still open.
 */
data class EpicRow(
    val id: String,
    val repository: String,
    val slug: String,
    val outcome: String,
    val changed: Long,
    val live: Boolean,
    val runningSteps: List<StepRow>,
    val endedSteps: Int,
    val phaseAt: Long = 0L,
    val seen: Long = 0L,
    val archived: Boolean = false,
    val steering: String = "",
    val path: String = "",
    val paused: Boolean = false,
    val pauseSource: String = "",
    val parked: String = "",
    val openQuestions: Int = 0,
)

/**
 * One notice in the notice history, from List. [pane], [session], [epic], [limit] and [alert] are the notice's key: exactly
 * one is set. [blockKind] and [blockRef] name the block of the epic's page the notice opens on, empty when it opens the
 * epic view. [time] is when the router delivered the notice, in unix seconds; [read] is whether the owner has read it.
 */
data class NoticeRow(
    val pane: String,
    val session: String,
    val epic: String,
    val limit: String,
    val alert: String,
    val state: String,
    val title: String,
    val body: String,
    val blockKind: String,
    val blockRef: String,
    val time: Long,
    val read: Boolean,
)

/**
 * One contract from Contracts.List: its current version's repository, title and care, and its state. [version] is the
 * newest version number; [openDecisions] counts the decisions of that version with no answer; [conversation] is the
 * conversation that proposed the contract. [created] and [changed] are unix seconds.
 */
data class ContractRow(
    val id: String,
    val repository: String,
    val title: String,
    val care: String,
    val state: String,
    val version: Long,
    val openDecisions: Long,
    val conversation: String,
    val created: Long,
    val changed: Long,
)

/**
 * One contract of a design, as Designs.List serves it: its [position] in the design's start order, counted from 1,
 * the ids of the contracts it starts [after], and the [epic] made from it with that epic's [epicPhase], both "" when
 * none has been made.
 */
data class DesignContractRow(
    val id: String,
    val title: String,
    val repository: String,
    val state: String,
    val position: Long,
    val after: List<String>,
    val epic: String,
    val epicPhase: String,
)

/**
 * One design as Designs.List serves it: its [conversation] that works it, its [created] time in unix seconds, its
 * [state] phrase, and its [contracts] in the order served. [ofOne] is true for a design of one: the contract that reads
 * as it was proposed with no design.
 */
data class DesignRow(
    val id: String,
    val title: String,
    val conversation: String,
    val created: Long,
    val ofOne: Boolean,
    val state: String,
    val contracts: List<DesignContractRow>,
)

/** One decision a contract's version asks the owner to answer. [answer] is "" while the decision is open. */
data class ContractDecisionRow(
    val id: String,
    val question: String,
    val options: List<String>,
    val recommendation: String,
    val reason: String,
    val row: String,
    val answer: String,
)

/**
 * One stored version of a contract: its [contract] body, [requirements] and [decisions], the [words] and [writer] it
 * came with, and when it was stored in unix seconds. [changedRows] are the ids whose text differs from the version
 * before, and [changedSections] the section names whose values do; both are empty for version 1.
 */
data class ContractVersionRow(
    val version: Long,
    val contract: EpicContractRow,
    val requirements: List<EpicRequirementRow>,
    val decisions: List<ContractDecisionRow>,
    val words: String,
    val writer: String,
    val time: Long,
    val changedRows: List<String>,
    val changedSections: List<String>,
)

/**
 * One change of a contract's state: [by] is "owner" or "session", and [session] is the writer's id when by is "session".
 * [maker] is who made the change, null when none is recorded.
 */
data class ContractStateRow(
    val state: String,
    val version: Long,
    val by: String,
    val session: String,
    val words: String,
    val time: Long,
    val maker: RecordMakerRow? = null,
)

/**
 * One contract with its [versions], oldest first, its [states], oldest first, and its [stage] as Contracts.Get serves
 * it: the working copy and the records the stage made.
 */
data class ContractRecordRow(
    val contract: ContractRow,
    val versions: List<ContractVersionRow>,
    val states: List<ContractStateRow>,
    val stage: ContractStageRow = ContractStageRow(),
)

/** The answer to an owner's accept, set aside or reopen: the contract as it stands, or the server's reason it was refused. */
sealed interface ContractCallResult {
    data class Done(val contract: ContractRow) : ContractCallResult
    data class Refused(val reason: String) : ContractCallResult
}

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

/**
 * A decision recorded on an epic. [question] is the Q<n> it answers, empty for a note. [by] is who recorded it,
 * 'owner', 'conversation' or 'orreryd', and empty for a decision stored before makers were recorded. [session] is the
 * conversation's session id when [by] is 'conversation', empty otherwise.
 */
data class EpicDecisionRow(
    val seq: Long,
    val id: String,
    val text: String,
    val time: Long,
    val question: String,
    val by: String = "",
    val session: String = "",
)

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

/**
 * One task of an orreryd epic's plan, with its build: [stage] is the stage the build is on, empty before its first
 * unit. [red] and [green] are the evidence runs the build names, null until it names one. [requirements] are the
 * requirement ids the task covers, in order. [reviews] are the build's reviews, in attempt then position order. [place]
 * and [tone] are where the task stands in its lifecycle and how it stands there, as serve computes them.
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
    val place: String = "",
    val tone: String = "",
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
    val reason: String = "",
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

/** One choice an orreryd epic's planner question declares: its [id], the answer that chooses it, and what it [does]. */
data class EpicQuestionChoiceRow(val id: String, val does: String)

/**
 * One open question an orreryd epic's planner asked, in the order they were asked. [choices] are the choices the
 * question declares, in order. [recommendedChoice] is the choice the planner recommends, "" when it recommends none,
 * and [recommendedWhy] is why. [escalated] is when the question was escalated, in unix seconds, 0 while it is not.
 * [escalationReason] is why it was escalated, [escalationBy] who escalated it (owner, conversation or orreryd) and
 * [escalationSession] the session that did when a conversation did. Those three are "" while it is not escalated.
 * [steererHeld] is whether the epic's steerer answers the question, so the owner is not asked: a steering session
 * hears the epic and the question is not escalated.
 */
data class EpicQuestionRow(
    val id: String,
    val text: String,
    val turn: Long,
    val asked: Long,
    val choices: List<EpicQuestionChoiceRow> = emptyList(),
    val recommendedChoice: String = "",
    val recommendedWhy: String = "",
    val escalated: Long = 0L,
    val escalationReason: String = "",
    val escalationBy: String = "",
    val escalationSession: String = "",
    val steererHeld: Boolean = false,
)

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

internal fun EpicReleaseStepProto.toEpicReleaseStepRow(): EpicReleaseStepRow =
    EpicReleaseStepRow(step = step, target = target, undo = undo)

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
 * One epic with its build tasks and every step labelled with it, oldest step first.
 * [requirements] are an orreryd epic's requirements in contract order, empty for a record epic. [buildTasks] are an
 * orreryd epic's planned tasks in the order they were planned, empty for a record epic. [summary] is the epic's summary
 * paragraph, empty until it is written. [contract] is the accepted contract, null for a record epic. [questions] are
 * the open questions in the order they were asked. [paused] is whether the epic's planner starts no turn, and [parked]
 * is why the epic is parked, empty when it is not. [plannerParked] is whether the planner holds that park, and
 * [steererParked] whether the epic's steerer holds it until it escalates it.
 * [repository] is the absolute path of the git repository the epic belongs to, empty when none is named.
 * [releaseProof] is the contract's release steps with the state their release runs gave them, in contract order,
 * empty for a record epic. [reviews] are the verdicts of the epic-stage reviewers by position, empty before its review.
 * [ledger] is the end-of-epic review ledger in ledger order, with each finding's closures, empty before its review
 * freezes. [releases] are the release runs in the order orreryd reads them, empty before its release. [report] is
 * what the epic shipped, null until the epic ends and writes it. [supersededBy] is the epic a close named as its
 * replacement, empty when its close named none or it is not closed.
 */
data class EpicDetail(
    val epic: EpicRow,
    val steps: List<StepRow>,
    val requirements: List<EpicRequirementRow> = emptyList(),
    val buildTasks: List<EpicBuildTaskRow> = emptyList(),
    val summary: String = "",
    val contract: EpicContractRow? = null,
    val questions: List<EpicQuestionRow> = emptyList(),
    val paused: Boolean = false,
    val parked: String = "",
    val plannerParked: Boolean = false,
    val steererParked: Boolean = false,
    val repository: String = "",
    val releaseProof: List<EpicReleaseProofRow> = emptyList(),
    val reviews: List<EpicReviewVerdictRow> = emptyList(),
    val ledger: List<EpicLedgerFindingRow> = emptyList(),
    val releases: List<EpicReleaseRunRow> = emptyList(),
    val report: EpicReportRow? = null,
    val supersededBy: String = "",
)

/** What a hold, nudge, stop or epic call came to: it landed, or orreryd refused it with its reason. */
sealed interface StepCallResult {
    data object Landed : StepCallResult
    data class Refused(val message: String) : StepCallResult
}

/** What a move or a continue came to: the session that steers the epic now, or orreryd's refusal with its reason. */
sealed interface SteerResult {
    data class Steered(val session: String) : SteerResult
    data class Refused(val message: String) : SteerResult
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
        val contracts = ContractsClient(protocol)
        val epics = EpicsClient(protocol)
        val designs = DesignsClient(protocol)
        val evidence = EvidenceClient(protocol)
        val notices = NoticesClient(protocol)
        val steps = StepsClient(protocol)
        val pools = PoolsClient(protocol)
        val accounts = AccountsClient(protocol)
        val routes = RoutesClient(protocol)
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
        val sessions = absentAsEmpty {
            call(write = false) { standard.sessions.listHeadless(ListHeadlessRequest.getDefaultInstance(), CLIENT_HEADERS) }
                .sessionsList
        }
        return sessions.map { it.toHeadlessPane() }
    }

    /**
     * The ids of the tracked sessions the owner has archived; a tmux pane is archived when its [Pane.orreryId] is in
     * this set. A server with no session list gives an empty set.
     */
    fun archivedSessions(): Set<String> {
        val sessions = absentAsEmpty {
            call(write = false) { standard.sessions.list(ListRequest.getDefaultInstance(), CLIENT_HEADERS) }.sessionsList
        }
        return sessions.filter { it.archived }.map { it.id }.toSet()
    }

    /**
     * Every contract, created ascending, with its current version. A server with no contracts to list gives an empty
     * list; an unavailable answer throws, as a failed call.
     */
    fun contracts(): List<ContractRow> =
        absentAsEmpty(CONTRACTS_ABSENT) {
            call(write = false) { standard.contracts.list(ContractsListRequest.getDefaultInstance(), CLIENT_HEADERS) }
                .contractsList.map { it.toContractRow() }
        }

    /**
     * Every design, with its contracts in the order the server served them. A server that answers unavailable,
     * unimplemented or not_found has no designs to list, so it gives an empty list; any other failure throws.
     */
    fun designs(): List<DesignRow> =
        absentAsEmpty {
            call(write = false) { standard.designs.list(DesignsListRequest.getDefaultInstance(), CLIENT_HEADERS) }
                .designsList.map { it.toDesignRow() }
        }

    /**
     * One design with its approaches, choice, log, delegations and stage, or null when no design has [id]. A design
     * whose response carries no stage has a null stage.
     */
    fun design(id: String): DesignRecordRow? {
        val request = DesignsGetRequest.newBuilder().setId(id).build()
        val response = notFoundAsNull { call(write = false) { standard.designs.get(request, CLIENT_HEADERS) } }
            ?: return null
        return DesignRecordRow(
            design = response.design.toDesignRow(),
            approaches = response.approachesList.map { it.toDesignApproachRow() },
            choice = if (response.hasChoice()) response.choice.toDesignChoiceRow() else null,
            log = response.logList.map { it.toDesignLogRow() },
            delegations = response.delegationsList.map { it.toDesignDelegationRow() },
            stage = if (response.hasStage()) response.stage.toDesignStageRow(response.design.id) else null,
        )
    }

    /**
     * Answers question [question] of [design] with [answer], or with the question's recommendation when [accept] is set.
     * A refused answer answers [DesignAnswerResult.Refused] with the server's reason. A landed one answers
     * [DesignAnswerResult.Done] with the question as the design's stage holds it after the answer, read back with [design].
     */
    fun answerDesignQuestion(design: String, question: Long, answer: String, accept: Boolean): DesignAnswerResult {
        val request = DesignsAnswerRequest.newBuilder()
            .setDesign(design)
            .setQuestion(question)
            .setAnswer(answer)
            .setAccept(accept)
            .build()
        try {
            call(write = true) { standard.designs.answer(request, CLIENT_HEADERS) }
        } catch (e: OrreryCallException) {
            if (e.code in DESIGN_REFUSED) return DesignAnswerResult.Refused(e.message.orEmpty())
            throw e
        }
        val answered = this.design(design)?.stage?.questions?.firstOrNull { it.id == question }
            ?: throw IOException("design $design holds no question $question after its answer")
        return DesignAnswerResult.Done(answered)
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

    /**
     * One page of headless session [sessionId]'s main-thread items older than the seq [before], the newest page first.
     * [SessionOlderPage.oldest] is the cursor to pass as before for the next page.
     */
    fun sessionOlder(sessionId: String, before: Long): SessionOlderPage {
        val request = SessionsOlderRequest.newBuilder()
            .setId(sessionId)
            .setBefore(before)
            .setLimit(50)
            .build()
        val response = call(write = false) { standard.sessions.older(request, CLIENT_HEADERS) }
        return SessionOlderPage(
            items = response.itemsList.map { it.toConversationItem() },
            oldest = response.oldest,
            more = response.more,
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
            steps = response.stepsList.map { it.toStepRow() },
            requirements = response.requirementsList.map { it.toEpicRequirementRow() },
            buildTasks = response.buildTasksList.map { it.toEpicBuildTaskRow() },
            summary = response.summary,
            contract = if (response.hasContract()) response.contract.toEpicContractRow() else null,
            questions = response.questionsList.map { it.toEpicQuestionRow() },
            paused = response.paused,
            parked = response.parked,
            plannerParked = response.plannerParked,
            steererParked = response.steererParked,
            repository = response.repository,
            releaseProof = response.releaseStepsList.map { it.toEpicReleaseProofRow() },
            reviews = response.reviewsList.map { it.toEpicReviewVerdictRow() },
            ledger = response.ledgerList.map { it.toEpicLedgerFindingRow() },
            releases = response.releasesList.map { it.toEpicReleaseRunRow() },
            report = if (response.hasReport()) response.report.toEpicReportRow() else null,
            supersededBy = response.supersededBy,
        )
    }

    /**
     * One contract with its versions and state changes, and the records its stage made, or null when no contract has
     * [id].
     */
    fun contract(id: String): ContractRecordRow? {
        val request = ContractsGetRequest.newBuilder().setId(id).build()
        val response = notFoundAsNull { call(write = false) { standard.contracts.get(request, CLIENT_HEADERS) } }
            ?: return null
        return ContractRecordRow(
            contract = response.contract.toContractRow(),
            versions = response.versionsList.map { it.toContractVersionRow() },
            states = response.statesList.map { it.toContractStateRow() },
            stage = response.toContractStageRow(),
        )
    }

    /** Accepts the contract [id] with no words. A refused accept answers [ContractCallResult.Refused] with the server's reason. */
    fun acceptContract(id: String): ContractCallResult {
        val request = ContractsAcceptRequest.newBuilder().setId(id).build()
        return contractCall { call(write = true) { standard.contracts.accept(request, CLIENT_HEADERS) }.contract }
    }

    /** Sets the contract [id] aside with [words]. A refused set aside answers [ContractCallResult.Refused]. */
    fun setAsideContract(id: String, words: String): ContractCallResult {
        val request = ContractsSetAsideRequest.newBuilder().setId(id).setWords(words).build()
        return contractCall { call(write = true) { standard.contracts.setAside(request, CLIENT_HEADERS) }.contract }
    }

    /** Reopens the contract [id] with no words. A refused reopen answers [ContractCallResult.Refused]. */
    fun reopenContract(id: String): ContractCallResult {
        val request = ContractsReopenRequest.newBuilder().setId(id).build()
        return contractCall { call(write = true) { standard.contracts.reopen(request, CLIENT_HEADERS) }.contract }
    }

    /**
     * Keeps the addition [addition] of the contract [id] with [words]. [addition] is the addition's id, A and its 1-based
     * position among the contract's additions. A refused keep answers [ContractCallResult.Refused].
     */
    fun keepAddition(id: String, addition: String, words: String = ""): ContractCallResult {
        val request = ContractsKeepRequest.newBuilder().setId(id).setAddition(addition).setWords(words).build()
        return contractCall { call(write = true) { standard.contracts.keep(request, CLIENT_HEADERS) }.contract }
    }

    /**
     * Strikes the addition [addition] of the contract [id] with [words], as [keepAddition] names it. The answer is the
     * contract as it stands after the striking. A refused strike answers [ContractCallResult.Refused].
     */
    fun strikeAddition(id: String, addition: String, words: String = ""): ContractCallResult {
        val request = ContractsStrikeRequest.newBuilder().setId(id).setAddition(addition).setWords(words).build()
        return contractCall { call(write = true) { standard.contracts.strike(request, CLIENT_HEADERS) }.contract }
    }

    /**
     * Marks the rows [rows] of version [version] of the contract [id] as shown to the owner, and answers the rows the
     * server marked. A refused mark answers no rows.
     */
    fun markShown(id: String, version: Long, rows: List<String>): List<String> {
        val request = ContractsShownRequest.newBuilder().setId(id).setVersion(version).addAllRows(rows).build()
        return try {
            call(write = true) { standard.contracts.shown(request, CLIENT_HEADERS) }.rowsList.toList()
        } catch (e: OrreryCallException) {
            if (e.code in CONTRACT_REFUSED) emptyList() else throw e
        }
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

    /** The LLM accounts the server stores, then the route registry. A failed read throws, as [pools] does. */
    fun llmAccounts(): LlmAccounts {
        val response = call(write = false) { standard.accounts.list(AccountsListRequest.getDefaultInstance(), CLIENT_HEADERS) }
        return LlmAccounts(
            accounts = response.accountsList.map { it.toLlmAccount() },
            routes = llmRoutes(),
        )
    }

    /** The route registry as it stands, read on its own. */
    fun llmRoutes(): LlmRoutes {
        val response = call(write = false) { standard.routes.get(RoutesGetRequest.getDefaultInstance(), CLIENT_HEADERS) }
        return response.registry.toLlmRoutes()
    }

    /**
     * Stores an account named [name] on [plan] with [token], with the anthropic provider. A write, so it is never
     * retried. The token is sent and kept nowhere else.
     */
    fun addAccount(name: String, plan: String, token: String): LlmWrite<LlmAccount> {
        val request = AccountsAddRequest.newBuilder()
            .setName(name)
            .setProvider(LLM_PROVIDER)
            .setPlan(plan)
            .setToken(token)
            .build()
        return llmWrite { call(write = true) { standard.accounts.add(request, CLIENT_HEADERS) }.account.toLlmAccount() }
    }

    /** Gives the account [id] the new name [name]. A write, so it is never retried. */
    fun renameAccount(id: String, name: String): LlmWrite<LlmAccount> {
        val request = AccountsRenameRequest.newBuilder().setId(id).setName(name).build()
        return llmWrite { call(write = true) { standard.accounts.rename(request, CLIENT_HEADERS) }.account.toLlmAccount() }
    }

    /** Removes the account [id]. A write, so it is never retried. */
    fun removeAccount(id: String): LlmWrite<Unit> {
        val request = AccountsRemoveRequest.newBuilder().setId(id).build()
        return llmWrite {
            call(write = true) { standard.accounts.remove(request, CLIENT_HEADERS) }
            Unit
        }
    }

    /**
     * Sets the account [context]'s forward seats send to [account], or to the session's own login when [account] is
     * empty. Answers with the route registry as it stands after the change. A write, so it is never retried.
     */
    fun setContextAccount(context: String, account: String): LlmWrite<LlmRoutes> {
        val request = RoutesSetContextAccountRequest.newBuilder().setContext(context).setAccount(account).build()
        return llmWrite {
            call(write = true) { standard.routes.setContextAccount(request, CLIENT_HEADERS) }.registry.toLlmRoutes()
        }
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

    /**
     * Takes the open question named here off the owner's list until [bringBack]. [kind] is "pane" for a tmux pane, with
     * [questionHash] its question hash and [changed] when it started waiting; "headless" for a headless session, with
     * [requestId] its open request. A refused not-now answers [StepCallResult.Refused].
     */
    fun notNow(kind: String, id: String, requestId: String, questionHash: String, changed: Long): StepCallResult {
        val request = NotNowRequest.newBuilder()
            .setKind(kind)
            .setId(id)
            .setRequestId(requestId)
            .setQuestionHash(questionHash)
            .setChanged(changed)
            .build()
        return stepCall { call(write = true) { standard.control.notNow(request, CLIENT_HEADERS) } }
    }

    /**
     * Puts back the question [notNow] took off the owner's list, named the same way. A refused bring-back answers
     * [StepCallResult.Refused].
     */
    fun bringBack(kind: String, id: String, requestId: String, questionHash: String, changed: Long): StepCallResult {
        val request = BringBackRequest.newBuilder()
            .setKind(kind)
            .setId(id)
            .setRequestId(requestId)
            .setQuestionHash(questionHash)
            .setChanged(changed)
            .build()
        return stepCall { call(write = true) { standard.control.bringBack(request, CLIENT_HEADERS) } }
    }

    /**
     * Denies the open question named here and ends its turn, named as [notNow] names it, but with no changed time. A
     * refused decline answers [StepCallResult.Refused].
     */
    fun decline(kind: String, id: String, requestId: String, questionHash: String): StepCallResult {
        val request = DeclineRequest.newBuilder()
            .setKind(kind)
            .setId(id)
            .setRequestId(requestId)
            .setQuestionHash(questionHash)
            .build()
        return stepCall { call(write = true) { standard.control.decline(request, CLIENT_HEADERS) } }
    }

    /** Interrupts the turn a headless session is running. Returns the texts the server answers, in send order. */
    fun interrupt(sessionId: String): List<String> {
        val request = InterruptRequest.newBuilder().setId(sessionId).build()
        return call(write = true) { standard.control.interrupt(request, CLIENT_HEADERS) }.textsList.toList()
    }

    /**
     * Cancels the queued owner message [promptId] of a headless session. Returns the texts the server answers, which
     * are empty when Claude already took the message.
     */
    fun withdraw(sessionId: String, promptId: String): List<String> {
        val request = WithdrawRequest.newBuilder().setId(sessionId).setPromptId(promptId).build()
        return call(write = true) { standard.control.withdraw(request, CLIENT_HEADERS) }.textsList.toList()
    }

    /** Takes back the messages Claude Code's queue holds for the pane [paneId]. Returns the text the server answers. */
    fun pullQueued(paneId: String): String {
        val request = PullQueuedRequest.newBuilder().setPane(paneId).build()
        return call(write = true) { standard.panes.pullQueued(request, CLIENT_HEADERS) }.text
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

    /** Records that the owner opened the notice on the pane [paneId], so the shade marks it seen. */
    fun paneNoticeSeen(paneId: String) {
        noticeSeen(NoticeKey.newBuilder().setPane(paneId).build())
    }

    /** Records that the owner opened the notice on the headless session [sessionId], so the shade marks it seen. */
    fun sessionNoticeSeen(sessionId: String) {
        noticeSeen(NoticeKey.newBuilder().setSession(sessionId).build())
    }

    private fun noticeSeen(key: NoticeKey) {
        val request = NoticesSeenRequest.newBuilder().setKey(key).build()
        call(write = true) { standard.notices.seen(request, CLIENT_HEADERS) }
    }

    /** The notice history, newest first, as the server answers: every notice timed within the last 14 days. */
    fun notices(): List<NoticeRow> {
        val response = call(write = false) { standard.notices.list(NoticesListRequest.getDefaultInstance(), CLIENT_HEADERS) }
        return response.noticesList.map { it.toNoticeRow() }
    }

    /** Records that the owner opened the notice [row] in the notice history, so every client marks it seen. */
    fun noticeRowSeen(row: NoticeRow) {
        noticeSeen(
            NoticeKey.newBuilder()
                .setPane(row.pane)
                .setSession(row.session)
                .setEpic(row.epic)
                .setLimit(row.limit)
                .setAlert(row.alert)
                .build(),
        )
    }

    /** Marks every notice in the notice history read. */
    fun readAllNotices() {
        call(write = true) { standard.notices.readAll(NoticesReadAllRequest.getDefaultInstance(), CLIENT_HEADERS) }
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
     * Releases the orreryd epic [id], running its release steps. [undoNone] names the positions, from 1, of its
     * undo-none release steps to run again; empty names none. A refused release answers [StepCallResult.Refused], and
     * any other failure throws.
     */
    fun releaseEpic(id: String, undoNone: List<Int>): StepCallResult {
        val request = EpicsReleaseRequest.newBuilder().setId(id).addAllUndoNone(undoNone).build()
        return stepCall { call(write = true) { standard.epics.release(request, CLIENT_HEADERS) } }
    }

    /**
     * Closes the orreryd epic [id] as [kind], which is superseded, shipped or abandoned, and [by], the epic that
     * replaces it, which a superseded close requires. A refused close answers [StepCallResult.Refused], and any other
     * failure throws.
     */
    fun closeEpic(id: String, kind: String, by: String): StepCallResult {
        val request = EpicsCloseRequest.newBuilder().setId(id).setKind(kind).setBy(by).build()
        return stepCall { call(write = true) { standard.epics.close(request, CLIENT_HEADERS) } }
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
     * Moves the steering of the orreryd epic [id] to [session], so that session's questions and events are the
     * epic's from now on. A refused move answers [SteerResult.Refused] with the server's reason.
     */
    fun moveEpic(id: String, session: String): SteerResult {
        val request = EpicsMoveRequest.newBuilder().setId(id).setSession(session).build()
        return steerCall {
            call(write = true) { standard.epics.move(request, CLIENT_HEADERS) }
            SteerResult.Steered(session)
        }
    }

    /**
     * Starts a steering session for the orreryd epic [id] and moves the epic's steering to it. The answer names the
     * started session, which is "" when the server started none. A refused continue answers [SteerResult.Refused].
     */
    fun continueEpic(id: String): SteerResult {
        val request = EpicsContinueRequest.newBuilder().setId(id).build()
        return steerCall {
            SteerResult.Steered(call(write = true) { standard.epics.`continue`(request, CLIENT_HEADERS) }.session)
        }
    }

    /**
     * Records [text] as the owner's answer on the orreryd epic [epicId], tied to the question [questionId] names with
     * the choice [choice] (a declared choice's id, or 'other' by default), or as a note with no choice when
     * [questionId] is empty. A refused answer answers [StepCallResult.Refused].
     */
    fun answerEpic(epicId: String, questionId: String, text: String, choice: String = CHOICE_OTHER): StepCallResult {
        // The decision id is the caller's, and orreryd refuses an empty one. A fresh id makes each answer a decision.
        val request = EpicsAnswerRequest.newBuilder()
            .setEpicId(epicId)
            .setId(UUID.randomUUID().toString())
            .setText(text)
            .setQuestion(questionId)
            .setChoice(if (questionId.isEmpty()) "" else choice)
            .build()
        return stepCall { call(write = true) { standard.epics.answer(request, CLIENT_HEADERS) } }
    }

    /**
     * Records that the owner saw the orreryd epic [id], so its seen mark is the time of this call. A refused seen
     * answers [StepCallResult.Refused].
     */
    fun seeEpic(id: String): StepCallResult {
        val request = EpicsSeenRequest.newBuilder().setId(id).build()
        return stepCall { call(write = true) { standard.epics.seen(request, CLIENT_HEADERS) } }
    }

    /**
     * Archives the list row of [kind] (epic, session or headless) named [id], and no other row. A refused archive answers
     * [StepCallResult.Refused].
     */
    fun archive(kind: String, id: String): StepCallResult {
        val request = EpicsArchiveRequest.newBuilder().setKind(kind).setId(id).build()
        return stepCall { call(write = true) { standard.epics.archive(request, CLIENT_HEADERS) } }
    }

    /**
     * Unarchives the list row of [kind] (epic, session or headless) named [id], and no other row. A refused unarchive
     * answers [StepCallResult.Refused].
     */
    fun unarchive(kind: String, id: String): StepCallResult {
        val request = EpicsUnarchiveRequest.newBuilder().setKind(kind).setId(id).build()
        return stepCall { call(write = true) { standard.epics.unarchive(request, CLIENT_HEADERS) } }
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

    private fun ListedNoticeProto.toNoticeRow(): NoticeRow =
        NoticeRow(
            pane = key.pane,
            session = key.session,
            epic = key.epic,
            limit = key.limit,
            alert = key.alert,
            state = state,
            title = title,
            body = body,
            blockKind = block.kind,
            blockRef = block.ref,
            time = time,
            read = read,
        )

    private fun EpicProto.toEpicRow(): EpicRow =
        EpicRow(
            id = id,
            repository = repository,
            slug = slug,
            outcome = outcome,
            changed = changed,
            live = live,
            runningSteps = runningStepsList.map { it.toStepRow() },
            endedSteps = endedSteps,
            phaseAt = phaseAt,
            seen = seen,
            archived = archived,
            steering = steering,
            path = path,
            paused = paused,
            pauseSource = pauseSource,
            parked = parked,
            openQuestions = openQuestions,
        )

    private fun ContractProto.toContractRow(): ContractRow =
        ContractRow(
            id = id,
            repository = repository,
            title = title,
            care = care,
            state = state,
            version = version,
            openDecisions = openDecisions,
            conversation = conversation,
            created = created,
            changed = changed,
        )

    private fun DesignProto.toDesignRow(): DesignRow =
        DesignRow(
            id = id,
            title = title,
            conversation = conversation,
            created = created,
            ofOne = ofOne,
            state = state,
            contracts = contractsList.map { it.toDesignContractRow() },
        )

    private fun DesignContractProto.toDesignContractRow(): DesignContractRow =
        DesignContractRow(
            id = id,
            title = title,
            repository = repository,
            state = state,
            position = position,
            after = afterList.toList(),
            epic = epic,
            epicPhase = epicPhase,
        )

    private fun ContractVersionProto.toContractVersionRow(): ContractVersionRow =
        ContractVersionRow(
            version = version,
            contract = contract.toEpicContractRow(),
            requirements = requirementsList.map { it.toEpicRequirementRow() },
            decisions = decisionsList.map { it.toContractDecisionRow() },
            words = words,
            writer = writer,
            time = time,
            changedRows = changedRowsList.toList(),
            changedSections = changedSectionsList.toList(),
        )

    private fun ContractDecisionProto.toContractDecisionRow(): ContractDecisionRow =
        ContractDecisionRow(
            id = id,
            question = question,
            options = optionsList.toList(),
            recommendation = recommendation,
            reason = reason,
            row = row,
            answer = answer,
        )

    private fun ContractStateChangeProto.toContractStateRow(): ContractStateRow =
        ContractStateRow(
            state = state,
            version = version,
            by = by,
            session = session,
            words = words,
            time = time,
            maker = if (hasMaker()) maker.toRecordMakerRow() else null,
        )

    private fun RecordMakerProto.toRecordMakerRow(): RecordMakerRow =
        RecordMakerRow(kind = kind, quote = quote, reason = reason, delegation = delegation, session = session)

    private fun DesignApproachProto.toDesignApproachRow(): DesignApproachRow =
        DesignApproachRow(
            seq = seq,
            name = name,
            shape = shape,
            cost = cost,
            wins = wins,
            loses = loses,
            reconsider = reconsider,
            research = researchList.toList(),
            recommended = recommended,
            reason = reason,
            time = time,
        )

    private fun DesignChoiceProto.toDesignChoiceRow(): DesignChoiceRow =
        DesignChoiceRow(
            approach = approach,
            words = words,
            maker = if (hasMaker()) maker.toRecordMakerRow() else null,
            time = time,
        )

    private fun DesignLogLineProto.toDesignLogRow(): DesignLogRow =
        DesignLogRow(
            seq = seq,
            time = time,
            maker = if (hasMaker()) maker.toRecordMakerRow() else null,
            session = session,
            what = what,
            ids = idsList.toList(),
            words = words,
        )

    private fun DesignDecisionProto.toDesignDecisionRow(): DesignDecisionRow =
        DesignDecisionRow(kind = kind, ref = ref, text = text, reason = reason, time = time)

    private fun DesignDelegationProto.toDesignDelegationRow(): DesignDelegationRow =
        DesignDelegationRow(
            seq = seq,
            kinds = kindsList.toList(),
            message = message,
            session = session,
            time = time,
            decisions = decisionsList.map { it.toDesignDecisionRow() },
        )

    private fun DesignStageProto.toDesignStageRow(designId: String): DesignStageRow =
        DesignStageRow(
            steps = stepsList.map { it.toDesignStepRow() },
            questions = questionsList.map { it.toDesignQuestionRow(designId) },
            ledger = ledgerList.map { it.toDesignLedgerItemRow() },
            frozen = frozen,
            closureSkipped = closureSkipped,
            discoveryCounted = discoveryCounted,
            closureCounted = closureCounted,
        )

    private fun DesignStepProto.toDesignStepRow(): DesignStepRow =
        DesignStepRow(
            step = step,
            kind = kind,
            subject = subject,
            created = created,
            status = status,
            report = report,
            verdict = verdict,
            counted = counted,
        )

    private fun DesignQuestionProto.toDesignQuestionRow(designId: String): DesignQuestionRow =
        DesignQuestionRow(
            design = designId,
            id = id,
            contract = contract,
            text = text,
            options = optionsList.toList(),
            recommendation = recommendation,
            asked = asked,
            answer = answer,
            accepted = accepted,
            maker = if (hasMaker()) maker.toRecordMakerRow() else null,
            answered = answered,
        )

    private fun DesignLedgerItemProto.toDesignLedgerItemRow(): DesignLedgerItemRow =
        DesignLedgerItemRow(
            id = id,
            contract = contract,
            origin = origin,
            impact = impact,
            evidence = evidence,
            change = change,
            disposition = disposition,
            reason = reason,
            boundary = boundary,
            maker = if (hasMaker()) maker.toRecordMakerRow() else null,
            closureWord = closureWord,
            closureNote = closureNote,
        )

    private fun ContractsGetResponse.toContractStageRow(): ContractStageRow =
        ContractStageRow(
            working = if (hasWorking()) working.toContractWorkingRow() else null,
            refusals = refusalsList.map { it.toContractRefusalRow() },
            writes = rowsList.map { it.toContractRowWriteRow() },
            left = leftList.map { it.toContractLeftRow() },
            work = workList.map { it.toContractRequirementWorkRow() },
            shown = shownList.map { it.toContractShownRow() },
            length = if (hasLength()) length.toContractLengthRow() else null,
        )

    private fun ContractWorkingProto.toContractWorkingRow(): ContractWorkingRow =
        ContractWorkingRow(
            version = version,
            contract = contract.toEpicContractRow(),
            requirements = requirementsList.map { it.toEpicRequirementRow() },
            decisions = decisionsList.map { it.toContractDecisionRow() },
            writer = writer,
            updated = updated,
            changedRows = changedRowsList.toList(),
        )

    private fun ContractRefusalProto.toContractRefusalRow(): ContractRefusalRow =
        ContractRefusalRow(seq = seq, version = version, field = field, text = text, session = session, time = time)

    private fun ContractRowWriteProto.toContractRowWriteRow(): ContractRowWriteRow =
        ContractRowWriteRow(
            seq = seq,
            version = version,
            row = row,
            action = action,
            depends = depends,
            words = words,
            maker = if (hasMaker()) maker.toRecordMakerRow() else null,
            time = time,
        )

    private fun ContractLeftProto.toContractLeftRow(): ContractLeftRow =
        ContractLeftRow(kind = kind, ref = ref, text = text)

    private fun ContractRequirementWorkProto.toContractRequirementWorkRow(): ContractRequirementWorkRow =
        ContractRequirementWorkRow(
            id = id,
            text = text,
            state = state,
            tasks = tasksList.map { it.toContractRequirementTaskRow() },
            proof = if (hasProof()) proof.toEvidenceRunRow() else null,
        )

    private fun ContractRequirementTaskProto.toContractRequirementTaskRow(): ContractRequirementTaskRow =
        ContractRequirementTaskRow(id = id, title = title, status = status, place = place)

    private fun ContractShownProto.toContractShownRow(): ContractShownRow =
        ContractShownRow(version = version, row = row, time = time)

    private fun ContractLengthProto.toContractLengthRow(): ContractLengthRow =
        ContractLengthRow(lines = lines, warn = warn, strong = strong)

    private fun EpicRequirementProto.toEpicRequirementRow(): EpicRequirementRow =
        EpicRequirementRow(
            id = id,
            text = text,
            evidence = evidence,
            kind = kind,
            conclusion = conclusion,
            runs = runsList.map { it.toEvidenceRunRow() },
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
        EpicDecisionRow(
            seq = seq,
            id = id,
            text = text,
            time = time,
            question = question,
            by = by,
            session = session,
        )

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
            limitResets = limitResets,
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
            place = place,
            tone = tone,
        )

    private fun EpicTaskReviewProto.toTaskReviewRow(): TaskReviewRow =
        TaskReviewRow(
            attempt = attempt,
            gate = gate,
            step = step,
            verdict = verdict,
            findings = findingsList.map { it.toTaskFindingRow() },
            reason = reason,
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

    private fun EpicQuestionProto.toEpicQuestionRow(): EpicQuestionRow =
        EpicQuestionRow(
            id = id,
            text = text,
            turn = turn,
            asked = asked,
            choices = choicesList.map { EpicQuestionChoiceRow(id = it.id, does = it.does) },
            recommendedChoice = recommendation.choice,
            recommendedWhy = recommendation.why,
            escalated = escalated,
            escalationReason = escalationReason,
            escalationBy = escalationBy,
            escalationSession = escalationSession,
            steererHeld = steererHeld,
        )

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

    private fun AccountProto.toLlmAccount(): LlmAccount =
        LlmAccount(id = id, name = name, provider = provider, plan = plan, fingerprint = fingerprint, created = created)

    private fun RouteRegistryProto.toLlmRoutes(): LlmRoutes =
        LlmRoutes(
            defaultContext = defaultContext,
            seats = seatsList.map { it.toLlmSeat() },
            contexts = contextsList.map { it.toLlmContext() },
        )

    private fun RouteSeatProto.toLlmSeat(): LlmSeat =
        LlmSeat(
            id = id,
            authMode = authMode,
            upstream = upstream,
            model = model,
            billing = billing,
            speed = speed,
            awsProfile = awsProfile,
            awsRegion = awsRegion,
            modelMapEnvFile = modelMapEnvFile,
        )

    private fun RouteContextProto.toLlmContext(): LlmContext =
        LlmContext(id = id, defaultSeat = defaultSeat, models = modelsMap.toMap(), account = account)

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
            orreryId = orreryId,
            putOff = putOff,
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
            archived = archived,
            orreryId = id,
            putOff = putOff,
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
            promptId = promptId,
            contract = contract,
            sender = sender,
            round = round,
            epic = epic,
            stepFacts = if (hasStepFacts()) stepFacts.toStepFacts() else null,
            added = added,
            removed = removed,
            client = client,
            design = design.design,
            designWhat = design.what,
            designIds = design.idsList.toList(),
        )

    private fun StepFactsProto.toStepFacts(): StepFacts =
        StepFacts(
            rounds = rounds,
            seconds = seconds,
            counted = counted,
            files = files,
            added = added,
            removed = removed,
        )

    private fun WaitingMessage.toWaitingItem(): WaitingItem =
        WaitingItem(text = text, time = time, canceled = canceled)

    private fun PendingEventProto.toPendingItem(): PendingItem =
        PendingItem(id = id, kind = kind, waits = waits)

    private fun QueuedMessageProto.toQueuedItem(): QueuedItem =
        QueuedItem(promptId = promptId, text = text, state = state, waits = waits)

    private fun ConversationContextProto.toConversationContext(): ConversationContext =
        ConversationContext(tokens = tokens, window = window)

    private fun BackgroundTaskProto.toBackgroundTask(): BackgroundTask =
        BackgroundTask(
            id = id,
            type = type,
            status = status,
            description = description,
            toolUseId = toolUseId,
            subagentType = subagentType,
            activity = activity,
            started = started,
            ended = ended,
        )

    private fun ConversationStateProto.toConversationState(pane: Pane?, request: SessionRequest?): ConversationState =
        ConversationState(
            origin = origin,
            goal = goal,
            waiting = waitingList.map { it.toWaitingItem() },
            context = if (hasContext()) context.toConversationContext() else null,
            pane = pane,
            request = request,
            interrupting = interrupting,
            interruptingSince = interruptingSince,
            turnSince = turnSince,
            pending = pendingList.map { it.toPendingItem() },
            background = backgroundList.map { it.toBackgroundTask() },
        )

    private fun WatchConversationResponse.toConversationDelta(): ConversationDelta =
        ConversationDelta(
            transcript = transcript,
            seq = seq,
            reset = reset,
            items = itemsList.map { it.toConversationItem() },
            oldest = oldest,
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
            oldest = oldest,
            state = if (hasState()) {
                state.toConversationState(
                    pane = if (state.hasSession()) state.session.toHeadlessPane() else null,
                    request = if (state.hasRequest()) state.request.toSessionRequest() else null,
                )
            } else {
                null
            },
            unfinished = unfinishedList.map { it.toUnfinishedText() },
            queued = queuedList.map { it.toQueuedItem() },
        )

    private fun OpenRequestProto.toSessionRequest(): SessionRequest =
        SessionRequest(id = id, kind = kind, question = question, options = optionsList.toList(), putOff = putOff)

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

    /**
     * The answer of an accept, set aside or reopen: [ContractCallResult.Refused] when the server refused it with one of
     * [CONTRACT_REFUSED], with its reason. Any other failure is thrown as [call] throws it.
     */
    private fun contractCall(rpc: () -> ContractProto): ContractCallResult =
        try {
            ContractCallResult.Done(rpc().toContractRow())
        } catch (e: OrreryCallException) {
            if (e.code in CONTRACT_REFUSED) ContractCallResult.Refused(e.message.orEmpty()) else throw e
        }

    /**
     * The answer of an account or route write: [LlmWrite.Done] with the answer, [LlmWrite.Refused] when the server
     * answered with one of [LLM_REFUSED], with its reason, and [LlmWrite.NoAnswer] when no answer came back or the
     * server answered with any other code. A failure that never reached the server is thrown as [call] throws it.
     */
    private inline fun <T> llmWrite(rpc: () -> T): LlmWrite<T> =
        try {
            LlmWrite.Done(rpc())
        } catch (e: OrreryCallException) {
            if (e.code in LLM_REFUSED) LlmWrite.Refused(e.code, e.message.orEmpty()) else LlmWrite.NoAnswer
        } catch (e: NoAnswerException) {
            LlmWrite.NoAnswer
        }

    /**
     * The answer of a move or a continue: [SteerResult.Refused] when the server answered with any code but the ones
     * that mean no answer came back. Those, and every failure that is not a server answer, are thrown as [call]
     * throws them.
     */
    private inline fun steerCall(rpc: () -> SteerResult): SteerResult =
        try {
            rpc()
        } catch (e: OrreryCallException) {
            if (e.code in STEER_NO_ANSWER) throw e else SteerResult.Refused(e.message.orEmpty())
        }

    /** The answer, or null when the server answered not_found. */
    private inline fun <T> notFoundAsNull(read: () -> T): T? =
        try {
            read()
        } catch (e: OrreryCallException) {
            if (e.code == Code.NOT_FOUND) null else throw e
        }

    /** The rows of a list call, or none when the server answered one of [absent]: it has no such rows to list. */
    private inline fun <T> absentAsEmpty(absent: Set<Code> = ABSENT_LIST, read: () -> List<T>): List<T> =
        try {
            read()
        } catch (e: OrreryCallException) {
            if (e.code in absent) emptyList() else throw e
        }
}
