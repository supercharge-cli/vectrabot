package com.openmausbot.companion.ui

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Handler
import android.os.Looper
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
    val scope = rememberCoroutineScope()
    var email by rememberSaveable { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var token by rememberSaveable { mutableStateOf("") }
    var workspaces by remember { mutableStateOf(listOf<WorkspaceRow>()) }
    var open by remember { mutableStateOf<OpenSandbox?>(null) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }

    fun signOut(message: String = "") { token = ""; open = null; workspaces = emptyList(); error = message }
    suspend fun loadList(with: String) {
        val rows = withContext(Dispatchers.IO) { api.get("/api/instances", with) }.optJSONArray("instances")
        workspaces = buildList {
            if (rows != null) for (i in 0 until rows.length()) {
                val item = rows.getJSONObject(i)
                add(WorkspaceRow(item.optString("id"), item.optString("name", "Workspace"), item.optString("status", "unknown")))
            }
        }
    }
    fun run(block: suspend () -> Unit) {
        busy = true; error = ""
        scope.launch {
            try { block() } catch (e: SessionExpired) { signOut(e.message ?: "") } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message ?: "Something went wrong" } finally { busy = false }
        }
    }
    suspend fun attach(row: WorkspaceRow): OpenSandbox {
        withContext(Dispatchers.IO) { api.post("/api/instances/${row.id}/wake", token, "{}") }
        val attach = withContext(Dispatchers.IO) { api.post("/api/instances/${row.id}/attach", token, JSONObject().put("device", "phone").toString()) }
        val host = attach.optString("url")
        val session = attach.optString("token")
        if (host.isBlank() || session.isBlank()) throw IllegalStateException("The workspace did not return a session")
        return OpenSandbox(row.id, row.name, host, session, token)
    }
    LaunchedEffect(token) { if (token.isNotBlank() && workspaces.isEmpty()) run { loadList(token) } }
    BackHandler(enabled = open != null) { open = null }

    Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        Column(modifier = Modifier.fillMaxSize().safeDrawingPadding().imePadding()) {
            val current = open
            when {
                token.isBlank() -> Column(
                    modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
                    verticalArrangement = Arrangement.spacedBy(14.dp),
                ) {
                    Spacer(Modifier.height(24.dp))
                    Text("Vectra", style = MaterialTheme.typography.headlineMedium)
                    Text("Sign in to your cloud workspaces. The agent, its desktop and your apps run in the cloud sandbox, not on this phone.", style = MaterialTheme.typography.bodyMedium, color = secondaryTint)
                    OutlinedTextField(email, { email = it }, label = { Text("Email") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    OutlinedTextField(password, { password = it }, label = { Text("Password") }, singleLine = true, visualTransformation = PasswordVisualTransformation(), modifier = Modifier.fillMaxWidth())
                    Button(onClick = {
                        run {
                            val body = JSONObject().put("email", email.trim()).put("password", password).toString()
                            val next = withContext(Dispatchers.IO) { api.post("/api/auth/login", null, body) }.optString("token")
                            if (next.isBlank()) throw IllegalStateException("The server did not return a token")
                            loadList(next); password = ""; token = next
                        }
                    }, enabled = !busy && email.isNotBlank() && password.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text(if (busy) "Signing in…" else "Sign in") }
                    if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error)
                }
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
                    onBack = { open = null },
                )
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
    val chat = mutableStateListOf<ChatMessage>()
    var agentId by mutableStateOf("main")
    var agents by mutableStateOf(listOf(SandboxAgent("main", "Main", "")))
    val terminal = TerminalScreen()
    var terminalVersion by mutableIntStateOf(0)
    var shellId = newShellId()
}

@Composable
private fun WorkspaceView(
    api: CloudApi,
    sandbox: OpenSandbox,
    onBack: () -> Unit,
) {
    val client = remember { CloudSandboxClient() }
    val ws = remember(sandbox.instanceId) { WorkspaceState() }
    var tab by rememberSaveable(sandbox.instanceId) { mutableIntStateOf(0) }
    var note by remember { mutableStateOf("") }
    val tabs = listOf("Chat", "Desktop", "Preview", "Terminal", "Agents")
    suspend fun refreshAgents() {
        val list = withContext(Dispatchers.IO) { client.agents(sandbox.host, sandbox.session) }
        if (list.isNotEmpty()) ws.agents = list
        if (ws.agents.none { it.id == ws.agentId }) ws.agentId = "main"
    }
    LaunchedEffect(sandbox.session) { runCatching { refreshAgents() } }
    Column(modifier = Modifier.fillMaxSize()) {
        Row(modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = onBack) { Text("‹ Workspaces") }
            Text(sandbox.name, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
            Text("● Running", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelMedium)
        }
        ScrollableTabRow(selectedTabIndex = tab, edgePadding = 8.dp) {
            tabs.forEachIndexed { i, title -> Tab(selected = tab == i, onClick = { tab = i }, text = { Text(title) }) }
        }
        if (note.isNotBlank()) Text(note, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(8.dp))
        Box(modifier = Modifier.weight(1f).fillMaxWidth().padding(10.dp)) {
            when (tab) {
                0 -> ChatTab(ws, sandbox, client)
                1 -> DesktopTab(api, sandbox, client)
                2 -> PreviewTab(api, sandbox, client)
                3 -> TerminalTab(ws, sandbox)
                else -> AgentsTab(ws, sandbox, client, refresh = { refreshAgents() }, onChat = { ws.agentId = it; tab = 0 })
            }
        }
    }
    // Leaving the workspace also leaves the terminal socket; the shell lingers on the host.
    DisposableEffect(sandbox.instanceId) { onDispose { note = "" } }
}

@Composable
private fun ChatTab(ws: WorkspaceState, sandbox: OpenSandbox, client: CloudSandboxClient) {
    val scope = rememberCoroutineScope()
    var draft by rememberSaveable { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var elapsed by remember { mutableIntStateOf(0) }
    val list = rememberLazyListState()
    LaunchedEffect(ws.chat.size) { if (ws.chat.isNotEmpty()) list.animateScrollToItem(ws.chat.size - 1) }
    LaunchedEffect(sending) { elapsed = 0; while (sending) { delay(1000); elapsed++ } }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            ws.agents.forEach { agent -> FilterChip(selected = agent.id == ws.agentId, onClick = { ws.agentId = agent.id }, label = { Text(agent.name) }) }
        }
        LazyColumn(state = list, modifier = Modifier.weight(1f).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (ws.chat.isEmpty()) item { Text("Ask the agent to build, fix or run something in this workspace. Watch it work in the Desktop tab.", color = secondaryTint) }
            items(ws.chat) { message -> ChatBubble(message) }
            if (sending) item { Text("Working… ${elapsed}s", color = secondaryTint, style = MaterialTheme.typography.bodySmall) }
        }
        Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(draft, { draft = it }, placeholder = { Text("Message the agent") }, modifier = Modifier.weight(1f), maxLines = 5)
            Button(onClick = {
                val prompt = draft.trim(); draft = ""; sending = true
                val agent = ws.agents.firstOrNull { it.id == ws.agentId } ?: ws.agents.first()
                ws.chat.add(ChatMessage(true, prompt))
                scope.launch {
                    val started = System.currentTimeMillis()
                    ws.chat.add(try {
                        val reply = withContext(Dispatchers.IO) { client.turn(sandbox.host, sandbox.session, prompt, agent.id.takeIf { it != "main" }) }
                        ChatMessage(false, reply, "${agent.name} · ${(System.currentTimeMillis() - started) / 100 / 10.0}s")
                    } catch (e: CancellationException) { throw e } catch (e: Exception) {
                        ChatMessage(false, e.message ?: "The turn failed", agent.name, error = true)
                    } finally { sending = false })
                }
            }, enabled = !sending && draft.isNotBlank()) { Text("Send") }
        }
    }
}

