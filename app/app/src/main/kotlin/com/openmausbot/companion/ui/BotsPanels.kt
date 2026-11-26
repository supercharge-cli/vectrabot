package com.openmausbot.companion.ui

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.DateRange
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.ExitToApp
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

@Composable
internal fun PanelTopBar(title: String, onBack: (() -> Unit)?, actions: @Composable () -> Unit = {}) {
    Row(Modifier.fillMaxWidth().height(52.dp).padding(horizontal = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        if (onBack != null) IconAction(Icons.Default.ArrowBack, "Back", onBack) else Spacer(Modifier.size(10.dp))
        Text(title, color = Vx.text, fontSize = 16.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(start = 4.dp))
        actions()
    }
    Box(Modifier.fillMaxWidth().height(1.dp).background(Vx.line))
}

@Composable
internal fun IconAction(icon: ImageVector, label: String, onClick: () -> Unit) {
    Box(Modifier.size(40.dp).clip(CircleShape).clickable(onClick = onClick).testTag("action-$label"), contentAlignment = Alignment.Center) {
        Icon(icon, label, tint = Vx.text2, modifier = Modifier.size(20.dp))
    }
}

@Composable
internal fun SectionBox(title: String, icon: ImageVector, trailing: @Composable () -> Unit = {}, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().clip(Vx.card).border(Vx.border, Vx.card).background(Vx.panel)) {
        Row(Modifier.fillMaxWidth().padding(start = 12.dp, end = 6.dp, top = 6.dp, bottom = 6.dp).height(32.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(icon, null, tint = Vx.text3, modifier = Modifier.size(15.dp))
            Text(title, color = Vx.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
            trailing()
        }
        Column(Modifier.fillMaxWidth().padding(start = 12.dp, end = 12.dp, bottom = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { content() }
    }
}

/** The bot list: what opens first in a workspace. */
@Composable
internal fun BotHome(state: BotsState, workspace: String, email: String, onOpen: (Bot) -> Unit, onNewBot: () -> Unit, onProviders: () -> Unit, onRoutines: () -> Unit, onWorkspaces: () -> Unit, onSignOut: () -> Unit) {
    var query by remember { mutableStateOf("") }
    var searching by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxSize().background(Vx.panel)) {
        Row(Modifier.fillMaxWidth().height(56.dp).padding(start = 14.dp, end = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(26.dp).clip(RoundedCornerShape(7.dp)).background(Vx.accent), contentAlignment = Alignment.Center) { Text(workspace.take(1).uppercase(), color = Color.White, fontWeight = FontWeight.Bold, fontSize = 13.sp) }
            Text(workspace, color = Vx.text, fontWeight = FontWeight.SemiBold, fontSize = 16.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(start = 10.dp).clickable(onClick = onWorkspaces))
            IconAction(Icons.Default.Search, "Search") { searching = !searching; if (!searching) query = "" }
            IconAction(Icons.Default.Edit, "New bot", onNewBot)
        }
        if (searching) OutlinedTextField(query, { query = it }, placeholder = { Text("Search bots") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp))
        Text("BOTS", color = Vx.text3, fontSize = 11.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(start = 16.dp, top = 8.dp, bottom = 4.dp))
        val shown = sortBots(state.bots).filter { query.isBlank() || it.name.contains(query, true) || it.title.contains(query, true) || it.description.contains(query, true) }
        LazyColumn(Modifier.weight(1f).fillMaxWidth().testTag("bot-list"), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 8.dp)) {
            if (state.loading && state.bots.isEmpty()) items(3) { SkeletonRow() }
            items(shown, key = { it.id }) { b -> BotRow(b, b.id == state.botId) { onOpen(b) } }
            if (!state.loading && shown.isEmpty()) item { Text(if (query.isBlank()) "No bots yet. Create one." else "No bots match.", color = Vx.text3, fontSize = 13.sp, modifier = Modifier.padding(12.dp)) }
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(Vx.line))
        Box {
            Row(Modifier.fillMaxWidth().clickable { menu = true }.padding(horizontal = 14.dp, vertical = 12.dp).testTag("profile"), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Box(Modifier.size(30.dp).clip(CircleShape).background(Vx.raised2), contentAlignment = Alignment.Center) { Text(email.take(1).uppercase().ifBlank { "U" }, color = Vx.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold) }
                Column(Modifier.weight(1f)) {
                    Text(email.ifBlank { "Signed in" }, color = Vx.text, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                        Box(Modifier.size(6.dp).clip(CircleShape).background(if (state.live) Vx.ok else Vx.warn))
                        Text(if (state.live) "Running" else "Connecting…", color = Vx.text3, fontSize = 11.sp)
                    }
                }
                Icon(Icons.Default.MoreVert, null, tint = Vx.text3, modifier = Modifier.size(18.dp))
            }
            DropdownMenu(menu, { menu = false }) {
                DropdownMenuItem(text = { Text("Providers") }, leadingIcon = { Icon(Icons.Default.Lock, null) }, onClick = { menu = false; onProviders() })
                DropdownMenuItem(text = { Text("Routines") }, leadingIcon = { Icon(Icons.Default.DateRange, null) }, onClick = { menu = false; onRoutines() })
                DropdownMenuItem(text = { Text("Workspaces") }, leadingIcon = { Icon(Icons.Default.Settings, null) }, onClick = { menu = false; onWorkspaces() })
                DropdownMenuItem(text = { Text("Sign out", color = Vx.bad) }, leadingIcon = { Icon(Icons.Default.ExitToApp, null, tint = Vx.bad) }, onClick = { menu = false; onSignOut() })
            }
        }
    }
}

@Composable
private fun SkeletonRow() {
    Row(Modifier.fillMaxWidth().padding(10.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(40.dp).clip(CircleShape).background(Vx.raised))
        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Box(Modifier.size(90.dp, 10.dp).clip(RoundedCornerShape(4.dp)).background(Vx.raised))
            Box(Modifier.size(170.dp, 9.dp).clip(RoundedCornerShape(4.dp)).background(Vx.raised))
        }
    }
}

@Composable
private fun BotRow(b: Bot, selected: Boolean, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clip(Vx.card).background(if (selected) Vx.selected else Color.Transparent).clickable(onClick = onClick).padding(horizontal = 10.dp, vertical = 10.dp).testTag("bot-${b.name}"), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
        BotFace(b, 40.dp, busy = b.busy)
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(b.name, color = Vx.text, fontWeight = FontWeight.SemiBold, fontSize = 15.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                    if (b.main) Text("Main", color = Vx.accentHi, fontSize = 10.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.testTag("main-${b.name}").clip(CircleShape).background(Vx.accentSoft).padding(horizontal = 7.dp, vertical = 1.dp))
                    else if (b.pinned) Icon(Icons.Default.Star, "Pinned", tint = Vx.text3, modifier = Modifier.size(12.dp))
                }
                Text(timeAgo(b.lastAt), color = Vx.text3, fontSize = 11.sp)
            }
            Text(if (b.busy) "Working…" else b.preview.ifBlank { b.title.ifBlank { "No messages yet" } }, color = if (b.busy) Vx.warn else Vx.text2, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** The bot's details: threads, routines, computer and settings; files in Library. */
@Composable
internal fun BotInfoPanel(state: BotsState, onBack: () -> Unit, onEdit: () -> Unit, onThread: (String) -> Unit, onNewThread: () -> Unit, onRoutines: () -> Unit, onProviders: () -> Unit, onTab: (Int) -> Unit, onNotice: (String) -> Unit) {
    val bot = state.bot ?: return
    val scope = rememberCoroutineScope()
    var tab by remember { mutableStateOf(0) }
    var routines by remember { mutableStateOf(listOf<Routine>()) }
    var files by remember { mutableStateOf(listOf<BotsClient.LibraryFile>()) }
    var perm by remember(bot.id) { mutableStateOf(bot.settings.permissionMode) }
    var mode by remember(bot.id) { mutableStateOf(bot.settings.mode) }
    var model by remember(bot.id) { mutableStateOf(bot.settings.model) }
    LaunchedEffect(bot.id) {
        runCatching { routines = withContext(Dispatchers.IO) { state.client.routines() }.filter { it.botId == bot.id } }
        runCatching { files = withContext(Dispatchers.IO) { state.client.files(bot.id) } }
    }
    val models = state.session.options.firstOrNull { it.id == "model" }?.choices.orEmpty()
    Column(Modifier.fillMaxSize().background(Vx.panel)) {
        PanelTopBar("", onBack)
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(6.dp)) {
                BotFace(bot, 72.dp)
                Text(bot.name, color = Vx.text, fontSize = 19.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(top = 6.dp))
                Text(bot.title.ifBlank { if (bot.main) "Main bot" else "Bot" }, color = Vx.text2, fontSize = 13.sp)
                if (bot.description.isNotBlank()) Text(bot.description, color = Vx.text3, fontSize = 12.5.sp, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(top = 6.dp)) {
                    QuietButton("Edit", Icons.Default.Edit, onClick = onEdit)
                    QuietButton("New thread", Icons.Default.Add, onClick = onNewThread)
                }
            }
            Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(9.dp)).background(Vx.bg).border(Vx.border, RoundedCornerShape(9.dp)).padding(3.dp)) {
                listOf("Activity", "Library").forEachIndexed { i, t ->
                    Text(t, color = if (tab == i) Vx.text else Vx.text2, fontSize = 13.sp, fontWeight = FontWeight.Medium,
                        modifier = Modifier.weight(1f).clip(RoundedCornerShape(6.dp)).background(if (tab == i) Vx.raised2 else Color.Transparent).clickable { tab = i }.padding(vertical = 7.dp), textAlign = androidx.compose.ui.text.style.TextAlign.Center)
                }
            }
            if (tab == 0) {
                SectionBox("Threads", Icons.Default.Edit, trailing = { Text("${bot.threads.size}", color = Vx.text3, fontSize = 12.sp, modifier = Modifier.padding(end = 6.dp)) }) {
                    bot.threads.take(30).forEach { t ->
                        Row(Modifier.fillMaxWidth().clip(Vx.small).background(if (t.id == state.threadId) Vx.selected else Color.Transparent).clickable { onThread(t.id) }.padding(horizontal = 8.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            if (t.busy) CircularProgressIndicator(Modifier.size(11.dp), strokeWidth = 1.5.dp, color = Vx.warn)
                            Text(t.title, color = Vx.text, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                            Text(timeAgo(t.updatedAt), color = Vx.text3, fontSize = 11.sp)
                        }
                    }
                }
                SectionBox("Routines", Icons.Default.DateRange, trailing = { IconAction(Icons.Default.Add, "Add routine", onRoutines) }) {
                    if (routines.isEmpty()) Text("No routines. Schedule a prompt to run daily or every few minutes.", color = Vx.text3, fontSize = 12.sp)
                    routines.forEach { r -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Box(Modifier.size(6.dp).clip(CircleShape).background(if (r.enabled) Vx.ok else Vx.text3))
                        Text(r.name, color = Vx.text, fontSize = 13.sp, modifier = Modifier.weight(1f))
                        Text(scheduleLabel(r), color = Vx.text3, fontSize = 11.sp)
                    } }
                }
                SectionBox("Computer", Icons.Default.Settings) {
                    InfoLine("Workspace", if (state.live) "Running" else "Connecting…", if (state.live) Vx.ok else Vx.warn)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        QuietButton("Desktop", modifier = Modifier.weight(1f)) { onTab(3) }
                        QuietButton("Terminal", modifier = Modifier.weight(1f)) { onTab(1) }
                        QuietButton("Preview", modifier = Modifier.weight(1f)) { onTab(2) }
                    }
                }
                SectionBox("Settings", Icons.Default.Settings) {
                    Picker("Model", model, listOf(ConfigChoice("", "Workspace default")) + models.ifEmpty { if (model.isNotBlank()) listOf(ConfigChoice(model, model)) else emptyList() }) { model = it }
                    Picker("Permissions", perm, listOf(ConfigChoice("ask", "Ask before edits and commands"), ConfigChoice("auto", "Auto: approve edits"), ConfigChoice("always-approve", "Always approve"))) { perm = it }
                    Picker("New threads start in", mode, listOf(ConfigChoice("default", "Default mode"), ConfigChoice("plan", "Plan mode"), ConfigChoice("ask", "Ask mode"))) { mode = it }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        QuietButton("Providers", Icons.Default.Lock, onClick = onProviders)
                        Spacer(Modifier.weight(1f))
                        PrimaryButton("Save") {
                            scope.launch {
                                try {
                                    val b = withContext(Dispatchers.IO) { state.client.updateBot(bot.id, JSONObject().put("model", model).put("permissionMode", perm).put("mode", mode)) }
                                    state.mergeBot(b)
                                    val tid = state.threadId
                                    if (tid.isNotBlank() && state.session.open) withContext(Dispatchers.IO) {
                                        if (model.isNotBlank()) runCatching { state.client.setOption(tid, "model", model) }
                                        runCatching { state.client.setOption(tid, "permission_mode", perm) }
                                    }
                                    onNotice("Settings saved")
                                } catch (e: CancellationException) { throw e } catch (e: Exception) { onNotice(e.message ?: "Couldn't save") }
                            }
                        }
                    }
                }
            } else {
                SectionBox("Files", Icons.Default.Edit) {
                    if (files.isEmpty()) Text("No files yet. Files you or the bot add to its library show here.", color = Vx.text3, fontSize = 12.sp)
                    files.forEach { f -> Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(f.name, color = Vx.text, fontSize = 13.sp, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(formatSize(f.size), color = Vx.text3, fontSize = 11.sp)
                    } }
                }
                val changed = state.thread().filter { it.kind == "changes" }.flatMap { it.changes }.distinctBy { it.path }
                SectionBox("Changed in this thread", Icons.Default.Edit) {
                    if (changed.isEmpty()) Text("Nothing changed yet.", color = Vx.text3, fontSize = 12.sp)
                    changed.forEach { c -> Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(c.path, color = Vx.text2, fontSize = 12.sp, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Tag(c.kind)
                    } }
                }
            }
        }
    }
}

