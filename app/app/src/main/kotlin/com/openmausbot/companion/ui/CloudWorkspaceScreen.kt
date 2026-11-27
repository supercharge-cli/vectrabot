package com.openmausbot.companion.ui

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.ScrollableTabRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Info
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import java.util.concurrent.TimeUnit

private const val CLOUD_BASE = "https://infra.xibeai.in"
private val jsonType = "application/json; charset=utf-8".toMediaType()

private class CloudApi {
    private val http = OkHttpClient.Builder().callTimeout(60, TimeUnit.SECONDS).build()

    fun post(path: String, token: String?, body: String): JSONObject {
        val call = Request.Builder().url(CLOUD_BASE + path).post(body.toRequestBody(jsonType))
        if (!token.isNullOrBlank()) call.header("authorization", "Bearer $token")
        return read(call.build())
    }

    fun get(path: String, token: String): JSONObject =
        read(Request.Builder().url(CLOUD_BASE + path).header("authorization", "Bearer $token").get().build())

    private fun read(request: Request): JSONObject {
        http.newCall(request).execute().use { response ->
            val text = response.body?.string().orEmpty()
            val json = if (text.trimStart().startsWith("{")) JSONObject(text) else JSONObject()
            if (response.code == 401 && request.header("authorization") != null) throw SessionExpired("Your sign-in expired. Sign in again.")
            if (!response.isSuccessful) throw IllegalStateException(json.optString("error", "Cloud request failed (${response.code})"))
            return json
        }
    }
}

private data class WorkspaceRow(val id: String, val name: String, val status: String)
private data class OpenSandbox(val instanceId: String, val name: String, val host: String, val session: String, val cloudToken: String)
private data class ChatMessage(val fromUser: Boolean, val text: String, val meta: String = "", val error: Boolean = false)

/**
 * The whole app: sign in to the cloud, pick a workspace, and work in its E2B
 * sandbox. Nothing runs on this phone; there is no background service.
 */
