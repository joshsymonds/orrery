package io.github.joshsymonds.orrery.api

import io.github.joshsymonds.orrery.BackgroundWork
import java.io.FilterInputStream
import java.io.FilterOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import javax.net.SocketFactory
import kotlinx.coroutines.Dispatchers
import okhttp3.Call
import okhttp3.EventListener
import okhttp3.Response

/*
 * Whether any real work a test waits on is still in flight. A test's waits end when their condition holds, or fail
 * as soon as nothing can make it hold: no fake-server handler at work, no request without its answer, no answer
 * unread, and no client thread running the app's, connect's, OkHttp's or protobuf's code. None of this reads a
 * clock, so a loaded machine makes a wait slower, never failed, and a wait that can never end fails at once.
 */
object Quiescence {
    private val sockets: MutableSet<CountingSocket> = Collections.newSetFromMap(ConcurrentHashMap())

    /** Calls started and not yet answered with headers, failed or canceled: queued for a thread or connecting. */
    private val pendingCalls: MutableSet<Call> = Collections.newSetFromMap(ConcurrentHashMap())

    /**
     * Opens every OrreryClient connection as a [CountingSocket] and reports its calls to [CallCounter]. Set once per
     * JVM, by [FakeOrreryServer].
     */
    internal fun install() {
        if (socketFactory !is CountingSocketFactory) socketFactory = CountingSocketFactory
        if (eventListenerFactory !is CallCounter) eventListenerFactory = CallCounter
        if (BackgroundWork.start !== countingStart) BackgroundWork.start = countingStart
    }

    /** One BackgroundWork job: its name, the test that asked for it, and the thread running it once one does. */
    private class Work(val name: String, val generation: Int) {
        @Volatile var thread: Thread? = null
    }

    /**
     * BackgroundWork asked for and not yet returned, started or still waiting for its thread. Only the current test's
     * count as in flight: work an earlier test left running (a push registration retrying a server that test already
     * closed) is not this test's, and counting it held every later wait in the JVM until the 20-minute test timeout.
     * [endTest] fails the test that leaves work running instead.
     */
    private val work: MutableSet<Work> = Collections.newSetFromMap(ConcurrentHashMap())

    /** Which test is running; [newTest] moves it on. */
    @Volatile private var generation = 0

    /** A new test begins: work asked for before now is an earlier test's and no longer counts as in flight. */
    internal fun newTest() {
        generation++
    }

    private fun currentWork(): List<Work> = generation.let { now -> work.filter { it.generation == now } }

    /** The current test's BackgroundWork still running. */
    internal fun backgroundWorkRunning(): Int = currentWork().size

    private val countingStart: (String, Runnable) -> Unit = { name, job -> countedStart(name, job) }

    /**
     * Counts [job], called [name], as in flight from now until it returns, then hands it to [launch] to run on a
     * thread. The count starts before any thread exists, so work whose thread has not run yet is still in flight.
     */
    internal fun countedStart(
        name: String,
        job: Runnable,
        launch: (Runnable) -> Unit = {
            // A daemon, so work a test leaked can never keep the test JVM from exiting.
            Thread(it, name).apply { isDaemon = true }.start()
        },
    ) {
        val entry = Work(name, generation)
        work += entry
        try {
            launch(
                Runnable {
                    entry.thread = Thread.currentThread()
                    try {
                        job.run()
                    } finally {
                        work -= entry
                    }
                },
            )
        } catch (e: Throwable) {
            work -= entry
            throw e
        }
    }

    /**
     * The test ends: wait for its BackgroundWork to finish, and fail, naming it, once it cannot. Work still finishes
     * while its thread has not run yet, runs, or waits on something in flight (an answer, a call); when every job left
     * is parked and nothing else is in flight, only time could end it (a retry's sleep), so the test left it running.
     * Never reads a clock: a loaded machine makes this slower, never failed.
     */
    internal fun endTest() {
        while (true) {
            val left = currentWork()
            if (left.isEmpty()) return
            if (left.any { it.isRunning() } || inFlightBesidesWork() != null) {
                Thread.sleep(PAUSE_MILLIS)
                continue
            }
            if (currentWork().isEmpty()) return
            throw AssertionError(
                "the test ended with BackgroundWork still running and nothing in flight that could finish it: " +
                    left.joinToString("; ") { it.describe() } + "; wait for it before the test ends",
            )
        }
    }

    /** Whether the job is still on its way or at work: not yet on a thread, or on one that is not parked. */
    private fun Work.isRunning(): Boolean {
        val running = thread ?: return true
        return running.state == Thread.State.RUNNABLE || running.state == Thread.State.NEW
    }