internal fun formatSize(n: Long) = when { n < 1024 -> "$n B"; n < 1024 * 1024 -> "${n / 1024} KB"; else -> "${n / 1024 / 1024} MB" }
internal fun scheduleLabel(r: Routine) = when (r.type) { "daily" -> "Daily ${r.time}"; "interval" -> "Every ${r.everyMinutes} min"; else -> "Once" }

@Composable
private fun InfoLine(label: String, value: String, dot: Color? = null) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(label, color = Vx.text2, fontSize = 13.sp, modifier = Modifier.weight(1f))
        if (dot != null) Box(Modifier.size(6.dp).clip(CircleShape).background(dot))
        Text(value, color = Vx.text, fontSize = 13.sp)
    }
}

@Composable
internal fun Picker(label: String, value: String, choices: List<ConfigChoice>, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(label, color = Vx.text2, fontSize = 12.sp)
        Box {
            Row(Modifier.fillMaxWidth().clip(Vx.small).border(1.dp, Vx.line2, Vx.small).background(Vx.bg).clickable { open = true }.padding(horizontal = 10.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(choices.firstOrNull { it.value == value }?.name ?: value.ifBlank { "—" }, color = Vx.text, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                Icon(Icons.Default.ArrowDropDown, null, tint = Vx.text3)
            }
            DropdownMenu(open, { open = false }) {
                choices.forEach { c -> DropdownMenuItem(text = { Text(c.name, fontSize = 13.sp) }, onClick = { open = false; onPick(c.value) }, trailingIcon = { if (c.value == value) Icon(Icons.Default.Check, null, modifier = Modifier.size(16.dp)) }) }
            }
        }
    }
}

/** The CLI's own provider setup, over the host: keys, sign-in, base URL and model. */
@Composable
internal fun ProvidersPanel(client: BotsClient, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    var status by remember { mutableStateOf<ProviderStatus?>(null) }
    var error by remember { mutableStateOf("") }
    var selected by remember { mutableStateOf<Provider?>(null) }
    suspend fun load() { status = withContext(Dispatchers.IO) { client.providers() } }
    LaunchedEffect(Unit) { try { load() } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message ?: "Couldn't load providers" } }
    Column(Modifier.fillMaxSize().background(Vx.panel)) {
        val sel = selected
        PanelTopBar(sel?.name ?: "Providers", onBack = { if (sel != null) selected = null else onBack() })
        val s = status
        when {
            s == null && error.isBlank() -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp, color = Vx.text3) }
            s == null -> Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("Couldn't load providers", color = Vx.text, fontWeight = FontWeight.SemiBold); Text(error, color = Vx.text2, fontSize = 13.sp)
                QuietButton("Retry", Icons.Default.Refresh) { error = ""; scope.launch { try { load() } catch (e: Exception) { error = e.message ?: "Failed" } } }
            }
            sel == null -> LazyColumn(Modifier.fillMaxSize().testTag("provider-list"), contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp)) {
                item {
                    Text("In use: " + (s.providers.firstOrNull { it.id == s.providerId }?.name ?: if (s.providerId == "custom") "Custom endpoint · ${s.baseUrl.removePrefix("https://")}" else "none") + if (s.modelId.isNotBlank()) " · ${s.modelId}" else "", color = Vx.text2, fontSize = 13.sp, modifier = Modifier.padding(start = 4.dp, bottom = 10.dp))
                }
                val groups = listOf(
                    "SIGN IN" to s.providers.filter { "browser" in it.auth || "device" in it.auth || "oauth_device" in it.auth },
                    "API KEY" to s.providers.filter { "api_key" in it.auth && "none" !in it.auth && "browser" !in it.auth && "device" !in it.auth },
                    "LOCAL" to s.providers.filter { "none" in it.auth },
                )
                groups.forEach { (label, items) ->
                    if (items.isNotEmpty()) {
                        item { Text(label, color = Vx.text3, fontSize = 11.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.padding(start = 4.dp, top = 10.dp, bottom = 4.dp)) }
                        items(items, key = { it.id }) { p ->
                            Row(Modifier.fillMaxWidth().clip(Vx.small).clickable { selected = p }.padding(horizontal = 8.dp, vertical = 11.dp).testTag("provider-${p.id}"), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Column(Modifier.weight(1f)) {
                                    Text(p.name, color = Vx.text, fontSize = 14.sp)
                                    if (p.description.isNotBlank()) Text(p.description, color = Vx.text3, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                }
                                if (s.providerId == p.id) Tag("In use", Vx.ok) else if (p.signedIn) Tag("Signed in", Vx.ok)
                            }
                        }
                    }
                }
            }
            else -> ProviderDetail(client, s, sel, context, onSaved = { scope.launch { runCatching { load() }; selected = null } })
        }
    }
}