@Composable
fun CloudWorkspaceScreen() {
    val api = remember { CloudApi() }
    val context = LocalContext.current
    val account = remember { context.getSharedPreferences("vectra.cloud", android.content.Context.MODE_PRIVATE) }
    val scope = rememberCoroutineScope()
    var email by rememberSaveable { mutableStateOf(account.getString("email", "").orEmpty()) }
    var password by remember { mutableStateOf("") }
    var token by rememberSaveable { mutableStateOf(account.getString("token", "").orEmpty()) }
    var workspaces by remember { mutableStateOf(listOf<WorkspaceRow>()) }
    var listed by remember { mutableStateOf(false) }
    var open by remember { mutableStateOf<OpenSandbox?>(null) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }

    fun signOut(message: String = "") {
        account.edit().remove("token").remove("email").apply()
        token = ""; open = null; workspaces = emptyList(); listed = false; error = message
    }
    suspend fun attach(row: WorkspaceRow): OpenSandbox {
        withContext(Dispatchers.IO) { api.post("/api/instances/${row.id}/wake", token, "{}") }
        val attach = withContext(Dispatchers.IO) { api.post("/api/instances/${row.id}/attach", token, JSONObject().put("device", "phone").toString()) }
        val host = attach.optString("url")
        val session = attach.optString("token")
        if (host.isBlank() || session.isBlank()) throw IllegalStateException("The workspace did not return a session")
        return OpenSandbox(row.id, row.name, host, session, token)
    }
    suspend fun loadList(with: String) {
        val rows = withContext(Dispatchers.IO) { api.get("/api/instances", with) }.optJSONArray("instances")
        val next = buildList {
            if (rows != null) for (i in 0 until rows.length()) {
                val item = rows.getJSONObject(i)
                add(WorkspaceRow(item.optString("id"), item.optString("name", "Workspace"), item.optString("status", "unknown")))
            }
        }
        workspaces = next
        if (next.size == 1 && open == null) open = attach(next.first())
        listed = true
    }
    fun run(block: suspend () -> Unit) {
        busy = true; error = ""
        scope.launch {
            try { block() } catch (e: SessionExpired) { signOut(e.message ?: "") } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message ?: "Something went wrong" } finally { busy = false }
        }
    }
    LaunchedEffect(token) { if (token.isNotBlank() && !listed) run { loadList(token) } }
    BackHandler(enabled = open != null && workspaces.size >= 2) { open = null }

    VectraTheme { Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(modifier = Modifier.fillMaxSize().safeDrawingPadding().imePadding()) {
            val current = open
            when {
                token.isBlank() -> Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
                    Column(
                        Modifier.widthIn(max = 400.dp).fillMaxWidth().clip(RoundedCornerShape(16.dp)).background(Vx.panel).border(1.dp, Vx.line, RoundedCornerShape(16.dp)).padding(28.dp),
                        verticalArrangement = Arrangement.spacedBy(16.dp),
                    ) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Box(Modifier.size(36.dp).clip(RoundedCornerShape(10.dp)).background(Vx.accent), contentAlignment = Alignment.Center) {
                                Text("V", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 16.sp)
                            }
                            Text("Vectrabot", color = Vx.text, fontWeight = FontWeight.SemiBold, fontSize = 15.sp)
                        }
                        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text("Sign in", color = Vx.text, fontSize = 28.sp, fontWeight = FontWeight.SemiBold)
                            Text("Open your Vectra Cloud workspace.", color = Vx.text2, fontSize = 14.sp)
                        }
                        OutlinedTextField(email, { email = it }, label = { Text("Email") }, placeholder = { Text("you@example.com") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                        OutlinedTextField(password, { password = it }, label = { Text("Password") }, placeholder = { Text("Password") }, singleLine = true, visualTransformation = PasswordVisualTransformation(), modifier = Modifier.fillMaxWidth())
                        if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error, fontSize = 13.sp)
                        Button(onClick = {
                            run {
                                val body = JSONObject().put("email", email.trim()).put("password", password).toString()
                                val next = withContext(Dispatchers.IO) { api.post("/api/auth/login", null, body) }.optString("token")
                                if (next.isBlank()) throw IllegalStateException("The server did not return a token")
                                account.edit().putString("token", next).putString("email", email.trim()).apply()
                                password = ""; token = next
                                loadList(next)
                            }
                        }, enabled = !busy && email.isNotBlank() && password.isNotBlank(), modifier = Modifier.fillMaxWidth().height(42.dp)) { Text(if (busy) "Signing in…" else "Continue") }
                    }
                }
                current == null && !listed -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Vx.accent) }
                current == null -> Column(modifier = Modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("Workspaces", style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
                        TextButton(onClick = { run { loadList(token) } }) { Text("Refresh") }
                        TextButton(onClick = { signOut() }) { Text("Sign out") }
                    }
                    if (workspaces.isEmpty() && !busy) Text("No workspaces yet. Create one on infra.xibeai.in.", color = secondaryTint)
                    LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.weight(1f)) {
                        items(workspaces, key = { it.id }) { row ->
                            Card(modifier = Modifier.fillMaxWidth()) {
                                Row(modifier = Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                                    Column(Modifier.weight(1f)) {
                                        Text(row.name, style = MaterialTheme.typography.titleMedium)
                                        Text(row.status, style = MaterialTheme.typography.bodySmall, color = statusColor(row.status))
                                    }
                                    Button(onClick = { run { open = attach(row) } }, enabled = !busy) { Text(if (row.status == "paused") "Wake" else "Open") }
                                }
                            }
                        }
                    }
                    if (busy) CircularProgressIndicator()
                    if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error)
                }
                else -> WorkspaceView(
                    api = api,
                    sandbox = current,
                    email = email,
                    manyWorkspaces = workspaces.size >= 2,
                    onBack = { if (workspaces.size >= 2) open = null },
                    onSignOut = { signOut() },
                )
            }
        }
    }
}
}

