package io.github.joshsymonds.orrery.api

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.net.InetAddress
import java.net.InetSocketAddress
import java.util.concurrent.ConcurrentHashMap

data class RecordedRequest(
    val method: String,
    val rawPath: String,
    val headers: Map<String, String>,
    val body: String,
)

/**
 * A loopback HTTP server that answers scripted routes and records every request.
 * A route with no script answers 404.
 */
class FakeOrreryServer : AutoCloseable {
    private data class Scripted(val status: Int, val body: String)

    private val server: HttpServer =
        HttpServer.create(InetSocketAddress(InetAddress.getByName("127.0.0.1"), 0), 0)
    private val routes = ConcurrentHashMap<String, Scripted>()
    private val recorded = ArrayList<RecordedRequest>()

    val baseUrl: String = "http://127.0.0.1:${server.address.port}"

    val requests: List<RecordedRequest>
        get() = synchronized(recorded) { recorded.toList() }

    init {
        server.createContext("/") { exchange -> handle(exchange) }
        server.start()
    }

    fun respond(method: String, rawPath: String, status: Int, body: String = "") {
        routes["$method $rawPath"] = Scripted(status, body)
    }

    override fun close() {
        server.stop(0)
    }

    private fun handle(exchange: HttpExchange) {
        val rawPath = exchange.requestURI.rawPath
        val body = exchange.requestBody.use { it.readBytes().toString(Charsets.UTF_8) }
        val headers = exchange.requestHeaders.entries.associate { entry ->
            entry.key.lowercase() to entry.value.joinToString(", ")
        }
        synchronized(recorded) {
            recorded.add(RecordedRequest(exchange.requestMethod, rawPath, headers, body))
        }

        val scripted = routes["${exchange.requestMethod} $rawPath"] ?: Scripted(404, "")
        val bytes = scripted.body.toByteArray(Charsets.UTF_8)
        if (bytes.isEmpty()) {
            exchange.sendResponseHeaders(scripted.status, -1)
            exchange.close()
        } else {
            exchange.sendResponseHeaders(scripted.status, bytes.size.toLong())
            exchange.responseBody.use { it.write(bytes) }
        }
    }
}
