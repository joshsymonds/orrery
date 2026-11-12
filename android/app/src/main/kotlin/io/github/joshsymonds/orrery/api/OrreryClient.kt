package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import com.connectrpc.ConnectException
import com.connectrpc.ProtocolClientConfig
import com.connectrpc.ResponseMessage
import com.connectrpc.extensions.GoogleJavaLiteProtobufStrategy
import com.connectrpc.impl.ProtocolClient
import com.connectrpc.okhttp.ConnectOkHttpClient
import com.connectrpc.protocols.NetworkProtocol
import io.github.joshsymonds.orrery.gen.orrery.v1.AskRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationContext as ConversationContextProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationItem as ConversationItemProto
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ConversationState as ConversationStateProto
import io.github.joshsymonds.orrery.gen.orrery.v1.HostClient
import io.github.joshsymonds.orrery.gen.orrery.v1.HostStatusRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.MarkSeenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesClient
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.PickerClient
import io.github.joshsymonds.orrery.gen.orrery.v1.PushClient
import io.github.joshsymonds.orrery.gen.orrery.v1.RegisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.RunRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ScreenRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendTextRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.UnregisterRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WaitingMessage
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.WatchConversationResponse
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.net.MalformedURLException
import java.util.concurrent.TimeUnit
import kotlin.time.Duration

private const val TIMEOUT_MS = 10_000
private const val CLIENT_HEADER = "orrery-android"
private val CLIENT_HEADERS: Map<String, List<String>> = mapOf("X-Orrery-Client" to listOf(CLIENT_HEADER))

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
 * [context] and [pane] are null when the state did not.
 */
data class ConversationState(
    val origin: String,
    val goal: String,
    val waiting: List<WaitingItem>,
    val context: ConversationContext?,
    val pane: Pane?,
)

/**
 * One step of a WatchConversation stream: the [items] after [seq] in [transcript]. A [reset] delta replaces what the
 * client holds.
 */
data class ConversationDelta(
    val transcript: String,
    val seq: Long,
    val reset: Boolean,
    val items: List<ConversationItem>,
    val state: ConversationState?,
)

data class HostStat(val label: String, val value: String, val percent: Double?, val level: String)

data class HostStatus(val host: String, val stats: List<HostStat>)

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
 * Blocking client for the orrery phone API over Connect. Each call is one unary request. A write
 * call is never retried, so a dropped connection cannot send it twice.
 */
class OrreryClient(
    baseUrl: String,
    private val composeReadTimeoutMillis: Int = 25_000,
    private val pickerReadTimeoutMillis: Int = 90_000,
    private val streamReadTimeoutMillis: Int = 10_000,
) {
    private val root = baseUrl.removeSuffix("/")

    private val http: OkHttpClient = OkHttpClient.Builder()
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
    }

    /**
     * Built on first use: connect-kotlin rejects an unusable address when a tier is built, with
     * IllegalArgumentException for a scheme other than http or https and ktor's URLParserException
     * (an IllegalStateException) for an address it cannot parse, and that must not crash the
     * constructor. Each use of an unusable address raises [MalformedURLException].
     */
    private val standardTier = lazy { Services(TIMEOUT_MS) }
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

    fun screen(paneId: String): String {
        val request = ScreenRequest.newBuilder().setPane(paneId).setAnsi(true).setHistory(500).build()
        val response = call(write = false) { standard.panes.screen(request, CLIENT_HEADERS) }
        return response.screen.toString(Charsets.UTF_8)
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
        val stream = streaming.panes.watchConversation(CLIENT_HEADERS)
        val responses = stream.responseChannel()
        // The finally also covers the send, so a cancel or a failure while the request is sent closes the stream.
        try {
            stream.sendAndClose(request).onFailure { throw streamFailure(it) }
            for (message in responses) {
                onDelta(message.toConversationDelta())
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

    private fun ConversationStateProto.toConversationState(): ConversationState =
        ConversationState(
            origin = origin,
            goal = goal,
            waiting = waitingList.map { it.toWaitingItem() },
            context = if (hasContext()) context.toConversationContext() else null,
            pane = if (hasPane()) pane.toPane() else null,
        )

    private fun WatchConversationResponse.toConversationDelta(): ConversationDelta =
        ConversationDelta(
            transcript = transcript,
            seq = seq,
            reset = reset,
            items = itemsList.map { it.toConversationItem() },
            state = if (hasState()) state.toConversationState() else null,
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

    /** The answer, or null when the server answered not_found. */
    private inline fun <T> notFoundAsNull(read: () -> T): T? =
        try {
            read()
        } catch (e: OrreryCallException) {
            if (e.code == Code.NOT_FOUND) null else throw e
        }
}