@Composable
private fun statusColor(status: String) = when (status) {
    "running" -> MaterialTheme.colorScheme.primary
    "error" -> MaterialTheme.colorScheme.error
    else -> secondaryTint
}

/** State that outlives tab switches while one workspace is open. */
private class WorkspaceState {
    val terminal = TerminalScreen()
    var terminalVersion by mutableIntStateOf(0)
    var shellId = newShellId()
}

private enum class Panel { Home, Chat, Info, Providers, Routines }

@Composable
private fun WorkspaceView(
    api: CloudApi,
    sandbox: OpenSandbox,
    email: String,
    manyWorkspaces: Boolean,
    onBack: () -> Unit,
    onSignOut: () -> Unit,
) {
    val client = remember { CloudSandboxClient() }
    val ws = remember(sandbox.instanceId) { WorkspaceState() }
    val context = LocalContext.current
    val bots = remember(sandbox.instanceId) {
        BotsState(BotsClient().apply {
            host = sandbox.host; session = sandbox.session
            // A 401 from the host means the device session rotated; fetch a fresh one.
            reattach = {
                runCatching {
                    val a = api.post("/api/instances/${sandbox.instanceId}/attach", sandbox.cloudToken, JSONObject().put("device", "phone").toString())
                    host = a.optString("url").ifBlank { host }; session = a.optString("token").ifBlank { session }
                }.isSuccess
            }
        }, ChatCache(java.io.File(context.filesDir, "chat/" + sandbox.instanceId)))
    }
    val scope = rememberCoroutineScope()
    var panel by rememberSaveable(sandbox.instanceId) { mutableStateOf(Panel.Home) }
    var tab by rememberSaveable(sandbox.instanceId) { mutableIntStateOf(0) }
    var editing by remember { mutableStateOf<Bot?>(null) }
    var creating by remember { mutableStateOf(false) }
    var toast by remember { mutableStateOf("") }
    LaunchedEffect(toast) { if (toast.isNotBlank()) { delay(2600); toast = "" } }
    val main = remember { Handler(Looper.getMainLooper()) }

    suspend fun reload() {
        val list = withContext(Dispatchers.IO) { bots.client.bots() }
        bots.replaceBots(list); bots.loading = false
        val tid = bots.threadId
        if (tid.isNotBlank()) runCatching { bots.loaded(tid, withContext(Dispatchers.IO) { bots.client.messages(tid) }) }
    }
    LaunchedEffect(sandbox.session) {
        runCatching { reload() }.onFailure { bots.error = it.message ?: "Couldn't load bots"; bots.loading = false }
        // Live updates, resumed from the last cursor after a drop.
        withContext(Dispatchers.IO) {
            var wait = 1000L
            while (isActive) {
                try {
                    bots.cursor = bots.client.events(bots.cursor, stop = { !isActive }) { frame ->
                        main.post { if (bots.apply(frame)) scope.launch { runCatching { reload() } } }
                    }
                    wait = 1000L
                } catch (e: CancellationException) { throw e } catch (e: HostError) {
                    if (e.status == 401) bots.client.reattach?.invoke()
                } catch (_: Exception) { }
                main.post { bots.live = false }
                delay(wait); wait = minOf(wait * 2, 15_000L)
            }
        }
    }
    BackHandler(enabled = panel != Panel.Home) {
        panel = when (panel) { Panel.Info -> Panel.Chat; Panel.Chat -> if (tab != 0) { tab = 0; Panel.Chat } else Panel.Home; else -> Panel.Home }
    }
    fun openBot(b: Bot, thread: String? = null) { bots.select(b, thread); tab = 0; panel = Panel.Chat }
    fun act(block: suspend () -> Unit) = scope.launch { try { block() } catch (e: CancellationException) { throw e } catch (e: Exception) { toast = e.message ?: "Request failed" } }

    val faces = remember(bots) { BotFaces(bots.client) }
    androidx.compose.runtime.CompositionLocalProvider(LocalBotFaces provides faces) {
    Box(Modifier.fillMaxSize().background(Vx.bg)) {
        when (panel) {
            Panel.Home -> BotHome(bots, sandbox.name, email, onOpen = { openBot(it) }, onNewBot = { creating = true },
                onProviders = { panel = Panel.Providers }, onRoutines = { panel = Panel.Routines }, onWorkspaces = onBack, onSignOut = onSignOut, manyWorkspaces = manyWorkspaces)
            Panel.Providers -> ProvidersPanel(bots.client, onBack = { panel = if (bots.botId.isNotBlank() && bots.threadId.isNotBlank()) Panel.Chat else Panel.Home })
            Panel.Routines -> RoutinesPanel(bots, onBack = { panel = Panel.Home })
            Panel.Info -> BotInfoPanel(bots, onBack = { panel = Panel.Chat }, onEdit = { editing = bots.bot },
                onThread = { bots.threadId = it; tab = 0; panel = Panel.Chat },
                onNewThread = { act { val b = bots.bot ?: return@act; val tid = withContext(Dispatchers.IO) { bots.client.newThread(b.id) }; bots.messages[tid] = androidx.compose.runtime.mutableStateListOf(); bots.threadId = tid; tab = 0; panel = Panel.Chat; runCatching { bots.mergeBot(withContext(Dispatchers.IO) { bots.client.bots() }.first { it.id == b.id }) } } },
                onRoutines = { panel = Panel.Routines }, onProviders = { panel = Panel.Providers }, onTab = { tab = it; panel = Panel.Chat }, onNotice = { toast = it })
            Panel.Chat -> Column(Modifier.fillMaxSize()) {
                val bot = bots.bot
                Row(Modifier.fillMaxWidth().height(56.dp).padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconAction(Icons.Default.ArrowBack, "Back") { panel = Panel.Home }
                    if (bot != null) Row(Modifier.weight(1f).clickable { panel = Panel.Info }, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        BotFace(bot, 32.dp, busy = bot.busy)
                        Column {
                            Text(bot.name, color = Vx.text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1)
                            Text(bot.threads.firstOrNull { it.id == bots.threadId }?.title ?: "New thread", color = Vx.text3, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    } else Spacer(Modifier.weight(1f))
                    IconAction(Icons.Default.Info, "Bot details") { panel = Panel.Info }
                }
                ViewTabs(tab) { tab = it }
                Box(Modifier.fillMaxWidth().height(1.dp).background(Vx.line))
                Box(Modifier.weight(1f).fillMaxWidth()) {
                    when (tab) {
                        0 -> BotChatPane(bots, onProviders = { panel = Panel.Providers }, onNotice = { toast = it })
                        1 -> Box(Modifier.padding(10.dp)) { TerminalTab(ws, sandbox) }
                        2 -> Box(Modifier.padding(10.dp)) { PreviewTab(api, sandbox, client) }
                        else -> Box(Modifier.padding(10.dp)) { DesktopTab(api, sandbox, client) }
                    }
                }
            }
        }
        if (toast.isNotBlank()) Text(toast, color = Vx.text, fontSize = 13.sp, modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = 96.dp).clip(Vx.small).background(Vx.raised2).border(1.dp, Vx.line2, Vx.small).padding(horizontal = 14.dp, vertical = 9.dp).testTag("toast"))
    }
    if (creating || editing != null) {
        val existing = editing
        BotEditorDialog(existing, takenColors = bots.bots.map { it.color.lowercase() }, onDismiss = { creating = false; editing = null },
            onSave = { d ->
                act {
                    val fields = JSONObject().put("name", d.name).put("title", d.title).put("description", d.description).put("instructions", d.instructions)
                        .put("color", d.color).put("emoji", d.emoji).put("avatarShape", d.avatarShape)
                    if (d.avatarKind != "image") fields.put("avatarKind", d.avatarKind)
                    d.glyphSeed?.let { fields.put("glyphSeed", it) }
                    if (existing != null && !existing.main) fields.put("pinned", d.pinned)
                    var b = withContext(Dispatchers.IO) { if (existing == null) bots.client.createBot(fields) else bots.client.updateBot(existing.id, fields) }
                    val img = d.image
                    if (img != null) b = withContext(Dispatchers.IO) { bots.client.uploadAvatar(b.id, img) }
                    else if (d.removeImage && existing?.avatarUrl != null) b = withContext(Dispatchers.IO) { bots.client.deleteAvatar(b.id) }
                    if (d.avatarKind == "image" && b.avatarKind != "image") b = withContext(Dispatchers.IO) { bots.client.updateBot(b.id, JSONObject().put("avatarKind", "image")) }
                    bots.mergeBot(b)
                    if (existing == null) openBot(b)
                    creating = false; editing = null
                }
            },
            onDelete = { act { withContext(Dispatchers.IO) { bots.client.deleteBot(existing!!.id) }; bots.bots = bots.bots.filterNot { it.id == existing!!.id }; bots.botId = ""; editing = null; panel = Panel.Home } },
            onDuplicate = { act { val b = withContext(Dispatchers.IO) { bots.client.duplicateBot(existing!!.id) }; bots.mergeBot(b); editing = null; openBot(b); toast = "Made ${b.name}" } },
        )
    }
    }
}

@Composable
private fun ViewTabs(selected: Int, onSelect: (Int) -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp).clip(RoundedCornerShape(9.dp)).background(Vx.panel).border(1.dp, Vx.line, RoundedCornerShape(9.dp)).padding(3.dp)) {
        listOf("Chat", "Terminal", "Preview", "Desktop").forEachIndexed { i, label ->
            Text(label, color = if (selected == i) Vx.text else Vx.text2, fontSize = 13.sp, fontWeight = FontWeight.Medium, textAlign = TextAlign.Center,
                modifier = Modifier.weight(1f).clip(RoundedCornerShape(6.dp)).background(if (selected == i) Vx.raised2 else Color.Transparent).clickable { onSelect(i) }.padding(vertical = 7.dp).testTag("tab-$label"))
        }
    }
}

