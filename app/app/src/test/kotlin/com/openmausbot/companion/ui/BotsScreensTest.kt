package com.openmausbot.companion.ui

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.background
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import java.io.File
import java.util.concurrent.CopyOnWriteArrayList

/** The phone's bot screens against a fake workspace host; also saves screenshots. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], qualifiers = "w393dp-h852dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class BotsScreensTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()
    private lateinit var server: MockWebServer
    private val requests = CopyOnWriteArrayList<Pair<String, String>>()

    private val bots = """{"bots":[
      {"id":"b1","name":"Chief","title":"Lead engineer","color":"#3b82f6","main":true,"glyph":2479,"avatarKind":"generated","avatarShape":"circle","description":"Runs the workspace","settings":{"model":"","permissionMode":"ask","mode":"default"},"threadId":"t1","busy":false,"preview":"Pushed the fix and CI is green.","lastAt":${System.currentTimeMillis() - 120_000},"tasks":[{"threadId":"t1","title":"Fix the failing build","busy":false,"preview":"","updatedAt":${System.currentTimeMillis() - 120_000}},{"threadId":"t9","title":"Landing page copy","updatedAt":${System.currentTimeMillis() - 7_200_000}}]},
      {"id":"b2","name":"Reviewer","title":"Code reviewer","color":"#22a06b","glyph":10540,"avatarKind":"emoji","emoji":"🔍","avatarShape":"rounded","pinned":true,"settings":{},"threadId":"t2","busy":true,"preview":"","lastAt":${System.currentTimeMillis() - 30_000},"tasks":[{"threadId":"t2","title":"Review PR","busy":true}]},
      {"id":"b3","name":"Ops","title":"Deploys and alerts","color":"#f08c3a","glyph":18499,"avatarKind":"image","avatarUrl":"/api/bots/b3/avatar?v=1","avatarShape":"square","settings":{},"threadId":"t3","preview":"All services healthy.","lastAt":${System.currentTimeMillis() - 86_400_000 * 2},"tasks":[]}
    ]}"""
    private val now = System.currentTimeMillis()
    private val messages = """{"hasMore":false,"session":{"threadId":"t1","open":true,"busy":false,"mode":"default","configOptions":[{"id":"model","name":"Model","type":"select","currentValue":"mistral-medium-latest","options":[{"value":"mistral-medium-latest","name":"mistral · mistral-medium-latest"}]},{"id":"permission_mode","name":"Permissions","type":"select","currentValue":"ask","options":[{"value":"ask","name":"Ask"},{"value":"auto","name":"Auto"}]}],"commands":[{"name":"context","description":"Show context usage"},{"name":"compact","description":"Compress history"}],"usage":{"usedTokens":8000,"maxTokens":128000}},"messages":[
      {"id":"m1","role":"user","kind":"text","text":"The build fails on main. Fix it and tell me what changed.","at":${now - 300_000}},
      {"id":"m2","role":"bot","kind":"text","text":"Found it: `parseConfig` returns **undefined** when the file is empty. I'll guard it.","at":${now - 290_000}},
      {"id":"m3","role":"bot","kind":"tool","tool":{"id":"c1","title":"Edit /home/user/workspace/src/config.ts","kind":"edit","status":"completed","diffs":[{"path":"/home/user/workspace/src/config.ts","oldText":"export function parseConfig(s) {\n  return JSON.parse(s)\n}","newText":"export function parseConfig(s) {\n  if (!s.trim()) return {}\n  return JSON.parse(s)\n}"}]},"at":${now - 280_000}},
      {"id":"m4","role":"bot","kind":"tool","tool":{"id":"c2","title":"npm test","kind":"execute","status":"completed","output":"{\"stdout\":\"42 passing\"}"},"at":${now - 270_000}},
      {"id":"m5","role":"bot","kind":"changes","changes":[{"path":"src/config.ts","kind":"modified"}],"at":${now - 260_000}},
      {"id":"m6","role":"bot","kind":"request","request":{"id":"q1","type":"permission","state":"pending","title":"Run git push origin main","options":[{"optionId":"allow-once","name":"Allow once","kind":"allow_once"},{"optionId":"allow-always","name":"Always allow","kind":"allow_always"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]},"at":${now - 250_000}}
    ]}"""
    private val planThread = """{"hasMore":false,"session":{"threadId":"t9","open":true,"busy":false,"mode":"plan"},"messages":[
      {"id":"p1","role":"user","kind":"text","text":"Plan a landing page refresh.","at":${now - 100_000}},
      {"id":"p2","role":"bot","kind":"request","request":{"id":"q2","type":"question","state":"pending","mode":"plan","questions":[{"question":"Which tone should the copy use?","header":"Tone","options":[{"label":"Friendly"},{"label":"Technical"},{"label":"Bold"}],"multiSelect":false}]},"at":${now - 90_000}},
      {"id":"p3","role":"bot","kind":"request","request":{"id":"q3","type":"plan","state":"pending","plan":"1. Rewrite the hero copy\n2. Add a pricing section\n3. Ship behind a flag"},"at":${now - 80_000}},
      {"id":"p4","role":"bot","kind":"notice","error":true,"errorCode":-32000,"action":"providers","text":"The provider rejected the credentials (401). Sign in again or paste a new API key.","at":${now - 70_000}}
    ]}"""

    private val facePng: ByteArray by lazy {
        val bmp = Bitmap.createBitmap(48, 48, Bitmap.Config.ARGB_8888).apply { eraseColor(android.graphics.Color.rgb(240, 140, 58)) }
        java.io.ByteArrayOutputStream().use { bmp.compress(Bitmap.CompressFormat.PNG, 100, it); it.toByteArray() }
    }

    @Before fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.path.orEmpty()
                requests += path to request.body.readUtf8()
                if (path.startsWith("/api/bots/b3/avatar")) return MockResponse().setHeader("content-type", "image/png").setBody(okio.Buffer().write(facePng))
                return MockResponse().setBody(when {
                    path.startsWith("/api/bots") && request.method == "GET" -> bots
                    path.startsWith("/api/threads/t1/messages") -> messages
                    path.startsWith("/api/threads/t9/messages") -> planThread
                    path.startsWith("/api/threads/t1/open") -> JSONObject(messages).getJSONObject("session").toString()
                    path.startsWith("/api/threads/t9/open") -> JSONObject(planThread).getJSONObject("session").toString()
                    path.startsWith("/api/routines") -> """{"routines":[{"id":"r1","name":"Morning check","prompt":"Summarize overnight alerts","botId":"b1","enabled":true,"schedule":{"type":"daily","time":"09:00"},"nextRunAt":${now + 3_600_000}}]}"""
                    path.startsWith("/api/bots/b1/files") -> """{"files":[{"name":"brief.md","size":2048}]}"""
                    path.startsWith("/api/providers") -> """{"configured":true,"providerId":"mistral","modelId":"mistral-medium-latest","hasApiKey":true,"oauth":{},"providers":[{"id":"mistral-auth","name":"Mistral Auth","description":"Sign in with your Mistral account","authMethods":["browser"]},{"id":"nous","name":"Nous Research","description":"Device sign-in","authMethods":["device"]},{"id":"openai","name":"OpenAI","authMethods":["api_key"]},{"id":"anthropic","name":"Anthropic","authMethods":["api_key"]},{"id":"mistral","name":"Mistral","authMethods":["api_key"],"baseUrl":"https://api.mistral.ai/v1"},{"id":"ollama","name":"Ollama","description":"Local models","authMethods":["none","api_key"]}]}"""
                    else -> "{}"
                })
            }
        }
        server.start()
    }
    @After fun stop() { server.shutdown() }

    private fun state(): BotsState = BotsState(BotsClient().apply { host = server.url("/").toString(); session = "s" }).also { st ->
        st.replaceBots(st.client.bots()); st.loading = false; st.live = true
    }

    private fun shot(name: String) {
        compose.waitForIdle()
        compose.runOnIdle {
            val view = compose.activity.window.decorView
            val bitmap = Bitmap.createBitmap(view.width, view.height, Bitmap.Config.ARGB_8888)
            view.draw(android.graphics.Canvas(bitmap))
            val dir = File(System.getenv("VB_SHOTS") ?: "build/outputs/vectra-screens").apply { mkdirs() }
            File(dir, "ui-v2-android-$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }

    @Test fun botListShowsEveryBotWithPreviewAndBusyState() {
        val st = state()
        var opened = ""
        compose.setContent { VectraTheme { BotHome(st, "Vectrabot demo", "me@example.com", onOpen = { opened = it.id }, onNewBot = {}, onProviders = {}, onRoutines = {}, onWorkspaces = {}, onSignOut = {}) } }
        compose.onNodeWithText("Chief").assertIsDisplayed()
        compose.onNodeWithText("Working…").assertIsDisplayed()
        compose.onNodeWithText("All services healthy.").assertIsDisplayed()
        shot("1-bots")
        compose.onNodeWithTag("bot-Reviewer").performClick()
        assertEquals("b2", opened)
    }

    @Test fun mainBotFirstAndEveryBotShowsItsOwnFace() {
        val st = state()
        compose.setContent { VectraTheme { androidx.compose.runtime.CompositionLocalProvider(LocalBotFaces provides BotFaces(st.client)) { BotHome(st, "Vectrabot demo", "me@example.com", onOpen = {}, onNewBot = {}, onProviders = {}, onRoutines = {}, onWorkspaces = {}, onSignOut = {}) } } }
        compose.onNodeWithTag("main-Chief", useUnmergedTree = true).assertIsDisplayed()
        compose.onNodeWithText("🔍").assertIsDisplayed()
        compose.waitUntil(5_000) { requests.any { it.first.startsWith("/api/bots/b3/avatar") } }
        for (n in listOf("Chief", "Reviewer", "Ops")) compose.onNodeWithTag("face-$n", useUnmergedTree = true).assertIsDisplayed()
        shot("8-bot-faces")
        val order = sortBots(st.bots).map { it.name }
        assertEquals(listOf("Chief", "Reviewer", "Ops"), order)
    }

    @Test fun editorPicksAnIconShapeAndColour() {
        val st = state()
        val ed = BotEditorState(st.bots.first { it.id == "b3" }, emptyList())
        compose.setContent { VectraTheme { Box(Modifier.fillMaxSize().background(Vx.raised)) { androidx.compose.foundation.layout.Column(Modifier.verticalScroll(androidx.compose.foundation.rememberScrollState())) { BotEditorForm(ed, onDelete = {}, onDuplicate = {}) } } } }
        compose.onNodeWithText("Delete bot").assertIsDisplayed()
        compose.onNodeWithTag("kind-emoji").performClick()
        compose.onNodeWithTag("emoji-🚀").performClick()
        compose.onNodeWithTag("shape-rounded").performClick()
        compose.onNodeWithTag("swatch-#8b5cf6").performClick()
        compose.onNodeWithTag("bot-description").performTextInput("Ships releases")
        shot("9-bot-editor")
        val d = compose.runOnIdle { ed.draft() }!!
        assertEquals("emoji", d.avatarKind); assertEquals("🚀", d.emoji); assertEquals("rounded", d.avatarShape); assertEquals("#8b5cf6", d.color)
        assertTrue(d.description.startsWith("Ships releases"))
    }

    @Test fun mainBotEditorHasNoDelete() {
        val st = state()
        compose.setContent { VectraTheme { BotEditorForm(BotEditorState(st.bots.first { it.main }, emptyList()), onDelete = {}, onDuplicate = {}) } }
        compose.onNodeWithText("Main bot").assertIsDisplayed()
        assertTrue(compose.onAllNodesWithText("Delete bot").fetchSemanticsNodes().isEmpty())
    }

    @Test fun chatRendersToolsDiffsAndAnswersAPermission() {
        val st = state(); st.select(st.bots[0])
        compose.setContent { VectraTheme { Box(Modifier.fillMaxSize().background(Vx.bg)) { BotChatPane(st, onProviders = {}, onNotice = {}) } } }
        compose.waitUntil(10_000) { st.thread().isNotEmpty() }
        compose.onNodeWithText("Permission needed").assertIsDisplayed()
        shot("2-chat-tools")
        compose.onNodeWithText("Allow once").performClick()
        compose.waitUntil(5_000) { requests.any { it.first == "/api/threads/t1/respond" } }
        val body = JSONObject(requests.first { it.first == "/api/threads/t1/respond" }.second)
        assertEquals("q1", body.getString("requestId")); assertEquals("allow-once", body.getJSONObject("answer").getString("optionId"))
    }

    @Test fun slashPaletteComesFromTheAgentAndSendPosts() {
        val st = state(); st.select(st.bots[0])
        compose.setContent { VectraTheme { Box(Modifier.fillMaxSize().background(Vx.bg)) { BotChatPane(st, onProviders = {}, onNotice = {}) } } }
        compose.waitUntil(10_000) { st.session.commands.isNotEmpty() }
        compose.onNodeWithTag("chat-input").performTextInput("/con")
        compose.onNodeWithTag("slash-palette").assertIsDisplayed()
        compose.onNodeWithText("/context").assertIsDisplayed()
        shot("3-slash")
        compose.onNodeWithText("/context").performClick()
        compose.onNodeWithTag("send").performClick()
        compose.waitUntil(5_000) { requests.any { it.first == "/api/bots/b1/messages" } }
        assertEquals("/context", JSONObject(requests.first { it.first == "/api/bots/b1/messages" }.second).getString("text"))
    }

    @Test fun planModeQuestionPlanAndErrorStates() {
        val st = state(); st.select(st.bots[0], "t9")
        var providers = 0
        compose.setContent { VectraTheme { Box(Modifier.fillMaxSize().background(Vx.bg)) { BotChatPane(st, onProviders = { providers++ }, onNotice = {}) } } }
        compose.waitUntil(10_000) { st.thread().isNotEmpty() }
        compose.onNodeWithText("Chat about this").assertIsDisplayed()
        compose.onNodeWithText("Approve and run").assertIsDisplayed()
        shot("4-plan-question")
        compose.onNodeWithText("Technical").performClick()
        compose.onNodeWithText("Submit").performClick()
        compose.waitUntil(5_000) { requests.any { it.first == "/api/threads/t9/respond" } }
        val answer = JSONObject(requests.first { it.first == "/api/threads/t9/respond" }.second).getJSONObject("answer")
        assertEquals("accepted", answer.getString("outcome"))
        assertEquals("Technical", answer.getJSONObject("answers").getJSONArray("Which tone should the copy use?").getString(0))
        compose.onNodeWithText("Providers").performClick()
        assertEquals(1, providers)
        compose.onNodeWithTag("error-notice").assertIsDisplayed()
    }

    @Test fun infoPanelShowsThreadsRoutinesAndSettings() {
        val st = state(); st.select(st.bots[0])
        compose.setContent { VectraTheme { BotInfoPanel(st, onBack = {}, onEdit = {}, onThread = {}, onNewThread = {}, onRoutines = {}, onProviders = {}, onTab = {}, onNotice = {}) } }
        compose.waitUntil(5_000) { compose.onAllNodesWithText("Morning check").fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("Fix the failing build").assertIsDisplayed()
        compose.onNodeWithText("Morning check").assertIsDisplayed()
        shot("5-bot-info")
    }

    @Test fun providersListGroupsSignInKeysAndLocal() {
        val st = state()
        compose.setContent { VectraTheme { ProvidersPanel(st.client, onBack = {}) } }
        compose.waitUntil(5_000) { compose.onAllNodesWithText("SIGN IN").fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("SIGN IN").assertIsDisplayed()
        compose.onNodeWithText("LOCAL").assertIsDisplayed()
        shot("6-providers")
        compose.onNodeWithTag("provider-openai").performClick()
        compose.onNodeWithTag("provider-key").assertIsDisplayed()
        shot("7-provider-key")
    }
}
