package io.github.joshsymonds.orrery.api

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

private const val TIMEOUT_MS = 10_000
private const val CLIENT_HEADER = "orrery-android"

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
)

/** A message Claude Code's queue holds for the pane. [canceled] is true when Claude Code cleared it on cancel. */
data class WaitingItem(val text: String, val time: Long, val canceled: Boolean)

/**
 * [goal] is the pane's active Claude Code goal, or "" when there is none. [waiting] is the
 * queued messages, oldest first, or empty when there are none.
 */
data class Conversation(
    val origin: String,
    val items: List<ConversationItem>,
    val goal: String = "",
    val waiting: List<WaitingItem> = emptyList(),
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

/** The string at [key], or "" when the key is absent or null: keys a server may predate. */
private fun JSONObject.lenientString(key: String): String = if (isNull(key)) "" else optString(key, "")

/** The objects of the array at [key], or none when the key is absent or null: keys a server may predate. */
private fun JSONObject.lenientArray(key: String): List<JSONObject> {
    val array = optJSONArray(key) ?: return emptyList()
    return List(array.length()) { index -> array.getJSONObject(index) }
}

/** The server answered with a non-2xx [status]. */
class OrreryHttpException(val status: Int, message: String) : IOException(message)

/**
 * The request was written, but no answer came within the compose read timeout, or the
 * connection dropped while waiting. The server may still have acted on the request.
 */
class NoAnswerException(cause: IOException) : IOException("no answer to the request", cause)

/**
 * Blocking client for the orrery phone API. Each call is one request on its
 * own connection; nothing is kept open between calls.
 */
class OrreryClient(
    baseUrl: String,
    private val composeReadTimeoutMillis: Int = 25_000,
    private val pickerReadTimeoutMillis: Int = 90_000,
) {
    private val root = baseUrl.removeSuffix("/")

    private class Response(val status: Int, val body: String)

    fun panes(): List<Pane> {
        val array = JSONArray(getOk("/v1/panes"))
        return List(array.length()) { index ->
            val json = array.getJSONObject(index)
            Pane(
                id = json.getString("id"),
                host = json.getString("host"),
                session = json.getString("session"),
                window = json.getInt("window"),
                pane = json.getInt("pane"),
                cwd = json.getString("cwd"),
                command = json.getString("command"),
                state = json.getString("state"),
                summary = json.getString("summary"),
                label = json.getString("label"),
                changed = json.getLong("changed"),
                seen = json.getLong("seen"),
                question = json.getString("question"),
                questionHash = json.getString("question_hash"),
                tool = json.optString("tool", ""),
                newestReply = json.optLong("newest_reply", 0L),
            )
        }
    }

    fun screen(paneId: String): String =
        getOk(panePath(paneId, "screen") + "?ansi=1&history=500")

    fun conversation(paneId: String): Conversation? {
        val body = getOrNotFound(panePath(paneId, "conversation") + "?limit=50") ?: return null
        val json = JSONObject(body)
        val array = json.getJSONArray("items")
        return Conversation(
            origin = json.getString("origin"),
            items = List(array.length()) { index ->
                val item = array.getJSONObject(index)
                ConversationItem(
                    kind = item.getString("kind"),
                    time = item.getLong("time"),
                    text = item.getString("text"),
                    tool = item.getString("tool"),
                    input = item.getString("input"),
                    id = item.getString("id"),
                    error = item.getBoolean("error"),
                    notice = item.lenientString("notice"),
                )
            },
            goal = json.lenientString("goal"),
            waiting = json.lenientArray("waiting").map { row ->
                WaitingItem(
                    text = row.lenientString("text"),
                    time = row.optLong("time", 0L),
                    canceled = row.optBoolean("canceled", false),
                )
            },
        )
    }

    fun host(): HostStatus? {
        val body = getOrNotFound("/v1/host") ?: return null
        val json = JSONObject(body)
        val array = json.getJSONArray("stats")
        return HostStatus(
            host = json.getString("host"),
            stats = List(array.length()) { index ->
                val stat = array.getJSONObject(index)
                HostStat(
                    label = stat.getString("label"),
                    value = stat.getString("value"),
                    percent = if (stat.isNull("percent")) null else stat.getDouble("percent"),
                    level = stat.getString("level"),
                )
            },
        )
    }

    fun sendText(paneId: String, text: String) {
        post(panePath(paneId, "text"), JSONObject().put("text", text))
    }

    /**
     * Posts the same request as [sendText], but waits up to [composeReadTimeoutMillis] for the answer.
     * Throws [OrreryHttpException] for a non-2xx answer, [NoAnswerException] when the request was
     * written and no answer came, and any other [IOException] when the connection failed before the
     * request was written.
     */
    fun sendComposed(paneId: String, text: String) {
        val path = panePath(paneId, "text")
        val response = sendAwaitingAnswer(path, JSONObject().put("text", text))
        checkOk("POST", path, response)
    }

    fun sendKeys(paneId: String, keys: List<String>, expectQuestion: String?): KeysResult {
        val path = panePath(paneId, "keys")
        val body = JSONObject().put("keys", JSONArray(keys))
        if (expectQuestion != null) {
            body.put("expect_question", expectQuestion)
        }
        val response = send("POST", path, body)
        if (response.status == 409) {
            return KeysResult.PromptChanged
        }
        checkOk("POST", path, response)
        return KeysResult.Sent
    }

    fun ask(text: String): List<PickRow> {
        val response = send("POST", "/v1/picker/ask", JSONObject().put("text", text), pickerReadTimeoutMillis)
        checkAnswered(response)
        val array = JSONObject(response.body).getJSONArray("rows")
        return List(array.length()) { index ->
            val row = array.getJSONObject(index)
            PickRow(
                token = row.getString("token"),
                kind = row.getString("kind"),
                dir = row.getString("dir"),
                pane = row.getString("pane"),
                label = row.getString("label"),
                note = row.getString("note"),
                prompt = row.getBoolean("prompt"),
            )
        }
    }

    fun run(token: String): String {
        val response = send("POST", "/v1/picker/run", JSONObject().put("token", token))
        checkAnswered(response)
        return JSONObject(response.body).getString("pane")
    }

    /** Reports the pane read through [readUpTo], unix seconds. */
    fun markSeen(paneId: String, readUpTo: Long) {
        post(panePath(paneId, "seen"), JSONObject().put("read_up_to", readUpTo))
    }

    fun registerPush(endpoint: String, p256dh: String, auth: String) {
        post(
            "/v1/push/register",
            JSONObject().put("endpoint", endpoint).put("p256dh", p256dh).put("auth", auth),
        )
    }

    fun unregisterPush(endpoint: String) {
        val response = send("DELETE", "/v1/push/register", JSONObject().put("endpoint", endpoint))
        checkOk("DELETE", "/v1/push/register", response)
    }

    private fun panePath(paneId: String, suffix: String): String =
        "/v1/panes/${paneId.replace("%", "%25")}/$suffix"

    private fun getOk(path: String): String {
        val response = send("GET", path, null)
        checkOk("GET", path, response)
        return response.body
    }

    private fun getOrNotFound(path: String): String? {
        val response = send("GET", path, null)
        if (response.status == 404) {
            return null
        }
        checkOk("GET", path, response)
        return response.body
    }

    private fun post(path: String, body: JSONObject) {
        val response = send("POST", path, body)
        checkOk("POST", path, response)
    }

    private fun checkOk(method: String, path: String, response: Response) {
        if (response.status !in 200..299) {
            throw OrreryHttpException(response.status, "HTTP ${response.status} from $method $path")
        }
    }

    /** The picker's non-2xx answers carry a plain-text reason; it is the message, or the status when the body is empty. */
    private fun checkAnswered(response: Response) {
        if (response.status !in 200..299) {
            throw OrreryHttpException(response.status, response.body.trim().ifEmpty { "HTTP ${response.status}" })
        }
    }

    private fun send(method: String, path: String, body: JSONObject?, readTimeoutMillis: Int = TIMEOUT_MS): Response {
        val connection = URL(root + path).openConnection() as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.connectTimeout = TIMEOUT_MS
            connection.readTimeout = readTimeoutMillis
            if (body != null) {
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json")
                connection.setRequestProperty("X-Orrery-Client", CLIENT_HEADER)
                connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
            }
            return readResponse(connection)
        } finally {
            connection.disconnect()
        }
    }

    /**
     * Like [send] for a POST with a body, but the body is streamed at a fixed length so it is on the
     * wire once the stream closes, and the read timeout is [composeReadTimeoutMillis].
     */
    private fun sendAwaitingAnswer(path: String, body: JSONObject): Response {
        val bytes = body.toString().toByteArray(Charsets.UTF_8)
        val connection = URL(root + path).openConnection() as HttpURLConnection
        try {
            connection.requestMethod = "POST"
            connection.connectTimeout = TIMEOUT_MS
            connection.readTimeout = composeReadTimeoutMillis
            connection.doOutput = true
            connection.setFixedLengthStreamingMode(bytes.size)
            connection.setRequestProperty("Content-Type", "application/json")
            connection.setRequestProperty("X-Orrery-Client", CLIENT_HEADER)
            connection.outputStream.use { it.write(bytes) }
            try {
                return readResponse(connection)
            } catch (e: IOException) {
                throw NoAnswerException(e)
            }
        } finally {
            connection.disconnect()
        }
    }

    private fun readResponse(connection: HttpURLConnection): Response {
        val status = connection.responseCode
        val stream = if (status in 200..299) connection.inputStream else connection.errorStream
        val text = stream?.use { it.readBytes().toString(Charsets.UTF_8) }.orEmpty()
        return Response(status, text)
    }
}
