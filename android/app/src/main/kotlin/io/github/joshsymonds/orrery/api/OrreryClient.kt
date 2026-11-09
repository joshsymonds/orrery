package io.github.joshsymonds.orrery.api

import org.json.JSONArray
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
)

sealed interface KeysResult {
    data object Sent : KeysResult
    data object PromptChanged : KeysResult
}

enum class WindowKind(val wire: String) {
    CLAUDE("claude"),
    SHELL("shell"),
}

class OrreryHttpException(val status: Int, message: String) : IOException(message)

/**
 * Blocking client for the orrery phone API. Each call is one request on its
 * own connection; nothing is kept open between calls.
 */
class OrreryClient(baseUrl: String) {
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
            )
        }
    }

    fun screen(paneId: String): String =
        getOk(panePath(paneId, "screen") + "?ansi=1&history=500")

    fun sendText(paneId: String, text: String) {
        post(panePath(paneId, "text"), JSONObject().put("text", text))
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

    fun markSeen(paneId: String) {
        post(panePath(paneId, "seen"), JSONObject())
    }

    fun openWindow(paneId: String, kind: WindowKind) {
        post("/v1/panes", JSONObject().put("pane", paneId).put("kind", kind.wire))
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

    private fun post(path: String, body: JSONObject) {
        val response = send("POST", path, body)
        checkOk("POST", path, response)
    }

    private fun checkOk(method: String, path: String, response: Response) {
        if (response.status !in 200..299) {
            throw OrreryHttpException(response.status, "HTTP ${response.status} from $method $path")
        }
    }

    private fun send(method: String, path: String, body: JSONObject?): Response {
        val connection = URL(root + path).openConnection() as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.connectTimeout = TIMEOUT_MS
            connection.readTimeout = TIMEOUT_MS
            if (body != null) {
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json")
                connection.setRequestProperty("X-Orrery-Client", CLIENT_HEADER)
                connection.outputStream.use { it.write(body.toString().toByteArray(Charsets.UTF_8)) }
            }
            val status = connection.responseCode
            val stream = if (status in 200..299) connection.inputStream else connection.errorStream
            val text = stream?.use { it.readBytes().toString(Charsets.UTF_8) }.orEmpty()
            return Response(status, text)
        } finally {
            connection.disconnect()
        }
    }
}