private class Control(val id: String, val expiresAt: Long)

private suspend fun handoff(api: CloudApi, sandbox: OpenSandbox, action: String, id: String, minutes: Int = 15): Long {
    val body = JSONObject().put("action", action).put("minutes", minutes).put("handoffId", id).put("botId", "main").toString()
    val reply = withContext(Dispatchers.IO) { api.post("/api/instances/${sandbox.instanceId}/computer/handoff", sandbox.cloudToken, body) }
    if (!reply.optBoolean("ok") || reply.optBoolean("handoff") != (action == "start")) {
        throw IllegalStateException(reply.optString("error").ifBlank { if (action == "start") "Takeover could not be confirmed" else "Return could not be confirmed" })
    }
    return reply.optLong("expiresAt").takeIf { it > 0 } ?: (System.currentTimeMillis() + minutes * 60_000L)
}

@Composable
private fun DesktopTab(api: CloudApi, sandbox: OpenSandbox, client: CloudSandboxClient) {
    val scope = rememberCoroutineScope()
    var control by remember { mutableStateOf<Control?>(null) }
    var socket by remember { mutableStateOf("") }
    var frame by remember { mutableStateOf<ImageBitmap?>(null) }
    var status by remember { mutableStateOf("Live view") }
    var clip by remember { mutableStateOf("") }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var busy by remember { mutableStateOf(false) }

    fun act(block: suspend () -> Unit) {
        busy = true
        scope.launch { try { block() } catch (e: CancellationException) { throw e } catch (e: Exception) { status = e.message ?: "Desktop request failed" } finally { busy = false } }
    }
    fun returnControl() = act {
        val held = control ?: return@act
        control = null; socket = ""
        handoff(api, sandbox, "stop", held.id)
        status = "Control returned to the agent"
    }
    // Live view: the agent's screen, view only, until the person takes control.
    LaunchedEffect(control == null) {
        if (control != null) return@LaunchedEffect
        var version = 0L
        while (isActive) {
            try {
                val next = withContext(Dispatchers.IO) { client.frame(sandbox.host, sandbox.session, version) }
                if (next != null) {
                    version = next.version
                    BitmapFactory.decodeByteArray(next.jpeg, 0, next.jpeg.size)?.let { frame = it.asImageBitmap() }
                }
            } catch (e: CancellationException) { throw e } catch (e: Exception) { status = e.message ?: "Live view unavailable"; delay(2000) }
        }
    }
    LaunchedEffect(control) {
        while (control != null) {
            now = System.currentTimeMillis()
            if (now >= (control?.expiresAt ?: Long.MAX_VALUE)) { control = null; socket = ""; status = "Control expired · returned to the agent" }
            delay(1000)
        }
    }
    // Leaving the tab hands the desktop back, as the PC app does on leaving the page.
    DisposableEffect(Unit) {
        onDispose { control?.let { held -> scope.launch { runCatching { handoff(api, sandbox, "stop", held.id) } } } }
    }

    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            val held = control
            Text(if (held != null) "You have control · ${((held.expiresAt - now).coerceAtLeast(0) / 1000).let { "%d:%02d".format(it / 60, it % 60) }}" else status, style = MaterialTheme.typography.labelLarge, modifier = Modifier.weight(1f))
            if (held == null) {
                Button(onClick = {
                    act {
                        val id = java.util.UUID.randomUUID().toString().replace("-", "")
                        val expires = handoff(api, sandbox, "start", id)
                        control = Control(id, expires)
                        socket = withContext(Dispatchers.IO) { client.vncTicket(CLOUD_BASE, sandbox.cloudToken, sandbox.instanceId, id) }
                    }
                }, enabled = !busy) { Text("Take control") }
            } else {
                OutlinedButton(onClick = { act { socket = withContext(Dispatchers.IO) { client.vncTicket(CLOUD_BASE, sandbox.cloudToken, sandbox.instanceId, held.id) } } }, enabled = !busy) { Text("Reconnect") }
                Button(onClick = { returnControl() }, enabled = !busy) { Text("Return") }
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth().background(androidx.compose.ui.graphics.Color.Black, RoundedCornerShape(8.dp)), contentAlignment = Alignment.Center) {
            when {
                control != null && socket.isNotBlank() -> DesktopViewer(socket)
                control != null -> CircularProgressIndicator()
                frame != null -> Image(frame!!, contentDescription = "The agent's desktop", contentScale = ContentScale.Fit, modifier = Modifier.fillMaxWidth().aspectRatio(1280f / 800f))
                else -> Text("Loading the desktop…", color = secondaryTint)
            }
        }
        if (control != null) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                OutlinedTextField(clip, { clip = it }, placeholder = { Text("Clipboard") }, singleLine = true, modifier = Modifier.weight(1f))
                TextButton(onClick = { act { clip = withContext(Dispatchers.IO) { client.clipboard(sandbox.host, sandbox.session) } } }) { Text("Get") }
                TextButton(onClick = { act { withContext(Dispatchers.IO) { client.sendClipboard(sandbox.host, sandbox.session, clip) }; status = "Sent to the desktop clipboard" } }) { Text("Send") }
            }
        } else {
            Text("Take control to use the mouse and keyboard; the agent waits until you return it. The sandbox pauses after 15 idle minutes and keeps everything.", style = MaterialTheme.typography.bodySmall, color = secondaryTint)
        }
    }
}