    private fun Work.describe(): String {
        val frames = thread?.stackTrace.orEmpty()
        val at = frames.firstOrNull { it.className.startsWith(APP_PACKAGE) } ?: frames.firstOrNull()
        return "\"$name\" (${thread?.state ?: "not started"}${at?.let { ", at $it" }.orEmpty()})"
    }

    /** What is still in flight, in a phrase, or null when nothing is: then only the test's thread can change anything. */
    fun inFlight(): String? {
        install()
        currentWork().takeIf { it.isNotEmpty() }?.let { left ->
            return "BackgroundWork not finished: ${left.joinToString { "\"${it.name}\"" }}"
        }
        return inFlightBesidesWork()
    }

    /** What is in flight other than BackgroundWork itself, or null. */
    private fun inFlightBesidesWork(): String? {
        FakeOrreryServer.busyHandlers().takeIf { it > 0 }?.let { return "$it fake-server handler(s) still answering" }
        pendingCalls.size.takeIf { it > 0 }?.let { return "$it call(s) not yet answered with headers" }
        coroutineTasks()?.let { return it }
        sockets.removeAll { it.isClosed }
        sockets.firstOrNull { it.awaitingAnswer.get() }?.let { return "a request on port ${it.localPort} has no answer yet" }
        sockets.firstOrNull { it.unread() > 0 }?.let { return "an answer on port ${it.localPort} is not read yet" }
        busyClientThread()?.let { (thread, frame) -> return "thread ${thread.name} is still working, at $frame" }
        return null
    }

    /**
     * Waits for [condition], named [description] in the failure, running [step] (idling the main looper, say)
     * before each check. It yields while work is in flight and fails as soon as nothing is in flight and
     * [condition] still does not hold after one more [step]: then nothing can make it hold.
     */
    fun awaitUntil(description: String, step: () -> Unit = {}, condition: () -> Boolean) {
        while (true) {
            step()
            if (condition()) return
            if (inFlight() != null) {
                Thread.sleep(PAUSE_MILLIS)
                continue
            }
            step()
            if (condition()) return
            if (inFlight() != null) continue
            throw AssertionError("$description never held: nothing is in flight; ${serverState()}")
        }
    }

    /**
     * IO work the coroutines have dispatched and not finished, queued or running: kotlinx's scheduler counts each
     * blocking task from dispatch to its end. It has no public view, so this reads its fields, and fails loudly if a
     * kotlinx upgrade renames them rather than letting the waits miss work.
     */
    private fun coroutineTasks(): String? {
        val scheduler = coroutineScheduler
        val state = controlState.invoke(scheduler) as Long
        val blocking = blockingTasks.invoke(scheduler, state) as Int
        if (blocking > 0) return "$blocking IO task(s) dispatched and not finished"
        val queued = queues.sumOf { field -> queueSize.invoke(field.get(scheduler)) as Int }
        return if (queued > 0) "$queued coroutine task(s) queued" else null
    }

    private val coroutineScheduler: Any by lazy {
        val field = Class.forName("kotlinx.coroutines.scheduling.SchedulerCoroutineDispatcher")
            .getDeclaredField("coroutineScheduler").apply { isAccessible = true }
        checkNotNull(field.get(Dispatchers.Default)) { "kotlinx's default scheduler has no CoroutineScheduler" }
    }
    private val schedulerClass = Class.forName("kotlinx.coroutines.scheduling.CoroutineScheduler")
    private val controlState = schedulerClass.getDeclaredMethod("getControlState\$volatile").apply { isAccessible = true }
    private val blockingTasks = schedulerClass.getDeclaredMethod("blockingTasks", Long::class.javaPrimitiveType)
        .apply { isAccessible = true }
    private val queues = listOf("globalCpuQueue", "globalBlockingQueue").map { name ->
        schedulerClass.getDeclaredField(name).apply { isAccessible = true }
    }
    private val queueSize = Class.forName("kotlinx.coroutines.internal.LockFreeTaskQueue").getMethod("getSize")

    /** The live fake servers' requests, by path, for a failure message. */
    fun serverState(): String = FakeOrreryServer.describeLive()

    /**
     * A client thread running work: one outside a blocking socket read with the app's, connect's, OkHttp's,
     * protobuf's or a coroutine task's frames on its stack. A thread parked in a pool or blocked reading a socket
     * waits; what it waits for shows as a busy handler or an awaiting or unread socket above.
     */
    private fun busyClientThread(): Pair<Thread, StackTraceElement>? {
        val self = Thread.currentThread()
        for ((thread, frames) in Thread.getAllStackTraces()) {
            if (thread == self || !thread.isAlive || FakeOrreryServer.isServerThread(thread) || frames.isEmpty()) continue
            if (work.any { it.thread == thread }) continue
            if (frames.any(::isSocketRead)) continue
            val app = frames.any { it.className.startsWith(APP_PACKAGE) }
            val task = frames.any { it.className == DISPATCHED_TASK && it.methodName == "run" }
            val client = frames.any { frame -> CLIENT_PACKAGES.any { frame.className.startsWith(it) } }
            val waiting = thread.state == Thread.State.WAITING || thread.state == Thread.State.TIMED_WAITING
            val busy = if (waiting) app || task else app || task || client
            // Parked or sleeping, only a task or the app's own code paused mid-run is work still to come.
            if (busy) return thread to (frames.firstOrNull(::isWorkFrame) ?: frames.first())
        }
        return null
    }

    private fun isWorkFrame(frame: StackTraceElement): Boolean =
        frame.className.startsWith(APP_PACKAGE) || CLIENT_PACKAGES.any { frame.className.startsWith(it) }

    private fun isSocketRead(frame: StackTraceElement): Boolean =
        (frame.className == "sun.nio.ch.NioSocketImpl" && frame.methodName in SOCKET_READS) ||
            (frame.className == "java.net.SocketInputStream" && frame.methodName in SOCKET_READS) ||
            (frame.className == "java.net.Socket\$SocketInputStream" && frame.methodName == "read")

    private const val PAUSE_MILLIS = 10L
    private const val APP_PACKAGE = "io.github.joshsymonds.orrery."
    private const val DISPATCHED_TASK = "kotlinx.coroutines.DispatchedTask"
    private val CLIENT_PACKAGES = listOf("com.connectrpc.", "okhttp3.", "okio.", "com.google.protobuf.")
    private val SOCKET_READS = setOf("read", "implRead", "timedRead", "socketRead", "socketRead0")

    /** A client socket that knows whether its request is still unanswered and how much of its answer is unread. */
    private class CountingSocket : Socket() {
        val awaitingAnswer = AtomicBoolean(false)

        fun unread(): Int = try {
            if (isClosed || !isConnected) 0 else super.getInputStream().available()
        } catch (e: IOException) {
            0
        }

        override fun getInputStream(): InputStream = object : FilterInputStream(super.getInputStream()) {
            // Any byte, or the end of the stream, means the server has answered or never will.
            override fun read(): Int = super.read().also { awaitingAnswer.set(false) }
            override fun read(b: ByteArray, off: Int, len: Int): Int =
                super.read(b, off, len).also { if (it != 0) awaitingAnswer.set(false) }
        }

        override fun getOutputStream(): OutputStream = object : FilterOutputStream(super.getOutputStream()) {
            override fun write(b: Int) {
                awaitingAnswer.set(true)
                out.write(b)
            }

            override fun write(b: ByteArray, off: Int, len: Int) {
                if (len > 0) awaitingAnswer.set(true)
                out.write(b, off, len)
            }
        }
    }

    /** Counts each call from its start until its answer's headers arrive, it fails, or it is canceled. */
    private object CallCounter : EventListener(), EventListener.Factory {
        override fun create(call: Call): EventListener = this

        override fun callStart(call: Call) {
            pendingCalls += call
        }

        override fun responseHeadersEnd(call: Call, response: Response) {
            pendingCalls -= call
        }

        override fun callFailed(call: Call, ioe: IOException) {
            pendingCalls -= call
        }

        override fun canceled(call: Call) {
            pendingCalls -= call
        }

        override fun callEnd(call: Call) {
            pendingCalls -= call
        }
    }

    private object CountingSocketFactory : SocketFactory() {
        private fun counted(): Socket = CountingSocket().also { sockets += it }

        override fun createSocket(): Socket = counted()
        override fun createSocket(host: String, port: Int): Socket =
            counted().apply { connect(InetSocketAddress(host, port)) }
        override fun createSocket(host: String, port: Int, localHost: InetAddress, localPort: Int): Socket =
            counted().apply {
                bind(InetSocketAddress(localHost, localPort))
                connect(InetSocketAddress(host, port))
            }
        override fun createSocket(host: InetAddress, port: Int): Socket =
            counted().apply { connect(InetSocketAddress(host, port)) }
        override fun createSocket(address: InetAddress, port: Int, localAddress: InetAddress, localPort: Int): Socket =
            counted().apply {
                bind(InetSocketAddress(localAddress, localPort))
                connect(InetSocketAddress(address, port))
            }
    }
}