@Composable
private fun ChatBubble(message: ChatMessage) {
    val colors = MaterialTheme.colorScheme
    Row(Modifier.fillMaxWidth(), horizontalArrangement = if (message.fromUser) Arrangement.End else Arrangement.Start) {
        Column(
            Modifier.widthIn(max = 320.dp)
                .background(if (message.fromUser) colors.primaryContainer else if (message.error) colors.errorContainer else colors.surfaceVariant, RoundedCornerShape(14.dp))
                .padding(12.dp),
        ) {
            SelectionContainer { Text(message.text, color = if (message.fromUser) colors.onPrimaryContainer else if (message.error) colors.onErrorContainer else colors.onSurfaceVariant) }
            if (message.meta.isNotBlank()) Text(message.meta, style = MaterialTheme.typography.labelSmall, color = secondaryTint)
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

@Composable
private fun DesktopViewer(socket: String) {
    AndroidView(
        factory = { context ->
            WebView(context).apply {
                webViewClient = WebViewClient()
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

@Composable
private fun AgentsTab(ws: WorkspaceState, sandbox: OpenSandbox, client: CloudSandboxClient, refresh: suspend () -> Unit, onChat: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    var name by remember { mutableStateOf("") }
    var instructions by remember { mutableStateOf("") }
    var note by remember { mutableStateOf("") }
    var removing by remember { mutableStateOf<SandboxAgent?>(null) }
    fun act(block: suspend () -> Unit) { scope.launch { note = try { block(); "" } catch (e: CancellationException) { throw e } catch (e: Exception) { e.message ?: "Request failed" } } }
    LaunchedEffect(Unit) { act { refresh() } }
    LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        items(ws.agents, key = { it.id }) { agent ->
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(12.dp)) {
                    Text(agent.name, style = MaterialTheme.typography.titleMedium)
                    if (agent.instructions.isNotBlank()) Text(agent.instructions, style = MaterialTheme.typography.bodySmall, color = secondaryTint, maxLines = 3)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        TextButton(onClick = { onChat(agent.id) }) { Text("Chat") }
                        if (agent.id != "main") TextButton(onClick = { removing = agent }) { Text("Remove", color = MaterialTheme.colorScheme.error) }
                    }
                }
            }
        }
        item {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("New agent", style = MaterialTheme.typography.titleSmall)
                OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(instructions, { instructions = it }, label = { Text("Instructions") }, minLines = 2, modifier = Modifier.fillMaxWidth())
                Button(onClick = { act { withContext(Dispatchers.IO) { client.putAgent(sandbox.host, sandbox.session, name, instructions) }; name = ""; instructions = ""; refresh() } }, enabled = name.isNotBlank()) { Text("Add agent") }
                if (note.isNotBlank()) Text(note, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
    removing?.let { agent ->
        AlertDialog(
            onDismissRequest = { removing = null },
            title = { Text("Remove ${agent.name}?") },
            text = { Text("Its chats in this workspace stop; files stay.") },
            confirmButton = { TextButton(onClick = { removing = null; act { withContext(Dispatchers.IO) { client.deleteAgent(sandbox.host, sandbox.session, agent.id) }; if (ws.agentId == agent.id) ws.agentId = "main"; refresh() } }) { Text("Remove") } },
            dismissButton = { TextButton(onClick = { removing = null }) { Text("Cancel") } },
        )
    }
}
