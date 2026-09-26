package com.vpsmanager.sdui.actionrunner

import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.core.sdui.SduiDataSource
import com.vpsmanager.core.sdui.SduiEnvelope
import com.vpsmanager.core.sdui.SduiScreen
import com.vpsmanager.data.sdui.SduiActionHttpResult
import com.vpsmanager.data.sdui.SduiDataResult
import com.vpsmanager.sdui.registry.SduiFixtures
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * [ActionRunner] is the single mutation path: every branch
 * of `POST /api/mobile/v1/actions/{action_id}` in plan 07-06's
 * `<interfaces>` contract is exercised here against a fake [ActionInvoker],
 * never a real network call — the 422 body is the one exception, read from
 * the real fixture corpus at `contracts/sdui/fixtures`, never inlined.
 */
class ActionRunnerTest {

    @Test
    fun `a 200 with patch updates the matching row and triggers no component refetch`() = runTest {
        val fetcher = CountingFetcher(mapOf("jobs" to SduiDataResult.Success(
            Json.parseToJsonElement("""[{"id":"job-1","status":"queued"}]"""),
        )))
        val screenState = ScreenState(envelopeWith(jobsTable()), fetcher, FailRefetcher())
        screenState.loadAll()
        assertEquals(1, fetcher.callCountFor("jobs"))

        val invoker = FakeInvoker(
            SduiActionHttpResult.Success(
                buildJsonObject { put("patch", buildJsonObject { put("id", "job-1"); put("status", "running") }) },
            ),
        )
        val runner = ActionRunner(invoker, screenState)

        val outcome = runner.run(ActionInvocation(actionId = "scheduler.jobs.run_now"))

        assertEquals(ActionOutcome.Patched, outcome)
        assertEquals("running", (screenState.rowsFor("jobs").single()["status"] as JsonPrimitive).content)
        // No refetch triggered by the patch -- the fetch counter from loadAll() above is unchanged.
        assertEquals(1, fetcher.callCountFor("jobs"))
    }

    @Test
    fun `a 200 with invalidate refetches exactly those component ids and no others`() = runTest {
        val fetcher = CountingFetcher(
            mapOf(
                "t1" to SduiDataResult.Success(Json.parseToJsonElement("[]")),
                "t2" to SduiDataResult.Success(Json.parseToJsonElement("[]")),
                "t3" to SduiDataResult.Success(Json.parseToJsonElement("[]")),
            ),
        )
        val envelope = SduiEnvelope(
            sduiVersion = 1,
            screen = SduiScreen(
                id = "s1",
                title = "S",
                components = listOf(tableNamed("t1"), tableNamed("t2"), tableNamed("t3")),
            ),
        )
        val screenState = ScreenState(envelope, fetcher, FailRefetcher())
        val invoker = FakeInvoker(
            SduiActionHttpResult.Success(
                buildJsonObject {
                    put("invalidate", buildJsonArray { add(JsonPrimitive("t1")); add(JsonPrimitive("t2")) })
                },
            ),
        )
        val runner = ActionRunner(invoker, screenState)

        val outcome = runner.run(ActionInvocation(actionId = "x.refresh"))

        assertEquals(ActionOutcome.Invalidated(listOf("t1", "t2")), outcome)
        assertEquals(1, fetcher.callCountFor("t1"))
        assertEquals(1, fetcher.callCountFor("t2"))
        assertEquals(0, fetcher.callCountFor("t3"))
    }

    @Test
    fun `a 200 with both patch and invalidate, or neither, surfaces Failed instead of guessing`() = runTest {
        val screenState = ScreenState(envelopeWith(jobsTable()), CountingFetcher(emptyMap()), FailRefetcher())

        val bothOutcome = ActionRunner(
            FakeInvoker(
                SduiActionHttpResult.Success(
                    buildJsonObject {
                        put("patch", buildJsonObject { put("id", "job-1") })
                        put("invalidate", buildJsonArray { add(JsonPrimitive("jobs")) })
                    },
                ),
            ),
            screenState,
        ).run(ActionInvocation(actionId = "x"))
        assertTrue(bothOutcome is ActionOutcome.Failed)

        val neitherOutcome = ActionRunner(
            FakeInvoker(SduiActionHttpResult.Success(buildJsonObject { })),
            screenState,
        ).run(ActionInvocation(actionId = "x"))
        assertTrue(neitherOutcome is ActionOutcome.Failed)
    }