private fun desktopBundleClient(context: android.content.Context) = object : WebViewClient() {
    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
        if (request.url.lastPathSegment != "rfb-bundle.js") return null
        return WebResourceResponse("text/javascript", "utf-8", context.assets.open("novnc/rfb-bundle.js"))
    }
}

@Composable
private fun DesktopViewer(socket: String) {
    AndroidView(
        factory = { context ->
            WebView(context).apply {
                webViewClient = desktopBundleClient(context)
                settings.javaScriptEnabled = true
                tag = socket
                loadDataWithBaseURL("https://desktop.vectrabot.invalid/", desktopViewerHtml(socket), "text/html", "utf-8", null)
            }
        },
        update = { view ->
            if (view.tag != socket) {
                view.tag = socket
                view.loadDataWithBaseURL("https://desktop.vectrabot.invalid/", desktopViewerHtml(socket), "text/html", "utf-8", null)
            }
        },
        modifier = Modifier.fillMaxSize(),
    )
}

@Composable
private fun PreviewTab(api: CloudApi, sandbox: OpenSandbox, client: CloudSandboxClient) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    var port by rememberSaveable { mutableStateOf("3000") }
    var ports by remember { mutableStateOf(listOf<Int>()) }
    var url by rememberSaveable { mutableStateOf("") }
    var note by remember { mutableStateOf("") }
    var reload by remember { mutableIntStateOf(0) }
    fun refreshPorts() { scope.launch { runCatching { ports = withContext(Dispatchers.IO) { client.ports(sandbox.host, sandbox.session) } } } }
    fun show(target: Int) {
        port = target.toString()
        scope.launch {
            try {
                val listening = withContext(Dispatchers.IO) { client.listening(sandbox.host, sandbox.session, target) }
                val host = withContext(Dispatchers.IO) { api.get("/api/instances/${sandbox.instanceId}/preview?port=$target", sandbox.cloudToken) }.optString("host")
                url = previewUrl(host)
                note = if (listening) "" else "Nothing is listening on port $target yet. Start your app in the terminal or ask the agent, then Reload."
                refreshPorts()
            } catch (e: CancellationException) { throw e } catch (e: Exception) { note = e.message ?: "Preview unavailable" }
        }
    }
    LaunchedEffect(sandbox.instanceId) { refreshPorts(); while (isActive) { delay(10_000); refreshPorts() } }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(port, { port = it.filter(Char::isDigit).take(5) }, label = { Text("Port") }, singleLine = true, modifier = Modifier.weight(1f))
            Button(onClick = { port.toIntOrNull()?.takeIf { it in 1..65535 }?.let(::show) ?: run { note = "Enter a port between 1 and 65535." } }) { Text("Open") }
        }
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
            if (ports.isEmpty()) Text("No app is listening yet.", style = MaterialTheme.typography.bodySmall, color = secondaryTint)
            ports.forEach { p -> FilterChip(selected = port == p.toString() && url.isNotBlank(), onClick = { show(p) }, label = { Text(":$p") }) }
            if (url.isNotBlank()) {
                TextButton(onClick = { reload++ }) { Text("Reload") }
                TextButton(onClick = { runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) } }) { Text("Open in browser ↗") }
            }
        }
        if (note.isNotBlank()) Text(note, style = MaterialTheme.typography.bodySmall, color = secondaryTint)
        if (url.isNotBlank()) {
            AndroidView(
                factory = { ctx -> WebView(ctx).apply { webViewClient = WebViewClient(); settings.javaScriptEnabled = true; settings.domStorageEnabled = true } },
                update = { view ->
                    val key = "$url#$reload"
                    if (view.tag != key) { view.tag = key; if (view.url == url) view.reload() else view.loadUrl(url) }
                },
                modifier = Modifier.weight(1f).fillMaxWidth(),
            )
        }
    }
}