@Composable
private fun ProviderDetail(client: BotsClient, s: ProviderStatus, p: Provider, context: android.content.Context, onSaved: () -> Unit) {
    val scope = rememberCoroutineScope()
    var key by remember(p.id) { mutableStateOf("") }
    var base by remember(p.id) { mutableStateOf(if (s.providerId == p.id && s.baseUrl.isNotBlank()) s.baseUrl else p.baseUrl) }
    var model by remember(p.id) { mutableStateOf(if (s.providerId == p.id) s.modelId else p.defaultModel) }
    var models by remember(p.id) { mutableStateOf(listOf<ConfigChoice>()) }
    var note by remember(p.id) { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var login by remember(p.id) { mutableStateOf<LoginStart?>(null) }
    val signIn = "browser" in p.auth || "device" in p.auth || "oauth_device" in p.auth
    val keyField = "api_key" in p.auth && !signIn
    fun act(block: suspend () -> Unit) { busy = true; note = ""; scope.launch { try { block() } catch (e: CancellationException) { throw e } catch (e: Exception) { note = e.message ?: "Request failed" } finally { busy = false } } }
    LaunchedEffect(login?.loginId) {
        val l = login ?: return@LaunchedEffect
        while (isActive) {
            delay(maxOf(2, l.interval) * 1000L)
            val (st, msg) = runCatching { withContext(Dispatchers.IO) { client.pollLogin(l.loginId) } }.getOrElse { "pending" to "" }
            if (st == "done") { login = null; note = "Signed in. Load models, then use this provider."; break }
            if (st != "pending") { login = null; note = msg.ifBlank { "Sign-in $st" }; break }
        }
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        if (p.description.isNotBlank()) Text(p.description, color = Vx.text2, fontSize = 13.sp)
        if (signIn) SectionBox("Sign in", Icons.Default.Lock) {
            val l = login
            if (l == null) Row(verticalAlignment = Alignment.CenterVertically) {
                Tag(if (p.signedIn) "Signed in" else "Not signed in", if (p.signedIn) Vx.ok else Vx.text3)
                Spacer(Modifier.weight(1f))
                PrimaryButton(if (p.signedIn) "Sign in again" else if ("browser" in p.auth) "Sign in with browser" else "Sign in with a code", enabled = !busy) {
                    act { val started = withContext(Dispatchers.IO) { client.startLogin(p.id, if ("browser" in p.auth) "browser" else "device") }; login = started; if (started.url.isNotBlank()) runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(started.url))) } }
                }
            } else {
                if (l.userCode.isNotBlank()) Text(l.userCode, color = Vx.text, fontFamily = FontFamily.Monospace, fontSize = 22.sp, fontWeight = FontWeight.SemiBold)
                Text("Finish signing in in the browser. This updates by itself.", color = Vx.text2, fontSize = 13.sp)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    QuietButton("Open link") { runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(l.url))) } }
                    QuietButton("Cancel") { scope.launch { withContext(Dispatchers.IO) { client.cancelLogin(l.loginId) }; login = null } }
                }
            }
        }
        if (keyField) Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            OutlinedTextField(key, { key = it }, label = { Text("API key") }, placeholder = { Text(if (s.providerId == p.id && s.hasApiKey) "Saved — paste a new key to replace it" else "Paste your key") }, singleLine = true,
                visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password), modifier = Modifier.fillMaxWidth().testTag("provider-key"))
            Text("Stored in the workspace's Supercharge config. It is never shown again.", color = Vx.text3, fontSize = 11.sp)
        }
        OutlinedTextField(base, { base = it }, label = { Text("Base URL") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        Picker("Model", model, (if (models.isEmpty() && model.isNotBlank()) listOf(ConfigChoice(model, model)) else models)) { model = it }
        OutlinedTextField(model, { model = it }, label = { Text("Or type a model id") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            QuietButton(if (busy) "Loading…" else "Load models", Icons.Default.Refresh, enabled = !busy) {
                act { models = withContext(Dispatchers.IO) { client.models(p.id, key.ifBlank { null }, base.ifBlank { null }) }; if (models.isEmpty()) note = "The provider returned no models; type an id." }
            }
            Spacer(Modifier.weight(1f))
            PrimaryButton("Use this provider", enabled = !busy) {
                act { withContext(Dispatchers.IO) { client.saveProvider(p.id, model, key.ifBlank { null }, base.ifBlank { null }) }; key = ""; onSaved() }
            }
        }
        if (note.isNotBlank()) Text(note, color = if (note.startsWith("Signed in")) Vx.ok else Vx.bad, fontSize = 13.sp)
    }
}

/** Scheduled prompts. They run while the workspace is awake. */
@Composable
internal fun RoutinesPanel(state: BotsState, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val client = state.client
    var list by remember { mutableStateOf(listOf<Routine>()) }
    var note by remember { mutableStateOf("") }
    var adding by remember { mutableStateOf(false) }
    suspend fun load() { list = withContext(Dispatchers.IO) { client.routines() } }
    fun act(block: suspend () -> Unit) { scope.launch { note = try { block(); "" } catch (e: CancellationException) { throw e } catch (e: Exception) { e.message ?: "Request failed" } } }
    LaunchedEffect(Unit) { act { load() } }
    Column(Modifier.fillMaxSize().background(Vx.panel)) {
        PanelTopBar("Routines", onBack) { IconAction(Icons.Default.Add, "New routine") { adding = true } }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            item { Text("Routines send a prompt to a bot on a schedule while the workspace is awake.", color = Vx.text3, fontSize = 12.sp, modifier = Modifier.padding(4.dp)) }
            if (list.isEmpty()) item { Text("No routines yet.", color = Vx.text2, fontSize = 13.sp, modifier = Modifier.padding(4.dp)) }
            items(list, key = { it.id }) { r ->
                val bot = state.bots.firstOrNull { it.id == r.botId }
                Column(Modifier.fillMaxWidth().clip(Vx.card).border(Vx.border, Vx.card).padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(r.name, color = Vx.text, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
                            Text("${bot?.name ?: "Bot"} · ${scheduleLabel(r)}" + if (r.nextRunAt > 0 && r.enabled) " · next ${clockTime(r.nextRunAt)}" else "", color = Vx.text3, fontSize = 12.sp)
                        }
                        Switch(r.enabled, { on -> act { withContext(Dispatchers.IO) { client.setRoutineEnabled(r.id, on) }; load() } }, colors = SwitchDefaults.colors(checkedTrackColor = Vx.accent))
                    }
                    Text(r.prompt, color = Vx.text2, fontSize = 13.sp, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        QuietButton("Run now", Icons.Default.PlayArrow) { act { withContext(Dispatchers.IO) { client.runRoutine(r.id) }; note = "Started — the reply lands in its thread" } }
                        QuietButton("Delete", Icons.Default.Delete, danger = true) { act { withContext(Dispatchers.IO) { client.deleteRoutine(r.id) }; load() } }
                    }
                }
            }
            if (note.isNotBlank()) item { Text(note, color = Vx.text2, fontSize = 13.sp) }
        }
    }
    if (adding) {
        var name by remember { mutableStateOf("") }
        var prompt by remember { mutableStateOf("") }
        var botId by remember { mutableStateOf(state.botId.ifBlank { state.bots.firstOrNull()?.id.orEmpty() }) }
        var type by remember { mutableStateOf("daily") }
        var time by remember { mutableStateOf("09:00") }
        var every by remember { mutableStateOf("60") }
        AlertDialog(
            onDismissRequest = { adding = false }, containerColor = Vx.raised,
            title = { Text("New routine", fontSize = 17.sp, fontWeight = FontWeight.SemiBold) },
            text = {
                Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    OutlinedTextField(prompt, { prompt = it }, label = { Text("Prompt") }, minLines = 2, modifier = Modifier.fillMaxWidth())
                    Picker("Bot", botId, state.bots.map { ConfigChoice(it.id, it.name) }) { botId = it }
                    Picker("Schedule", type, listOf(ConfigChoice("daily", "Every day"), ConfigChoice("interval", "Every few minutes"))) { type = it }
                    if (type == "daily") OutlinedTextField(time, { time = it }, label = { Text("Time (HH:MM)") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    else OutlinedTextField(every, { every = it.filter(Char::isDigit) }, label = { Text("Minutes (5 or more)") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                }
            },
            confirmButton = { PrimaryButton("Create", enabled = name.isNotBlank() && prompt.isNotBlank() && botId.isNotBlank()) {
                adding = false
                act { withContext(Dispatchers.IO) { client.createRoutine(name.trim(), prompt.trim(), botId, type, time, every.toIntOrNull() ?: 60) }; load() }
            } },
            dismissButton = { TextButton(onClick = { adding = false }) { Text("Cancel", color = Vx.text2) } },
        )
    }
}
