package com.openmausbot.companion.ui

import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class BotsClientTest {
    private lateinit var server: MockWebServer
    private lateinit var client: BotsClient

    @Before fun start() {
        server = MockWebServer().also { it.start() }
        client = BotsClient().apply { host = server.url("/").toString(); session = "dev-session" }
    }
    @After fun stop() { server.shutdown() }

    @Test fun listsBotsWithThreadsFromTheHost() {
        server.enqueue(MockResponse().setBody("""{"bots":[{"id":"b1","name":"Chief","title":"Lead","color":"#3b82f6","settings":{"model":"","permissionMode":"ask","mode":"plan"},"threadId":"t1","busy":true,"preview":"hi","lastAt":5,"tasks":[{"threadId":"t1","title":"Fix CI","busy":true,"preview":"hi","updatedAt":5,"pinned":false}]}]}"""))
        val bots = client.bots()
        assertEquals(1, bots.size)
        val b = bots[0]
        assertEquals("Chief", b.name); assertEquals("plan", b.settings.mode); assertTrue(b.busy)
        assertEquals("Fix CI", b.threads[0].title)
        val req = server.takeRequest()
        assertEquals("/api/bots", req.path)
        assertEquals("Bearer dev-session", req.getHeader("authorization"))
    }

    @Test fun sendPostsTheThreadAndReportsQueueing() {
        server.enqueue(MockResponse().setBody("""{"threadId":"t1","queued":true}"""))
        assertTrue(client.send("b 1", "t1", "hello"))
        val req = server.takeRequest()
        assertEquals("/api/bots/b%201/messages", req.path)
        val body = JSONObject(req.body.readUtf8())
        assertEquals("t1", body.getString("threadId")); assertEquals("hello", body.getString("text"))
    }

    @Test fun reattachesOnceOn401() {
        server.enqueue(MockResponse().setResponseCode(401).setBody("""{"error":"session expired"}"""))
        server.enqueue(MockResponse().setBody("""{"bots":[]}"""))
        var reattached = 0
        client.reattach = { reattached++; client.session = "fresh"; true }
        client.bots()
        assertEquals(1, reattached)
        server.takeRequest()
        assertEquals("Bearer fresh", server.takeRequest().getHeader("authorization"))
    }

    @Test fun hostErrorsCarryTheirMessage() {
        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"keep at least one bot"}"""))
        val e = runCatching { client.deleteBot("b1") }.exceptionOrNull()
        assertTrue(e is HostError); assertEquals("keep at least one bot", e!!.message); assertEquals(409, (e as HostError).status)
    }

    @Test fun eventsStreamParsesFramesAndKeepsTheCursor() {
        server.enqueue(MockResponse().setHeader("content-type", "text/event-stream").setBody(
            "data: {\"kind\":\"hello\",\"cursor\":\"e1.0\",\"resumed\":false}\n\n" +
                "id: e1.1\ndata: {\"kind\":\"message\",\"botId\":\"b1\",\"threadId\":\"t1\",\"message\":{\"id\":\"m1\",\"role\":\"bot\",\"kind\":\"text\",\"text\":\"hi\"}}\n\n" +
                ": ping\n\n" +
                "id: e1.2\ndata: {\"kind\":\"turn\",\"botId\":\"b1\",\"threadId\":\"t1\",\"busy\":false}\n\n",
        ))
        val frames = mutableListOf<HostFrame>()
        val cursor = client.events("e0.5", stop = { false }) { frames += it }
        assertEquals(listOf("hello", "message", "turn"), frames.map { it.kind })
        assertEquals("e1.2", cursor)
        val req = server.takeRequest()
        assertTrue(req.path!!.startsWith("/api/events?token=dev-session&since=e0.5"))
    }

    @Test fun providersStatusAndLoginShapes() {
        server.enqueue(MockResponse().setBody("""{"configured":true,"providerId":"custom","baseUrl":"https://api.example/v1","modelId":"m","hasApiKey":true,"oauth":{"nous":{"signedIn":true}},"providers":[{"id":"nous","name":"Nous","authMethods":["device"]},{"id":"openai","name":"OpenAI","authMethods":["api_key"]},{"id":"ollama","name":"Ollama","authMethods":["none","api_key"]}]}"""))
        val s = client.providers()
        assertEquals(3, s.providers.size); assertTrue(s.providers[0].signedIn); assertEquals(listOf("none", "api_key"), s.providers[2].auth)
        server.takeRequest()
        server.enqueue(MockResponse().setBody("""{"loginId":"L1","url":"https://login","interval":4,"kind":"browser"}"""))
        val l = client.startLogin("mistral-auth", "browser")
        assertEquals("L1", l.loginId); assertEquals(4, l.interval)
        assertEquals("mistral-auth", JSONObject(server.takeRequest().body.readUtf8()).getString("providerId"))
        server.enqueue(MockResponse().setBody("""{"status":"done"}"""))
        assertEquals("done", client.pollLogin("L1").first)
        assertEquals("""{"loginId":"L1"}""", server.takeRequest().body.readUtf8())
    }

    @Test fun saveNeverSendsABlankKey() {
        server.enqueue(MockResponse().setBody("{}"))
        client.saveProvider("openai", "gpt", apiKey = "", baseUrl = null)
        val body = JSONObject(server.takeRequest().body.readUtf8())
        assertFalse(body.has("apiKey")); assertEquals("gpt", body.getString("modelId"))
    }

    @Test fun parsesToolRequestAndErrorMessages() {
        val tool = parseMessage(JSONObject("""{"id":"m","role":"bot","kind":"tool","tool":{"id":"c","title":"Write /home/user/workspace/a.txt","kind":"edit","status":"completed","diffs":[{"path":"/home/user/workspace/a.txt","oldText":"","newText":"hi"}]}}"""))
        assertEquals("edit", tool.tool!!.kind); assertEquals(1, tool.tool!!.diffs.size)
        val req = parseMessage(JSONObject("""{"id":"r","role":"bot","kind":"request","request":{"id":"q","type":"question","state":"pending","mode":"plan","questions":[{"question":"Color?","options":[{"label":"Red"},{"label":"Blue"}],"multiSelect":false}]}}"""))
        assertEquals("plan", req.request!!.mode); assertEquals("Blue", req.request!!.questions[0].options[1].label)
        val err = parseMessage(JSONObject("""{"id":"e","role":"bot","kind":"notice","error":true,"errorCode":-32000,"action":"providers","text":"rejected"}"""))
        assertEquals(-32000, err.errorCode); assertEquals("Providers", errorActionLabel(err.action))
    }

    @Test fun answersMatchTheHostShapes() {
        assertEquals("""{"optionId":"allow-once"}""", Answers.permission("allow-once").toString())
        assertEquals("approved", Answers.planApproved().getString("outcome"))
        val revise = Answers.planRevise("smaller")
        assertEquals("cancelled", revise.getString("outcome")); assertEquals("smaller", revise.getString("feedback"))
        assertEquals("abandoned", Answers.planRejected().getString("outcome"))
        val q = Answers.questions("accepted", mapOf("Color?" to listOf("Blue"), "Empty?" to emptyList()))
        assertEquals("Blue", q.getJSONObject("answers").getJSONArray("Color?").getString(0))
        assertFalse(q.getJSONObject("answers").has("Empty?"))
    }

    @Test fun sessionParsesModesOptionsCommandsAndUsage() {
        val s = parseSession(JSONObject("""{"threadId":"t","open":true,"busy":false,"queued":2,"mode":"plan","modes":{"availableModes":[{"id":"default","name":"Default"},{"id":"plan","name":"Plan"}]},"configOptions":[{"id":"model","name":"Model","type":"select","currentValue":"a","options":[{"value":"a","name":"A"}]},{"id":"x","type":"boolean"}],"commands":[{"name":"context","description":"Show context"}],"usage":{"usedTokens":50,"maxTokens":1000}}"""))
        assertEquals(2, s.modes.size); assertEquals(1, s.options.size); assertEquals("context", s.commands[0].name)
        assertEquals(2, s.queued); assertEquals(1000, s.maxTokens)
    }

    @Test fun lineDiffAndSummaries() {
        assertEquals(listOf(' ' to "a", '-' to "b", '+' to "c"), lineDiff("a\nb", "a\nc"))
        assertEquals("a.txt", shortPath("/home/user/workspace/a.txt"))
        assertEquals("plan.md", shortPath("/home/user/.supercharge/sessions/x/plan.md"))
        assertEquals("The file a.txt was created.", toolSummary("""{"tool_output_for_prompt_concise":"The file /home/user/workspace/a.txt was created."}"""))
        assertEquals("plain", toolSummary("plain"))
    }

    @Test fun stateAppliesFrames() {
        val st = BotsState(client)
        st.replaceBots(listOf(parseBot(JSONObject("""{"id":"b1","name":"Chief","threadId":"t1","tasks":[{"threadId":"t1","title":"x"}]}"""))))
        st.select(st.bots[0])
        st.loaded("t1", BotsClient.Page(emptyList(), false, SessionInfo.Empty))
        assertTrue(st.apply(HostFrame("hello", JSONObject("""{"kind":"hello","resumed":false}"""))))
        st.apply(HostFrame("message", JSONObject("""{"botId":"b1","threadId":"t1","message":{"id":"m1","role":"bot","kind":"text","text":"one","at":9}}""")))
        st.apply(HostFrame("message", JSONObject("""{"botId":"b1","threadId":"t1","message":{"id":"m1","role":"bot","kind":"text","text":"one two","at":9}}""")))
        assertEquals(1, st.thread().size); assertEquals("one two", st.thread()[0].text)
        assertEquals("one two", st.bots[0].preview)
        st.apply(HostFrame("turn", JSONObject("""{"botId":"b1","threadId":"t1","busy":true}""")))
        assertTrue(st.session.busy); assertTrue(st.bots[0].busy)
        st.apply(HostFrame("thread", JSONObject("""{"botId":"b1","threadId":"t2","thread":{"threadId":"t2","title":"Second","updatedAt":10}}""")))
        assertEquals("Second", st.bots[0].threads[0].title)
        st.apply(HostFrame("bot.deleted", JSONObject("""{"botId":"b1"}""")))
        assertNotNull(st.bots); assertTrue(st.bots.isEmpty())
    }
}