/** One terminal connection that resumes the workspace's shell when it drops. */
private class TerminalLink(private val ws: WorkspaceState, private val url: () -> String, private val onState: (String) -> Unit) {
    private val http = OkHttpClient.Builder().pingInterval(20, TimeUnit.SECONDS).build()
    private val main = Handler(Looper.getMainLooper())
    private var socket: WebSocket? = null
    private var wanted = true
    private var retry = 0
    var size: Pair<Int, Int>? = null
        set(value) { field = value; value?.let { sendResize(it) } }

    fun connect() {
        wanted = true
        socket?.cancel()
        onState("Connecting…")
        socket = http.newWebSocket(Request.Builder().url(url()).build(), object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                main.post {
                    when (val control = terminalControl(text)) {
                        is TerminalControl.Attached -> {
                            retry = 0
                            if (!control.resumed) { ws.terminal.clear() }
                            onState(if (control.resumed) "Connected · resumed" else "Connected")
                            size?.let(::sendResize)
                        }
                        TerminalControl.Exited -> { onState("Shell exited"); ws.shellId = newShellId() }
                        null -> Unit
                    }
                    ws.terminalVersion++
                }
            }
            override fun onMessage(webSocket: WebSocket, bytes: okio.ByteString) {
                main.post { ws.terminal.append(bytes.utf8()); ws.terminalVersion++ }
            }
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = dropped(webSocket, reason)
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = dropped(webSocket, t.message ?: "connection lost")
        })
    }

    private fun dropped(from: WebSocket, reason: String) {
        main.post {
            if (from !== socket || !wanted) return@post
            val wait = (1000L shl retry.coerceAtMost(4)).coerceAtMost(15_000L)
            retry++
            onState("Reconnecting in ${wait / 1000}s ($reason)")
            main.postDelayed({ if (wanted && from === socket) connect() }, wait)
        }
    }

    fun send(text: String) { socket?.send(okio.ByteString.of(*text.toByteArray())) }

    private fun sendResize(cr: Pair<Int, Int>) { socket?.send(JSONObject().put("type", "resize").put("cols", cr.first).put("rows", cr.second).toString()) }

    fun close() { wanted = false; socket?.close(1000, "leave"); socket = null; main.removeCallbacksAndMessages(null); http.dispatcher.executorService.shutdown() }
}

