package io.github.joshsymonds.orrery.api

import com.connectrpc.Code
import com.google.protobuf.MessageLite
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import org.json.JSONObject
import java.io.IOException
import java.net.InetAddress
import java.net.InetSocketAddress
import java.util.concurrent.ConcurrentHashMap

data class RecordedRequest(
    val method: String,
    val rawPath: String,
    val headers: Map<String, String>,
    val body: ByteArray,
)

/** What the fake answers one Connect request with. */
sealed interface Reply {
    /** A successful answer: [message] as `application/proto`, sent after [delayMillis]. */
    data class Message(val message: MessageLite, val delayMillis: Long) : Reply

    /** A Connect error: [code] and [message] as `application/json`, sent after [delayMillis]. */
    data class Failure(val code: Code, val message: String, val delayMillis: Long) : Reply

    /** A 200 answer whose body is [body] verbatim, for a body that is not the procedure's message. */
    class Raw(val body: ByteArray) : Reply

    /** The request is read, then the connection closes with no answer. */
    data object Drop : Reply
}

/**
 * A loopback server that speaks Connect unary over HTTP/1.1 and records every request. A
 * procedure with no script answers `not_found`, as an unscripted route answered 404 before the port.
 */
class FakeOrreryServer : AutoCloseable {
    private val server: HttpServer =
        HttpServer.create(InetSocketAddress(InetAddress.getByName("127.0.0.1"), 0), 0)
    private val routes = ConcurrentHashMap<String, (RecordedRequest) -> Reply>()
    private val recorded = ArrayList<RecordedRequest>()

    val baseUrl: String = "http://127.0.0.1:${server.address.port}"

    val requests: List<RecordedRequest>
        get() = synchronized(recorded) { recorded.toList() }

    init {
        server.createContext("/") { exchange -> handle(exchange) }
        server.start()
    }

    /** Answers [procedure], a path such as `/orrery.v1.Panes/List`, with [message]. */
    fun respond(procedure: String, message: MessageLite, delayMillis: Long = 0L) {
        route(procedure) { Reply.Message(message, delayMillis) }
    }

    /** Answers [procedure] with the Connect error [code] and [message]. */
    fun fail(procedure: String, code: Code, message: String = "", delayMillis: Long = 0L) {
        route(procedure) { Reply.Failure(code, message, delayMillis) }
    }

    /** Reads each request to [procedure], then closes its connection with no answer. */
    fun drop(procedure: String) {
        route(procedure) { Reply.Drop }
    }

    /** Scripts [procedure] with [script], which picks the reply for each request it receives. */
    fun route(procedure: String, script: (RecordedRequest) -> Reply) {
        routes[procedure] = script
    }

    override fun close() {
        server.stop(0)
    }

    private fun handle(exchange: HttpExchange) {
        val rawPath = exchange.requestURI.rawPath
        val body = exchange.requestBody.use { it.readBytes() }
        val headers = exchange.requestHeaders.entries.associate { entry ->
            entry.key.lowercase() to entry.value.joinToString(", ")
        }
        val request = RecordedRequest(exchange.requestMethod, rawPath, headers, body)
        synchronized(recorded) { recorded.add(request) }

        val reply = routes[rawPath]?.invoke(request) ?: Reply.Failure(Code.NOT_FOUND, "", 0L)
        when (reply) {
            is Reply.Drop -> throw IOException("dropped without an answer")
            is Reply.Message -> {
                Thread.sleep(reply.delayMillis)
                send(exchange, 200, "application/proto", reply.message.toByteArray())
            }
            is Reply.Raw -> send(exchange, 200, "application/proto", reply.body)
            is Reply.Failure -> {
                Thread.sleep(reply.delayMillis)
                val json = JSONObject().put("code", reply.code.codeName).put("message", reply.message)
                send(exchange, reply.code.httpStatus(), "application/json", json.toString().toByteArray(Charsets.UTF_8))
            }
        }
    }

    private fun send(exchange: HttpExchange, status: Int, contentType: String, bytes: ByteArray) {
        exchange.responseHeaders.add("Content-Type", contentType)
        if (bytes.isEmpty()) {
            exchange.sendResponseHeaders(status, -1)
            exchange.close()
        } else {
            exchange.sendResponseHeaders(status, bytes.size.toLong())
            exchange.responseBody.use { it.write(bytes) }
        }
    }

    /** The HTTP status Connect sends for this code. */
    private fun Code.httpStatus(): Int = when (this) {
        Code.CANCELED -> 499
        Code.UNKNOWN -> 500
        Code.INVALID_ARGUMENT -> 400
        Code.DEADLINE_EXCEEDED -> 504
        Code.NOT_FOUND -> 404
        Code.ALREADY_EXISTS -> 409
        Code.PERMISSION_DENIED -> 403
        Code.RESOURCE_EXHAUSTED -> 429
        Code.FAILED_PRECONDITION -> 400
        Code.ABORTED -> 409
        Code.OUT_OF_RANGE -> 400
        Code.UNIMPLEMENTED -> 404
        Code.INTERNAL_ERROR -> 500
        Code.UNAVAILABLE -> 503
        Code.DATA_LOSS -> 500
        Code.UNAUTHENTICATED -> 401
    }
}
