package com.openmausbot.companion.ui

import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import android.os.Handler
import android.os.Looper
import kotlinx.coroutines.Dispatchers
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
    private val http = OkHttpClient.Builder()
        .callTimeout(30, TimeUnit.SECONDS)
        .build()

    fun post(path: String, token: String?, body: String): JSONObject {
        val call = Request.Builder()
            .url(CLOUD_BASE + path)
            .post(body.toRequestBody(jsonType))
        if (!token.isNullOrBlank()) call.header("authorization", "Bearer $token")
        return read(call.build())
    }

    fun put(path: String, token: String, body: String): JSONObject {
        val call = Request.Builder()
            .url(CLOUD_BASE + path)
            .put(body.toRequestBody(jsonType))
            .header("authorization", "Bearer $token")
        return read(call.build())
    }

    fun get(path: String, token: String): JSONObject {
        val call = Request.Builder()
            .url(CLOUD_BASE + path)
            .header("authorization", "Bearer $token")
            .get()
        return read(call.build())
    }

    private fun read(request: Request): JSONObject {
        http.newCall(request).execute().use { response ->
            val text = response.body?.string().orEmpty()
            val json = if (text.startsWith("{")) JSONObject(text) else JSONObject()
            if (!response.isSuccessful) {
                throw IllegalStateException(json.optString("error", "Cloud request failed (${response.code})"))
            }
            return json
        }
    }
}

@Composable
fun CloudWorkspaceScreen(onBack: () -> Unit) {
    val api = remember { CloudApi() }
    val scope = rememberCoroutineScope()
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var token by remember { mutableStateOf("") }
    var workspaces by remember { mutableStateOf(listOf<Pair<String, String>>()) }
    var open by remember { mutableStateOf<OpenSandbox?>(null) }
    var tab by remember { mutableStateOf("terminal") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }

    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("Vectra Cloud", style = MaterialTheme.typography.headlineSmall)
        Text(
            "This phone signs in to our cloud. The workspace runs in the E2B sandbox, not on this device.",
            style = MaterialTheme.typography.bodyMedium,
        )
        if (token.isBlank()) {
            OutlinedTextField(email, { email = it }, label = { Text("Email") }, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(
                password,
                { password = it },
                label = { Text("Password") },
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth(),
            )
            Button(
                onClick = {
                    busy = true
                    error = ""
                    scope.launch {
                        try {
                            val body = JSONObject().put("email", email.trim()).put("password", password).toString()
                            val signed = withContext(Dispatchers.IO) { api.post("/api/auth/login", null, body) }
                            val next = signed.optString("token")
                            if (next.isBlank()) throw IllegalStateException("Server did not return a token")
                            val list = withContext(Dispatchers.IO) { api.get("/api/instances", next) }
                            val rows = list.optJSONArray("instances")
                            val parsed = buildList {
                                if (rows != null) {
                                    for (i in 0 until rows.length()) {
                                        val item = rows.getJSONObject(i)
                                        add(item.optString("id") to item.optString("name", "Workspace"))
                                    }
                                }
                            }
                            token = next
                            workspaces = parsed
                        } catch (failure: Exception) {
                            error = failure.message ?: "Sign-in failed"
                        } finally {
                            busy = false
                        }
                    }
                },
                enabled = !busy && email.isNotBlank() && password.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) { Text("Sign in") }
        } else {
            Text("Workspaces", style = MaterialTheme.typography.titleMedium)
            workspaces.forEach { (id, name) ->
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text(name, modifier = Modifier.weight(1f))
                    TextButton(onClick = {
                        busy = true
                        error = ""
                        scope.launch {
                            try {
                                val attach = withContext(Dispatchers.IO) { api.post("/api/instances/$id/attach", token, "{}") }
                                val host = attach.optString("url")
                                val session = attach.optString("token")
                                if (host.isBlank() || session.isBlank()) throw IllegalStateException("Sandbox did not return a session")
                                open = OpenSandbox(id, host, session, token)
                                tab = "terminal"
                            } catch (failure: Exception) {
                                error = failure.message ?: "Workspace did not open"
                            } finally {
                                busy = false
                            }
                        }
                    }) { Text("Open") }
                }
            }
            open?.let { sandbox ->
                SandboxPane(api, sandbox, tab, onTab = { tab = it })
            }
        }
        if (busy) CircularProgressIndicator()
        if (error.isNotBlank()) Text(error, color = MaterialTheme.colorScheme.error)
        TextButton(onClick = onBack) { Text("Back") }
    }
}

private data class OpenSandbox(val instanceId: String, val host: String, val session: String, val cloudToken: String)