    @Test
    fun `a 422 body from the real fixture is parsed into ValidationFailed with both keys present`() = runTest {
        val fixtureBody = SduiFixtures.read("validation-error.json")
        val screenState = ScreenState(envelopeWith(jobsTable()), CountingFetcher(emptyMap()), FailRefetcher())
        val runner = ActionRunner(FakeInvoker(SduiActionHttpResult.ValidationFailed(fixtureBody)), screenState)

        val outcome = runner.run(ActionInvocation(actionId = "scheduler.jobs.create"))

        assertTrue(outcome is ActionOutcome.ValidationFailed)
        val fields = (outcome as ActionOutcome.ValidationFailed).fields
        assertTrue(fields.containsKey("name"))
        assertTrue(fields.containsKey("schedule"))
    }

    @Test
    fun `a 404 unknown_action yields Gone`() = runTest {
        val screenState = ScreenState(envelopeWith(jobsTable()), CountingFetcher(emptyMap()), FailRefetcher())
        val runner = ActionRunner(FakeInvoker(SduiActionHttpResult.NotFound), screenState)

        val outcome = runner.run(ActionInvocation(actionId = "does.not.exist"))

        assertEquals(ActionOutcome.Gone, outcome)
    }

    @Test
    fun `a 403 yields Stale and triggers exactly one full screen refetch`() = runTest {
        val refetcher = CountingRefetcher(envelopeWith(jobsTable()))
        val screenState = ScreenState(envelopeWith(jobsTable()), CountingFetcher(emptyMap()), refetcher)
        val runner = ActionRunner(FakeInvoker(SduiActionHttpResult.Stale), screenState)

        val outcome = runner.run(ActionInvocation(actionId = "scheduler.jobs.run_now"))

        assertEquals(ActionOutcome.Stale, outcome)
        assertEquals(1, refetcher.callCount)
    }

    @Test
    fun `a destructive action without confirmation never reaches the repository`() = runTest {
        val screenState = ScreenState(envelopeWith(jobsTable()), CountingFetcher(emptyMap()), FailRefetcher())
        val invoker = FakeInvoker(SduiActionHttpResult.Success(buildJsonObject { }))
        val runner = ActionRunner(invoker, screenState)

        val outcome = runner.run(ActionInvocation(actionId = "scheduler.jobs.delete", destructive = true))

        assertTrue(outcome is ActionOutcome.Failed)
        assertEquals(0, invoker.callCount)
    }

    @Test
    fun `a destructive action with confirmation reaches the repository`() = runTest {
        val screenState = ScreenState(envelopeWith(jobsTable()), CountingFetcher(emptyMap()), FailRefetcher())
        val invoker = FakeInvoker(SduiActionHttpResult.Success(buildJsonObject { }))
        val runner = ActionRunner(invoker, screenState)

        runner.run(
            ActionInvocation(
                actionId = "scheduler.jobs.delete",
                destructive = true,
                confirmation = Confirmation(confirmed = true),
            ),
        )

        assertEquals(1, invoker.callCount)
        val sentConfirmation = invoker.lastRequestBody?.get("confirmation") as? JsonObject
        assertTrue(sentConfirmation != null)
        assertTrue((sentConfirmation!!["confirmed"] as JsonPrimitive).content == "true")
    }

    private fun jobsTable() = SduiComponent.Table(
        id = "jobs",
        columns = emptyList(),
        rowsSource = SduiDataSource(endpoint = "/api/mobile/v1/scheduler/jobs"),
    )

    private fun tableNamed(id: String) = SduiComponent.Table(
        id = id,
        columns = emptyList(),
        rowsSource = SduiDataSource(endpoint = "/api/mobile/v1/$id"),
    )

    private fun envelopeWith(vararg components: SduiComponent) = SduiEnvelope(
        sduiVersion = 1,
        screen = SduiScreen(id = "s1", title = "S", components = components.toList()),
    )

    private class CountingFetcher(private val results: Map<String, SduiDataResult>) : ComponentDataFetcher {
        private val counts = mutableMapOf<String, Int>()

        override suspend fun fetch(dataSource: SduiDataSource): SduiDataResult {
            val key = dataSource.endpoint.substringAfterLast('/')
            counts[key] = (counts[key] ?: 0) + 1
            return results[key] ?: SduiDataResult.Empty
        }

        fun callCountFor(componentId: String): Int = counts[componentId] ?: 0
    }

    private class FailRefetcher : ScreenRefetcher {
        override suspend fun refetch(): SduiEnvelope = error("refetchScreen should not be called in this test")
    }

    private class CountingRefetcher(private val envelope: SduiEnvelope) : ScreenRefetcher {
        var callCount = 0
            private set

        override suspend fun refetch(): SduiEnvelope {
            callCount += 1
            return envelope
        }
    }

    private class FakeInvoker(private val result: SduiActionHttpResult) : ActionInvoker {
        var callCount = 0
            private set
        var lastRequestBody: JsonObject? = null
            private set

        override suspend fun invoke(actionId: String, requestBody: JsonObject): SduiActionHttpResult {
            callCount += 1
            lastRequestBody = requestBody
            return result
        }
    }
}
