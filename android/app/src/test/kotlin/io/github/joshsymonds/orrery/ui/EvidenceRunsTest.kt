package io.github.joshsymonds.orrery.ui

import android.os.Looper
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.font.FontFamily
import com.google.protobuf.ByteString
import io.github.joshsymonds.orrery.api.EvidenceArtifactFile
import io.github.joshsymonds.orrery.api.EvidenceArtifactRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.Epic
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicRequirement
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsGetResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceArtifactResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EvidenceRun
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.Base64
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.atomic.AtomicBoolean

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class EvidenceRunsTest {
    private companion object {
        const val REQUIREMENT = 1
        const val REVISION = "0123456789abcdef0123456789abcdef01234567"
        const val LOG = "example log line one\nexample log line two\n"
        const val EPIC_ID = "example-repo/example-epic"
        const val EPICS_GET = "/orrery.v1.Epics/Get"
        const val EVIDENCE_ARTIFACT = "/orrery.v1.Evidence/Artifact"
        const val TIMEOUT_MS = 5_000L
        const val CHANGED = 4_102_444_800L
        const val IMAGE_TAG = "evidence-image:1:0:0"

        // A 1x1 PNG.
        val PNG: ByteArray = Base64.getDecoder().decode(
            "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=",
        )
    }

    @get:Rule
    val composeRule = createComposeRule()

    /** A loader over [files], keyed by run and name. It records each call and whether one ran on the main thread. */
    private class Loader(private val files: Map<Pair<String, String>, EvidenceArtifactFile>) {
        val calls = ConcurrentLinkedQueue<Pair<String, String>>()
        val onMainThread = AtomicBoolean(false)

        val load: (String, String) -> EvidenceArtifactFile? = { run, name ->
            calls.add(run to name)
            if (Looper.myLooper() == Looper.getMainLooper()) onMainThread.set(true)
            files[run to name]
        }
    }

    private fun run(
        id: String,
        phase: String = "red",
        source: String = "captured",
        state: String = "ended",
        verdict: String = "holds",
        judgeReason: String = "",
        passed: Boolean = true,
        exit: Int = 0,
        logBytes: Long = 0L,
        artifacts: List<EvidenceArtifactRow> = emptyList(),
    ): EvidenceRunRow =
        EvidenceRunRow(
            id = id,
            phase = phase,
            source = source,
            state = state,
            exit = exit,
            timedOut = false,
            notCaptured = "",
            verdict = verdict,
            judgeReason = judgeReason,
            passed = passed,
            revision = REVISION,
            created = CHANGED - 600,
            ended = if (state == "ended") CHANGED - 60 else 0L,
            logBytes = logBytes,
            artifacts = artifacts,
        )

    private fun setRuns(runs: List<EvidenceRunRow>, loader: Loader) {
        composeRule.setContent {
            EvidenceRuns(requirement = REQUIREMENT, runs = runs, loadArtifact = loader.load)
        }
    }

    private fun countAll(matcher: SemanticsMatcher): Int =
        composeRule.onAllNodes(matcher, useUnmergedTree = true).fetchSemanticsNodes().size

    private fun countIn(tag: String, matcher: SemanticsMatcher): Int =
        countAll(matcher and hasAnyAncestor(hasTestTag(tag)))

    private fun awaitAll(matcher: SemanticsMatcher) {
        composeRule.waitUntil(TIMEOUT_MS) { countAll(matcher) > 0 }
    }

    private fun clickTag(tag: String) {
        composeRule.onNodeWithTag(tag, useUnmergedTree = true).performClick()
    }

    private fun textLayoutOf(matcher: SemanticsMatcher): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        val node = composeRule.onNode(matcher, useUnmergedTree = true).fetchSemanticsNode()
        node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action?.invoke(layouts)
        return layouts.single()
    }

    private fun setScreen(server: FakeOrreryServer) {
        composeRule.setContent {
            EpicScreen(
                client = OrreryClient(server.baseUrl),
                epicId = EPIC_ID,
                onBack = {},
                onOpenStep = {},
            )
        }
    }

    private fun epicBody(vararg requirements: EpicRequirement): EpicsGetResponse =
        EpicsGetResponse.newBuilder()
            .setEpic(
                Epic.newBuilder()
                    .setId(EPIC_ID)
                    .setRepository("example-repo")
                    .setSlug("example-epic")
                    .setOutcome("in progress")
                    .setChanged(CHANGED)
                    .setLive(true),
            )
            .addAllRequirements(requirements.toList())
            .build()

    @Test
    fun runsRenderTheirPhaseVerdictPassedStateAndAgentSuppliedMark() {
        val runs = listOf(
            run("example-run-1", phase = "red", verdict = "fails", passed = true, exit = 3),
            run(
                "example-run-2",
                phase = "green",
                source = "supplied",
                state = "judging",
                verdict = "",
                judgeReason = "example judge reason",
                passed = false,
            ),
        )
        setRuns(runs, Loader(emptyMap()))

        val first = "evidence-run:$REQUIREMENT:0"
        assertEquals(1, countIn(first, hasText("red")))
        assertEquals(1, countIn(first, hasText("fails")))
        assertEquals(1, countIn(first, hasText("passed")))
        assertEquals(1, countIn(first, hasText("exit 3")))
        assertEquals(1, countIn(first, hasText("revision 0123456789ab")))
        assertEquals(0, countIn(first, hasText("agent-supplied")))
        assertEquals(0, countIn(first, hasText("running")))

        val second = "evidence-run:$REQUIREMENT:1"
        assertEquals(1, countIn(second, hasText("green")))
        assertEquals(1, countIn(second, hasText("agent-supplied")))
        assertEquals(1, countIn(second, hasText("no verdict")))
        assertEquals(1, countIn(second, hasText("example judge reason")))
        assertEquals(1, countIn(second, hasText("not passed")))
        assertEquals(0, countIn(second, hasText("passed")))
        assertEquals(1, countIn(second, hasText("running")))
    }

    @Test
    fun tappingLogShowsTheLogInMonospaceLoadedOffTheMainThread() {
        val loader = Loader(
            mapOf(("example-run-1" to "") to EvidenceArtifactFile(LOG.toByteArray(Charsets.UTF_8), "text/plain")),
        )
        setRuns(listOf(run("example-run-1", logBytes = LOG.length.toLong())), loader)

        clickTag("evidence-log:$REQUIREMENT:0")

        awaitAll(hasText("example log line two", substring = true))
        assertEquals(listOf("example-run-1" to ""), loader.calls.toList())
        assertFalse(loader.onMainThread.get())
        assertEquals(FontFamily.Monospace, textLayoutOf(hasText("example log line two", substring = true)).layoutInput.style.fontFamily)
    }

    @Test
    fun aKeptImageArtifactRendersAnImageNode() {
        val artifact = EvidenceArtifactRow(
            path = "example/screenshot.png",
            state = "kept",
            size = PNG.size.toLong(),
            mediaType = "image/png",
        )
        val loader = Loader(
            mapOf(("example-run-1" to "example/screenshot.png") to EvidenceArtifactFile(PNG, "image/png")),
        )
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), loader)

        awaitAll(hasTestTag(IMAGE_TAG))

        assertEquals(1, countAll(hasTestTag(IMAGE_TAG)))
        assertEquals(listOf("example-run-1" to "example/screenshot.png"), loader.calls.toList())
        assertTrue(composeRule.onNodeWithTag(IMAGE_TAG, useUnmergedTree = true).fetchSemanticsNode().size.width > 0)
    }

    @Test
    fun aMissingArtifactSaysMissing() {
        val artifact = EvidenceArtifactRow(
            path = "example/screenshot.png",
            state = "missing",
            size = 0L,
            mediaType = "image/png",
        )
        val loader = Loader(emptyMap())
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), loader)

        awaitAll(hasText("missing"))

        val row = "evidence-run:$REQUIREMENT:0"
        assertEquals(1, countIn(row, hasText("missing")))
        assertEquals(1, countIn(row, hasText("example/screenshot.png")))
        assertEquals(0, countAll(hasTestTag(IMAGE_TAG)))
        assertEquals(0, loader.calls.size)
    }

    @Test
    fun anOverCapArtifactSaysOverCap() {
        val artifact = EvidenceArtifactRow(
            path = "example/report.json",
            state = "over_cap",
            size = 9_000_000L,
            mediaType = "application/json",
        )
        val loader = Loader(emptyMap())
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), loader)

        awaitAll(hasText("over cap"))

        assertEquals(1, countIn("evidence-run:$REQUIREMENT:0", hasText("over cap")))
        assertEquals(0, loader.calls.size)
    }

    @Test
    fun aKeptImageTheServerNoLongerHoldsSaysMissing() {
        val artifact = EvidenceArtifactRow(
            path = "example/screenshot.png",
            state = "kept",
            size = PNG.size.toLong(),
            mediaType = "image/png",
        )
        setRuns(listOf(run("example-run-1", artifacts = listOf(artifact))), Loader(emptyMap()))

        awaitAll(hasText("missing"))

        assertEquals(1, countIn("evidence-run:$REQUIREMENT:0", hasText("missing")))
        assertEquals(0, countAll(hasTestTag(IMAGE_TAG)))
    }

    @Test
    fun aLegacyRequirementShowsNoRuns() {
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                epicBody(
                    EpicRequirement.newBuilder()
                        .setId("R1")
                        .setText("Example legacy requirement.")
                        .setEvidence("example check")
                        .setKind("check")
                        .build(),
                ),
            )
            setScreen(server)
            awaitAll(hasText("Example legacy requirement."))

            assertEquals(0, countAll(hasTestTag("evidence-run:0:0")))
            assertEquals(0, countAll(hasText("log")))
        }
    }

    @Test
    fun epicScreenShowsEachRequirementsConclusionAndRunsAndFetchesTheLogOverItsClient() {
        val redRun = EvidenceRun.newBuilder()
            .setId("example-run-1")
            .setPhase("red")
            .setSource("captured")
            .setState("ended")
            .setVerdict("fails")
            .setPassed(true)
            .setRevision(REVISION)
            .setCreated(CHANGED - 600)
            .setEnded(CHANGED - 60)
            .setLogBytes(LOG.length.toLong())
            .build()
        FakeOrreryServer().use { server ->
            server.respond(
                EPICS_GET,
                epicBody(
                    EpicRequirement.newBuilder()
                        .setId("R1")
                        .setText("Example requirement.")
                        .setEvidence("example check")
                        .setKind("check")
                        .setConclusion("example conclusion")
                        .addRuns(redRun)
                        .build(),
                ),
            )
            server.respond(
                EVIDENCE_ARTIFACT,
                EvidenceArtifactResponse.newBuilder()
                    .setData(ByteString.copyFromUtf8(LOG))
                    .setMediaType("text/plain")
                    .build(),
            )
            setScreen(server)
            awaitAll(hasText("example conclusion"))

            assertEquals(1, countIn("evidence-run:0:0", hasText("fails")))
            clickTag("evidence-log:0:0")
            awaitAll(hasText("example log line two", substring = true))

            val request = server.requests.last { it.rawPath == EVIDENCE_ARTIFACT }
            val sent = EvidenceArtifactRequest.parseFrom(request.body)
            assertEquals("example-run-1", sent.run)
            assertEquals("", sent.name)
        }
    }
}