@Composable
private fun SandboxPane(api: CloudApi, sandbox: OpenSandbox, tab: String, onTab: (String) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf("terminal", "preview", "desktop", "settings").forEach { name ->
                TextButton(onClick = { onTab(name) }) { Text(name.replaceFirstChar { it.uppercase() }) }
            }
        }
        when (tab) {
            "terminal" -> CloudTerminal(sandbox)
            "preview" -> CloudPage(api, sandbox, 3000, "Preview")
            "desktop" -> CloudPage(api, sandbox, 6080, "Desktop")
            else -> CloudSettings(api, sandbox)
        }
    }
}

@Composable
private fun CloudTerminal(sandbox: OpenSandbox) {
    var screen by remember(sandbox.host) { mutableStateOf("") }
    var draft by remember { mutableStateOf("") }
    val http = remember { OkHttpClient.Builder().build() }
    val socket = remember(sandbox.host) { mutableStateOf<WebSocket?>(null) }
    DisposableEffect(sandbox.host) {
        val url = sandbox.host.replaceFirst("https://", "wss://").replaceFirst("http://", "ws://")
            .trimEnd('/') + "/v1/terminal?token=" + java.net.URLEncoder.encode(sandbox.session, "UTF-8")
        val main = Handler(Looper.getMainLooper())
        val ws = http.newWebSocket(Request.Builder().url(url).build(), object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) { main.post { screen += text } }
            override fun onMessage(webSocket: WebSocket, bytes: okio.ByteString) { main.post { screen += bytes.utf8() } }
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                main.post { screen += "\n" + (t.message ?: "terminal failed") }
            }
        })
        socket.value = ws
        onDispose { ws.close(1000, "leave"); http.dispatcher.executorService.shutdown() }
    }
    Text(screen.ifBlank { "Terminal connecting…" }, style = MaterialTheme.typography.bodySmall, modifier = Modifier.height(220.dp).verticalScroll(rememberScrollState()))
    Row {
        BasicTextField(draft, { draft = it }, modifier = Modifier.weight(1f))
        TextButton(onClick = {
            socket.value?.send(draft + "\n")
            draft = ""
        }) { Text("Send") }
    }
}

@Composable
private fun CloudPage(api: CloudApi, sandbox: OpenSandbox, port: Int, title: String) {
    var page by remember(port) { mutableStateOf("") }
    var note by remember(port) { mutableStateOf("Loading $title…") }
    LaunchedEffect(port) {
        try {
            val body = withContext(Dispatchers.IO) { api.get("/api/instances/${sandbox.instanceId}/preview?port=$port", sandbox.cloudToken) }
            page = body.optString("host")
            note = if (page.isBlank()) "No $title host" else ""
        } catch (failure: Exception) {
            note = failure.message ?: "$title unavailable"
        }
    }
    if (note.isNotBlank()) Text(note)
    if (page.isNotBlank()) {
        AndroidView(
            factory = { context ->
                WebView(context).apply {
                    webViewClient = WebViewClient()
                    settings.javaScriptEnabled = true
                    loadUrl(page)
                }
            },
            update = { if (it.url != page) it.loadUrl(page) },
            modifier = Modifier.fillMaxWidth().height(360.dp),
        )
    }
}

@Composable
private fun CloudSettings(api: CloudApi, sandbox: OpenSandbox) {
    var text by remember { mutableStateOf("Loading settings…") }
    var name by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    suspend fun reload() {
        val body = withContext(Dispatchers.IO) { api.get("/api/instances/${sandbox.instanceId}/sandbox-state", sandbox.cloudToken) }
        text = body.toString(2)
    }
    LaunchedEffect(sandbox.instanceId) {
        try { reload() } catch (failure: Exception) { text = failure.message ?: "Settings unavailable" }
    }
    Text("Agents use the Supercharge engine only.", style = MaterialTheme.typography.bodySmall)
    OutlinedTextField(name, { name = it }, label = { Text("New agent name") }, modifier = Modifier.fillMaxWidth())
    Button(onClick = {
        scope.launch {
            try {
                val id = name.trim().lowercase().replace(Regex("[^a-z0-9-]"), "").take(64)
                val agent = JSONObject().put("id", id).put("name", name.trim()).put("engine", "supercharge")
                withContext(Dispatchers.IO) {
                    api.put("/api/instances/${sandbox.instanceId}/sandbox-state", sandbox.cloudToken, JSONObject().put("agent", agent).toString())
                }
                name = ""
                reload()
            } catch (failure: Exception) {
                text = failure.message ?: "Agent was not saved"
            }
        }
    }, enabled = name.isNotBlank()) { Text("Add agent") }
    Text(text, style = MaterialTheme.typography.bodySmall)
}
