package io.github.joshsymonds.orrery.ui

import java.time.Instant
import java.time.ZoneId

enum class DockKind { SESSION, PANE, SHELL, STEP }

data class DockTool(val name: String, val arg: String, val start: Long)

/** A step's dock facts: [doing] is what the step is doing, which a held step's state line says. */
data class DockStep(val label: String, val held: Boolean, val doing: String = "")

data class DockFacts(
    val kind: DockKind,
    val name: String,
    val state: String,
    val interrupting: Boolean = false,
    val interruptingSince: Long = 0L,
    val turnSince: Long = 0L,
    val tool: DockTool? = null,
    val changed: Long = 0L,
    val reason: String = "",
    val interruptedAt: Long = 0L,
    val putOff: Boolean = false,
    val step: DockStep? = null,
)

enum class DockControl { INTERRUPT, INTERRUPTING, RESUME, NONE }

data class StateLine(
    val tone: String,
    val word: String,
    val tool: String,
    val what: String,
    val clock: String,
    val control: DockControl,
)

/** The state line's words, first matching rule wins. Times are unix seconds; zone formats the time of day. */
fun stateLine(facts: DockFacts, nowSeconds: Long, zone: ZoneId): StateLine {
    val step = facts.step
    val tool = facts.tool
    return when {
        step != null && step.held -> StateLine("held", "held", "", step.doing, "", DockControl.NONE)
        step != null -> StateLine(
            "working",
            "running",
            shortName(tool),
            workingWhat(tool),
            since(facts.turnSince, nowSeconds),
            DockControl.INTERRUPT,
        )
        facts.interrupting && facts.state == "working" -> StateLine(
            "interrupting",
            "interrupting…",
            "",
            "waiting for the turn to end",
            since(facts.interruptingSince, nowSeconds),
            DockControl.INTERRUPTING,
        )
        facts.state == "working" -> StateLine(
            "working",
            "working",
            shortName(tool),
            workingWhat(tool),
            workingClock(facts, nowSeconds),
            DockControl.INTERRUPT,
        )
        facts.state == "waiting" && facts.putOff -> StateLine(
            "idle", "waiting on a question you put off", "", "", "", DockControl.NONE,
        )
        facts.state == "waiting" -> StateLine("waiting", "waiting on you", "", "", "", DockControl.NONE)
        facts.state == "done" && facts.interruptedAt > 0 -> StateLine(
            "interrupted",
            "interrupted by you",
            "",
            joinParts(clockOf(facts.interruptedAt, zone)),
            "",
            DockControl.NONE,
        )
        facts.state == "done" -> StateLine(
            "done", "done", "", joinParts(changedAgo(facts, nowSeconds)), "", DockControl.NONE,
        )
        facts.state == "interrupted" -> StateLine(
            "interrupted",
            "cut off",
            "",
            joinParts(facts.reason, changedAgo(facts, nowSeconds)),
            "",
            DockControl.RESUME,
        )
        facts.state == "failed" -> StateLine(
            "failed",
            "failed",
            "",
            joinParts(facts.reason, changedAgo(facts, nowSeconds)),
            "",
            DockControl.NONE,
        )
        facts.state == "stopped" -> StateLine(
            "stopped", "stopped", "", joinParts(changedAgo(facts, nowSeconds)), "", DockControl.NONE,
        )
        else -> StateLine("idle", "idle", "", "", "", DockControl.NONE)
    }
}

/** The input's placeholder in touch words, first matching state wins. */
fun dockPlaceholder(facts: DockFacts): String {
    val name = facts.name
    val step = facts.step
    return when {
        facts.kind == DockKind.SHELL -> "Type a command…"
        step != null && step.held -> "Nudge ${step.label}. It resumes with your words"
        step != null -> "Nudge ${step.label}. It interrupts the turn, then gives it your words"
        facts.interrupting && facts.state == "working" -> "Stopping $name…"
        facts.state == "working" -> "Queue for $name"
        facts.state == "waiting" -> "Answer $name"
        facts.state == "done" && facts.interruptedAt > 0 -> "$name stopped where it was. Say what to do next"
        facts.state == "interrupted" -> "$name was cut off. Resume it, or say what to do next"
        facts.state == "failed" -> "$name failed. A message starts it again"
        facts.state == "stopped" -> "$name is stopped. A message starts it again"
        else -> "Message $name"
    }
}

/** Elapsed time as m:ss, or h:mm:ss from an hour. Never negative. */
fun elapsedText(seconds: Long): String {
    val total = seconds.coerceAtLeast(0L)
    val hours = total / 3_600
    val minutes = total % 3_600 / 60
    val secs = total % 60
    return if (hours > 0) {
        "%d:%02d:%02d".format(hours, minutes, secs)
    } else {
        "%d:%02d".format(minutes, secs)
    }
}

/** How long ago a change was, as the largest whole unit; under a minute it reads "just now". */
fun agoText(changed: Long, nowSeconds: Long): String {
    val total = (nowSeconds - changed).coerceAtLeast(0L)
    return when {
        total < 60 -> "just now"
        total < 3_600 -> "${total / 60}m ago"
        total < 86_400 -> "${total / 3_600}h ago"
        else -> "${total / 86_400}d ago"
    }
}

/** A tool's short name: the part after its last "__". */
private fun shortName(tool: DockTool?): String = tool?.name?.substringAfterLast("__").orEmpty()

/** The tool's argument, else "thinking". */
private fun workingWhat(tool: DockTool?): String = tool?.arg?.ifEmpty { null } ?: "thinking"

/** The "· " separator and the non-empty parts; no parts give "". */
private fun joinParts(vararg parts: String): String {
    val kept = parts.filter { it.isNotEmpty() }
    return if (kept.isEmpty()) "" else "· " + kept.joinToString(" · ")
}

private fun changedAgo(facts: DockFacts, nowSeconds: Long): String =
    if (facts.changed > 0) agoText(facts.changed, nowSeconds) else ""

/** Elapsed time from a start, or "" when the start is unset. */
private fun since(start: Long, nowSeconds: Long): String =
    if (start > 0) elapsedText(nowSeconds - start) else ""

/** The working clock: from the turn, else the tool's start, else the last change. */
private fun workingClock(facts: DockFacts, nowSeconds: Long): String {
    val toolStart = facts.tool?.start ?: 0L
    return when {
        facts.turnSince > 0 -> elapsedText(nowSeconds - facts.turnSince)
        toolStart > 0 -> elapsedText(nowSeconds - toolStart)
        facts.changed > 0 -> elapsedText(nowSeconds - facts.changed)
        else -> ""
    }
}

private fun clockOf(time: Long, zone: ZoneId): String =
    Instant.ofEpochSecond(time).atZone(zone).format(CLOCK_TIME)
