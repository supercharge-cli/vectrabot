package com.openmausbot.companion.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Create
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

private val modeLabels = mapOf("default" to "Default", "plan" to "Plan", "ask" to "Ask")
private val permLabels = mapOf("ask" to "Ask before edits", "auto" to "Auto", "always-approve" to "Always approve")

/** The chat with one bot's thread: messages, agent requests and the composer. */
@Composable
internal fun BotChatPane(state: BotsState, onProviders: () -> Unit, onNotice: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    val client = state.client
    val tid = state.threadId
    val bot = state.bot ?: return
    val list = rememberLazyListState()
    val messages = state.thread()
    val session = state.session
    var loadingOlder by remember { mutableStateOf(false) }
    fun act(block: suspend () -> Unit) = scope.launch {
        try { block() } catch (e: CancellationException) { throw e } catch (e: Exception) { onNotice(e.message ?: "Request failed") }
    }
    LaunchedEffect(tid) {
        if (tid.isBlank()) return@LaunchedEffect
        if (state.messages[tid] == null) runCatching { state.loaded(tid, withContext(Dispatchers.IO) { client.messages(tid) }) }.onFailure { onNotice(it.message ?: "Couldn't load the thread") }
        runCatching { val s = withContext(Dispatchers.IO) { client.open(tid) }; state.sessions[tid] = s }
    }
    LaunchedEffect(tid, messages.size) { if (messages.isNotEmpty()) list.animateScrollToItem(messages.size) }

    Column(Modifier.fillMaxSize()) {
        LazyColumn(state = list, modifier = Modifier.weight(1f).fillMaxWidth().testTag("chat-log"), contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 14.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            if (state.hasMore[tid] == true) item {
                Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    QuietButton(if (loadingOlder) "Loading…" else "Load earlier messages") {
                        if (!loadingOlder) { loadingOlder = true; act { try { state.loaded(tid, withContext(Dispatchers.IO) { client.messages(tid, before = messages.firstOrNull()?.id) }, older = true) } finally { loadingOlder = false } } }
                    }
                }
            }
            if (messages.isEmpty()) item { EmptyChat(bot) }
            itemsIndexed(messages) { i, m ->
                val prev = messages.getOrNull(i - 1)
                val showLabel = !m.fromUser && (prev == null || prev.fromUser)
                MessageView(m, bot, showLabel, last = i == messages.lastIndex,
                    onRespond = { req, answer -> act { withContext(Dispatchers.IO) { client.respond(tid, req.id, answer) } } },
                    onRetry = { text -> act { withContext(Dispatchers.IO) { client.send(bot.id, tid, text) } } },
                    onProviders = onProviders,
                    lastUserText = messages.lastOrNull { it.fromUser }?.text,
                )
            }
            if (session.busy) item { WorkingRow(session.queued) }
        }
        Composer(state, session, onSend = { text -> act { val q = withContext(Dispatchers.IO) { client.send(bot.id, tid, text) }; if (q) onNotice("Queued — it runs when this turn ends") } },
            onStop = { act { withContext(Dispatchers.IO) { client.interrupt(bot.id, tid) } } },
            onMode = { mode -> act { withContext(Dispatchers.IO) { client.setMode(tid, mode) }; state.sessions[tid] = state.session.copy(mode = mode) } },
            onOption = { id, value -> act { withContext(Dispatchers.IO) { client.setOption(tid, id, value) }?.let { state.sessions[tid] = it } } },
        )
    }
}

private fun <T> androidx.compose.foundation.lazy.LazyListScope.itemsIndexed(items: List<T>, content: @Composable (Int, T) -> Unit) =
    items(items.size) { i -> content(i, items[i]) }