@Composable
private fun TerminalTab(ws: WorkspaceState, sandbox: OpenSandbox) {
    var state by remember { mutableStateOf("Connecting…") }
    var draft by remember { mutableStateOf("") }
    val link = remember(sandbox.session) { TerminalLink(ws, { terminalUrl(sandbox.host, sandbox.session, ws.shellId) }) { state = it } }
    DisposableEffect(link) { link.connect(); onDispose { link.close() } }
    val scroll = rememberScrollState()
    LaunchedEffect(ws.terminalVersion) { scroll.scrollTo(scroll.maxValue) }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(state, style = MaterialTheme.typography.labelMedium, modifier = Modifier.weight(1f))
            TextButton(onClick = { link.connect() }) { Text("Reconnect") }
            TextButton(onClick = { ws.shellId = newShellId(); ws.terminal.clear(); ws.terminalVersion++; link.connect() }) { Text("New shell") }
        }
        BoxWithConstraints(Modifier.weight(1f).fillMaxWidth().background(androidx.compose.ui.graphics.Color(0xFF0B0D10), RoundedCornerShape(8.dp)).padding(8.dp)) {
            val cols = (maxWidth.value / 7.2f).toInt().coerceIn(20, 300)
            val rows = (maxHeight.value / 15f).toInt().coerceIn(5, 200)
            LaunchedEffect(cols, rows) { link.size = cols to rows }
            val screen = ws.terminalVersion.let { ws.terminal.value }
            SelectionContainer(Modifier.fillMaxSize().verticalScroll(scroll)) {
                Text(screen.ifEmpty { " " }, fontFamily = FontFamily.Monospace, fontSize = 12.sp, lineHeight = 15.sp, color = androidx.compose.ui.graphics.Color(0xFFD6DEEB))
            }
        }
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            listOf("Tab" to "\t", "Ctrl-C" to "\u0003", "Ctrl-D" to "\u0004", "Esc" to "\u001b", "↑" to "\u001b[A", "↓" to "\u001b[B").forEach { (label, keys) ->
                OutlinedButton(onClick = { link.send(keys) }) { Text(label) }
            }
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(draft, { draft = it }, placeholder = { Text("Command") }, singleLine = true, textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace), modifier = Modifier.weight(1f))
            Button(onClick = { link.send(draft + "\r"); draft = "" }) { Text("Run") }
        }
    }
}