@Composable
private fun EmptyChat(bot: Bot) {
    Column(Modifier.fillMaxWidth().padding(top = 64.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        BotFace(bot, 56.dp)
        Text(bot.name, fontWeight = FontWeight.SemiBold, fontSize = 17.sp, color = Vx.text)
        Text(bot.description.ifBlank { bot.title.ifBlank { "Ask it to build, fix or run something in this workspace." } }, color = Vx.text2, fontSize = 14.sp)
    }
}

@Composable
private fun WorkingRow(queued: Int) {
    var seconds by remember { mutableStateOf(0) }
    LaunchedEffect(Unit) { while (true) { delay(1000); seconds++ } }
    Row(Modifier.padding(start = 4.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        CircularProgressIndicator(Modifier.size(12.dp), strokeWidth = 1.5.dp, color = Vx.text3)
        Text("Working · ${seconds}s" + if (queued > 0) " · $queued queued" else "", color = Vx.text3, fontSize = 12.sp)
    }
}

@Composable
private fun MessageView(
    m: BotMessage, bot: Bot, showLabel: Boolean, last: Boolean,
    onRespond: (AgentRequest, JSONObject) -> Unit, onRetry: (String) -> Unit, onProviders: () -> Unit, lastUserText: String?,
) {
    if (m.fromUser) {
        Column(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalAlignment = Alignment.End) {
            Box(Modifier.widthIn(max = 300.dp).clip(RoundedCornerShape(18.dp, 18.dp, 6.dp, 18.dp)).background(Vx.accent).padding(horizontal = 14.dp, vertical = 10.dp)) {
                SelectionContainer { Text(m.text, color = Color.White, fontSize = 15.sp, lineHeight = 21.sp) }
            }
            Text(clockTime(m.at), color = Vx.text3, fontSize = 11.sp, modifier = Modifier.padding(top = 3.dp, end = 4.dp))
        }
        return
    }
    Column(Modifier.fillMaxWidth()) {
        if (showLabel) Row(Modifier.padding(top = 8.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            BotFace(bot, 18.dp)
            Text(bot.name, color = Vx.text2, fontSize = 12.sp, fontWeight = FontWeight.Medium)
            Text(clockTime(m.at), color = Vx.text3, fontSize = 11.sp)
        }
        when (m.kind) {
            "text" -> Box(Modifier.widthIn(max = 340.dp).clip(RoundedCornerShape(6.dp, 18.dp, 18.dp, 18.dp)).background(Vx.raised).padding(horizontal = 14.dp, vertical = 10.dp)) {
                SelectionContainer { MarkdownText(m.text) }
            }
            "thought" -> Collapsible("Thinking", Icons.Default.Info) { Text(m.text, color = Vx.text2, fontSize = 13.sp) }
            "tool" -> m.tool?.let { ToolCardView(it) }
            "todo" -> PlanStepsCard(m.entries)
            "changes" -> ChangesCard(m.changes)
            "request" -> m.request?.let { RequestCardView(it, onRespond) }
            "notice" -> NoticeView(m, last, onRetry = { lastUserText?.let(onRetry) }, onProviders = onProviders)
            else -> Text(m.text, color = Vx.text2)
        }
    }
}

@Composable
private fun NoticeView(m: BotMessage, last: Boolean, onRetry: () -> Unit, onProviders: () -> Unit) {
    if (!m.error) {
        Text(m.text, color = Vx.text3, fontSize = 12.sp, modifier = Modifier.padding(start = 4.dp, top = 2.dp))
        return
    }
    Column(Modifier.fillMaxWidth().clip(Vx.card).background(Vx.badSoft).border(1.dp, Vx.bad.copy(alpha = 0.3f), Vx.card).padding(12.dp).testTag("error-notice"), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(Icons.Default.Warning, null, tint = Vx.bad, modifier = Modifier.size(16.dp))
            Text(m.text, color = Vx.text, fontSize = 13.sp, lineHeight = 18.sp)
        }
        if (last) Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (m.action == "providers" || m.action == "model") PrimaryButton(errorActionLabel(m.action) ?: "Fix", onClick = onProviders)
            QuietButton("Retry", Icons.Default.Refresh, onClick = onRetry)
        }
    }
}

@Composable
private fun Collapsible(title: String, icon: ImageVector, initiallyOpen: Boolean = false, trailing: @Composable () -> Unit = {}, body: @Composable () -> Unit) {
    var open by rememberSaveable(title) { mutableStateOf(initiallyOpen) }
    Column(Modifier.fillMaxWidth().clip(Vx.card).border(Vx.border, Vx.card).background(Vx.panel).animateContentSize()) {
        Row(Modifier.fillMaxWidth().clickable { open = !open }.padding(horizontal = 12.dp, vertical = 9.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(icon, null, tint = Vx.text3, modifier = Modifier.size(15.dp))
            Text(title, color = Vx.text, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            trailing()
            Icon(if (open) Icons.Default.KeyboardArrowDown else Icons.Default.KeyboardArrowRight, null, tint = Vx.text3, modifier = Modifier.size(16.dp))
        }
        if (open) Column(Modifier.fillMaxWidth().padding(start = 12.dp, end = 12.dp, bottom = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) { body() }
    }
}

private fun toolIcon(kind: String): ImageVector = when (kind) {
    "edit", "delete", "move" -> Icons.Default.Edit
    "read", "search" -> Icons.Default.Search
    "execute" -> Icons.Default.PlayArrow
    "think" -> Icons.Default.Info
    else -> Icons.Default.Build
}

@Composable
internal fun ToolCardView(t: ToolCall) {
    val title = t.title.replace(Regex("/home/user/\\.supercharge/\\S*/([^/\\s]+)"), "$1").replace("/home/user/workspace/", "")
    val running = t.status == "pending" || t.status == "in_progress"
    Collapsible(title, toolIcon(t.kind), initiallyOpen = t.diffs.isNotEmpty(), trailing = {
        if (t.diffs.isNotEmpty()) Tag("${t.diffs.size} file" + if (t.diffs.size > 1) "s" else "")
        when {
            running -> CircularProgressIndicator(Modifier.size(12.dp), strokeWidth = 1.5.dp, color = Vx.text3)
            t.status == "failed" -> Icon(Icons.Default.Close, null, tint = Vx.bad, modifier = Modifier.size(14.dp))
            else -> Icon(Icons.Default.Check, null, tint = Vx.ok, modifier = Modifier.size(14.dp))
        }
    }) {
        t.diffs.forEach { DiffView(it) }
        val out = if (t.output.isBlank()) "" else toolSummary(t.output)
        if (out.isNotBlank() && (t.diffs.isEmpty() || t.status == "failed")) Mono(out, if (t.status == "failed") Vx.bad else Vx.text2)
        if (t.diffs.isEmpty() && out.isBlank()) Text(if (running) "Running…" else "No output.", color = Vx.text3, fontSize = 12.sp)
    }
}

@Composable
private fun DiffView(d: ToolDiff) {
    val lines = remember(d) { lineDiff(d.oldText, d.newText) }
    val add = lines.count { it.first == '+' }; val del = lines.count { it.first == '-' }
    Column(Modifier.fillMaxWidth().clip(Vx.small).border(Vx.border, Vx.small)) {
        Row(Modifier.fillMaxWidth().background(Vx.raised).padding(horizontal = 10.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(shortPath(d.path), color = Vx.text2, fontSize = 12.sp, fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            Text("+$add", color = Vx.ok, fontSize = 12.sp, fontFamily = FontFamily.Monospace)
            Text("−$del", color = Vx.bad, fontSize = 12.sp, fontFamily = FontFamily.Monospace)
        }
        Column(Modifier.heightIn(max = 260.dp).verticalScroll(rememberScrollState()).horizontalScroll(rememberScrollState())) {
            lines.take(400).forEach { (k, text) ->
                Text("$k ${text}", fontFamily = FontFamily.Monospace, fontSize = 12.sp, lineHeight = 17.sp, softWrap = false,
                    color = when (k) { '+' -> Vx.text; '-' -> Vx.text2; else -> Vx.text3 },
                    modifier = Modifier.background(when (k) { '+' -> Vx.add; '-' -> Vx.del; else -> Color.Transparent }).padding(horizontal = 10.dp))
            }
        }
    }
}

@Composable
private fun Mono(text: String, color: Color = Vx.text2) {
    Box(Modifier.fillMaxWidth().clip(Vx.small).background(Vx.bg).border(Vx.border, Vx.small).heightIn(max = 220.dp).verticalScroll(rememberScrollState()).padding(10.dp)) {
        SelectionContainer { Text(text.take(6000), fontFamily = FontFamily.Monospace, fontSize = 12.sp, lineHeight = 17.sp, color = color) }
    }
}

@Composable
private fun PlanStepsCard(entries: List<PlanStep>) {
    val done = entries.count { it.status == "completed" }
    Collapsible("Plan · $done of ${entries.size} done", Icons.Default.List, initiallyOpen = true) {
        entries.forEach { e ->
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                when (e.status) {
                    "completed" -> Icon(Icons.Default.CheckCircle, null, tint = Vx.ok, modifier = Modifier.size(14.dp))
                    "in_progress" -> CircularProgressIndicator(Modifier.size(12.dp), strokeWidth = 1.5.dp, color = Vx.warn)
                    else -> Box(Modifier.size(12.dp).border(1.dp, Vx.text3, CircleShape))
                }
                Text(e.content, color = if (e.status == "completed") Vx.text3 else Vx.text, fontSize = 13.sp)
            }
        }
    }
}

@Composable
private fun ChangesCard(changes: List<FileChange>) {
    Collapsible("Changed ${changes.size} file" + if (changes.size != 1) "s" else "", Icons.Default.Create, initiallyOpen = true) {
        changes.forEach { c ->
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(c.path, color = Vx.text2, fontFamily = FontFamily.Monospace, fontSize = 12.sp, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Tag(c.kind, when (c.kind) { "added" -> Vx.ok; "deleted" -> Vx.bad; else -> Vx.text3 })
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun RequestCardView(r: AgentRequest, onRespond: (AgentRequest, JSONObject) -> Unit) {
    val pending = r.state == "pending"
    var sent by remember(r.id) { mutableStateOf(false) }
    val enabled = pending && !sent
    fun send(answer: JSONObject) { sent = true; onRespond(r, answer) }
    val accent = if (pending) Vx.accent.copy(alpha = 0.5f) else Vx.line
    Column(Modifier.fillMaxWidth().clip(Vx.card).border(1.dp, accent, Vx.card).background(Vx.panel).padding(12.dp).testTag("request-${r.type}"), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(when (r.type) { "permission" -> Icons.Default.Lock; "plan" -> Icons.Default.List; else -> Icons.Default.Info }, null, tint = Vx.text2, modifier = Modifier.size(15.dp))
            Text(when (r.type) { "permission" -> "Permission needed"; "plan" -> "Plan ready for review"; else -> "Questions" }, color = Vx.text, fontWeight = FontWeight.SemiBold, fontSize = 13.sp, modifier = Modifier.weight(1f))
            if (pending) Tag("Waiting for you", Vx.warn)
        }
        when (r.type) {
            "permission" -> {
                if (r.title.isNotBlank()) Text(r.title.replace("/home/user/workspace/", ""), color = Vx.text2, fontSize = 13.sp, fontFamily = FontFamily.Monospace)
                if (enabled) FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    r.options.forEach { o ->
                        val allow = o.kind.startsWith("allow")
                        if (allow && o.kind == "allow_once") PrimaryButton(o.name) { send(Answers.permission(o.id)) }
                        else QuietButton(o.name, danger = o.kind.startsWith("reject")) { send(Answers.permission(o.id)) }
                    }
                }
            }
            "plan" -> {
                Box(Modifier.fillMaxWidth().clip(Vx.small).background(Vx.bg).border(Vx.border, Vx.small).padding(12.dp)) { MarkdownText(r.plan) }
                if (enabled) {
                    var revising by remember { mutableStateOf(false) }
                    var feedback by remember { mutableStateOf("") }
                    if (revising) OutlinedTextField(feedback, { feedback = it }, placeholder = { Text("What should change?") }, modifier = Modifier.fillMaxWidth(), minLines = 2)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        PrimaryButton("Approve and run") { send(Answers.planApproved()) }
                        QuietButton(if (revising) "Send feedback" else "Revise") { if (revising && feedback.isNotBlank()) send(Answers.planRevise(feedback.trim())) else revising = true }
                        Spacer(Modifier.weight(1f))
                        QuietButton("Reject", danger = true) { send(Answers.planRejected()) }
                    }
                }
            }
            else -> {
                val picked = remember(r.id) { mutableStateMapOf<String, List<String>>() }
                r.questions.forEach { q ->
                    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        if (q.header.isNotBlank()) Text(q.header.uppercase(), color = Vx.text3, fontSize = 11.sp, fontWeight = FontWeight.Medium)
                        Text(q.question, color = Vx.text, fontSize = 14.sp)
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            q.options.forEach { o ->
                                val on = picked[q.question]?.contains(o.label) == true
                                ChoiceChip(o.label, on, enabled) {
                                    val cur = picked[q.question].orEmpty()
                                    picked[q.question] = if (q.multiSelect) (if (on) cur - o.label else cur + o.label) else listOf(o.label)
                                }
                            }
                        }
                    }
                }
                if (enabled) Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    PrimaryButton("Submit", enabled = picked.values.any { it.isNotEmpty() }) { send(Answers.questions("accepted", picked.toMap())) }
                    if (r.mode == "plan") QuietButton("Chat about this") { send(Answers.questions("chat_about_this", picked.toMap())) }
                    Spacer(Modifier.weight(1f))
                    if (r.mode == "plan") QuietButton("Skip") { send(Answers.questions("skip_interview", picked.toMap())) }
                    else QuietButton("Dismiss") { send(Answers.cancelled()) }
                }
            }
        }
        if (!pending) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Icon(if (r.state == "answered") Icons.Default.Check else Icons.Default.Close, null, tint = if (r.state == "answered") Vx.ok else Vx.text3, modifier = Modifier.size(13.dp))
            Text(answerLabel(r), color = Vx.text3, fontSize = 12.sp)
        }
    }
}

private fun answerLabel(r: AgentRequest): String {
    if (r.state != "answered") return "Cancelled"
    val a = runCatching { JSONObject(r.answer) }.getOrNull() ?: return "Answered"
    a.optString("optionId").takeIf { it.isNotBlank() }?.let { id -> return r.options.firstOrNull { it.id == id }?.name ?: id }
    return when (a.optString("outcome")) {
        "approved" -> "Approved"
        "abandoned" -> "Rejected"
        "cancelled" -> if (a.optString("feedback").isNotBlank()) "Sent back: ${a.optString("feedback")}" else "Dismissed"
        "accepted" -> a.optJSONObject("answers")?.let { o -> o.keys().asSequence().joinToString(" · ") { k -> o.optJSONArray(k)?.let { arr -> (0 until arr.length()).joinToString(", ") { arr.optString(it) } } ?: o.optString(k) } } ?: "Answered"
        "chat_about_this" -> "Chatting about it"
        "skip_interview" -> "Skipped"
        else -> "Answered"
    }
}

@Composable
private fun Composer(state: BotsState, session: SessionInfo, onSend: (String) -> Unit, onStop: () -> Unit, onMode: (String) -> Unit, onOption: (String, String) -> Unit) {
    var draft by rememberSaveable(state.threadId) { mutableStateOf("") }
    var modeMenu by remember { mutableStateOf(false) }
    val slash = draft.startsWith("/") && ' ' !in draft
    val commands = if (slash) session.commands.filter { it.name.startsWith(draft.drop(1), ignoreCase = true) }.take(8) else emptyList()
    Column(Modifier.fillMaxWidth().background(Vx.bg).padding(start = 12.dp, end = 12.dp, bottom = 10.dp, top = 4.dp)) {
        AnimatedVisibility(commands.isNotEmpty()) {
            Column(Modifier.fillMaxWidth().padding(bottom = 6.dp).clip(Vx.card).background(Vx.raised).border(1.dp, Vx.line2, Vx.card).padding(4.dp).testTag("slash-palette")) {
                commands.forEach { c ->
                    Row(Modifier.fillMaxWidth().clip(Vx.small).clickable { draft = "/${c.name} " }.padding(horizontal = 10.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        Text("/${c.name}", color = Vx.text, fontFamily = FontFamily.Monospace, fontSize = 13.sp)
                        Text(c.description, color = Vx.text3, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(24.dp)).background(Vx.raised).border(1.dp, Vx.line2, RoundedCornerShape(24.dp)).padding(start = 6.dp, end = 6.dp, top = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.Bottom) {
            Box {
                Row(Modifier.clip(RoundedCornerShape(16.dp)).border(1.dp, if (session.mode == "plan") Vx.accent else Vx.line2, RoundedCornerShape(16.dp)).background(if (session.mode == "plan") Vx.accentSoft else Color.Transparent).clickable { modeMenu = true }.padding(horizontal = 10.dp, vertical = 7.dp).testTag("mode-chip"), verticalAlignment = Alignment.CenterVertically) {
                    Text(modeLabels[session.mode] ?: session.mode, color = if (session.mode == "plan") Vx.accentHi else Vx.text2, fontSize = 12.sp, fontWeight = FontWeight.Medium)
                }
                DropdownMenu(modeMenu, { modeMenu = false }) {
                    val modes = session.modes.ifEmpty { modeLabels.map { ConfigChoice(it.key, it.value) } }
                    modes.forEach { mo -> DropdownMenuItem(text = { Text(mo.name) }, onClick = { modeMenu = false; onMode(mo.value) }, trailingIcon = { if (mo.value == session.mode) Icon(Icons.Default.Check, null, modifier = Modifier.size(16.dp)) }) }
                }
            }
            BasicTextField(
                draft, { draft = it },
                modifier = Modifier.weight(1f).padding(horizontal = 10.dp, vertical = 8.dp).heightIn(min = 20.dp, max = 140.dp).testTag("chat-input"),
                textStyle = TextStyle(color = Vx.text, fontSize = 15.sp, lineHeight = 20.sp),
                cursorBrush = SolidColor(Vx.accentHi),
                decorationBox = { inner -> Box { if (draft.isEmpty()) Text("Message the bot, or / for commands", color = Vx.text3, fontSize = 15.sp, maxLines = 1); inner() } },
            )
            val canSend = draft.isNotBlank()
            if (session.busy && !canSend) RoundIcon(Icons.Default.Close, "Stop", Vx.text, Vx.raised2, Modifier.testTag("stop")) { onStop() }
            else RoundIcon(Icons.Default.Send, "Send", Color.White, if (canSend) Vx.accent else Vx.raised2, Modifier.testTag("send"), enabled = canSend) { onSend(draft.trim()); draft = "" }
        }
        val opts = session.options
        if (opts.isNotEmpty() || session.maxTokens > 0) Row(Modifier.fillMaxWidth().padding(top = 6.dp, start = 6.dp).horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
            opts.forEach { o -> OptionChip(o, onOption) }
            if (session.maxTokens > 0) Text("${(100 * session.usedTokens / session.maxTokens)}% context", color = Vx.text3, fontSize = 11.sp)
        }
    }
}

@Composable
private fun OptionChip(o: ConfigOption, onOption: (String, String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val cur = o.choices.firstOrNull { it.value == o.current }
    val label = when (o.id) {
        "permission_mode" -> permLabels[o.current] ?: cur?.name ?: o.current
        "context_window" -> if (o.current == "auto") "Context auto" else "Context ${cur?.name ?: o.current}"
        else -> cur?.name ?: o.current
    }
    Box {
        Row(Modifier.clip(RoundedCornerShape(6.dp)).clickable { open = true }.padding(vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(label, color = Vx.text3, fontSize = 11.sp, maxLines = 1)
            Icon(Icons.Default.ArrowDropDown, null, tint = Vx.text3, modifier = Modifier.size(14.dp))
        }
        DropdownMenu(open, { open = false }) {
            Text(o.name, color = Vx.text3, fontSize = 11.sp, modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp))
            o.choices.forEach { c -> DropdownMenuItem(text = { Text(c.name, fontSize = 13.sp) }, onClick = { open = false; onOption(o.id, c.value) }, trailingIcon = { if (c.value == o.current) Icon(Icons.Default.Check, null, modifier = Modifier.size(16.dp)) }) }
        }
    }
}

// ---------- small shared controls ----------

@Composable
internal fun Tag(text: String, color: Color = Vx.text3) {
    Text(text, color = color, fontSize = 11.sp, fontWeight = FontWeight.Medium, modifier = Modifier.clip(RoundedCornerShape(5.dp)).background(color.copy(alpha = 0.12f)).padding(horizontal = 6.dp, vertical = 2.dp))
}

@Composable
internal fun PrimaryButton(text: String, enabled: Boolean = true, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Text(text, color = if (enabled) Color.White else Vx.text3, fontSize = 13.sp, fontWeight = FontWeight.Medium,
        modifier = modifier.clip(Vx.small).background(if (enabled) Vx.accent else Vx.raised2).clickable(enabled = enabled, onClick = onClick).padding(horizontal = 12.dp, vertical = 8.dp))
}

@Composable
internal fun QuietButton(text: String, icon: ImageVector? = null, danger: Boolean = false, enabled: Boolean = true, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Row(modifier.clip(Vx.small).border(1.dp, Vx.line2, Vx.small).clickable(enabled = enabled, onClick = onClick).padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        if (icon != null) Icon(icon, null, tint = Vx.text2, modifier = Modifier.size(14.dp))
        Text(text, color = if (danger) Vx.bad else if (enabled) Vx.text else Vx.text3, fontSize = 13.sp, fontWeight = FontWeight.Medium)
    }
}

@Composable
private fun ChoiceChip(text: String, on: Boolean, enabled: Boolean, onClick: () -> Unit) {
    Text(text, color = if (on) Vx.accentHi else Vx.text, fontSize = 13.sp,
        modifier = Modifier.clip(Vx.small).border(1.dp, if (on) Vx.accent else Vx.line2, Vx.small).background(if (on) Vx.accentSoft else Color.Transparent).clickable(enabled = enabled, onClick = onClick).padding(horizontal = 12.dp, vertical = 7.dp))
}

@Composable
internal fun RoundIcon(icon: ImageVector, label: String, tint: Color, bg: Color, modifier: Modifier = Modifier, enabled: Boolean = true, onClick: () -> Unit) {
    Box(modifier.size(36.dp).clip(CircleShape).background(bg).clickable(enabled = enabled, onClick = onClick), contentAlignment = Alignment.Center) {
        Icon(icon, label, tint = tint, modifier = Modifier.size(18.dp))
    }
}
