package com.example.freerdptun

import android.app.Activity
import android.content.ClipboardManager
import android.content.Context
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.delay

private sealed interface Screen {
    data object Home : Screen
    data object Add : Screen
    data object Log : Screen
    data class Edit(val id: String) : Screen
}

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Native.ensureInit(this)
        val store = ProfileStore(this)
        setContent { MaterialTheme { App(store) } }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun App(store: ProfileStore) {
    val ctx = LocalContext.current
    var screen by remember { mutableStateOf<Screen>(Screen.Home) }
    val state by VpnStateHolder.state.collectAsStateWithLifecycle()

    // Запрос системного согласия на VPN, затем подключение.
    var pending by remember { mutableStateOf<Profile?>(null) }
    val consent = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val p = pending
        pending = null
        if (r.resultCode == Activity.RESULT_OK && p != null) FrtVpnService.connect(ctx, p)
        else VpnStateHolder.set { it.copy(error = "Разрешение на VPN не выдано") }
    }
    fun connect(p: Profile) {
        VpnStateHolder.clearError()
        val problems = p.config.validate()
        if (problems.isNotEmpty()) {
            VpnStateHolder.set { it.copy(error = problems.joinToString("\n")) }
            return
        }
        val prep = VpnService.prepare(ctx)
        if (prep == null) FrtVpnService.connect(ctx, p)
        else { pending = p; consent.launch(prep) }
    }

    BackHandler(enabled = screen != Screen.Home) { screen = Screen.Home }

    when (val s = screen) {
        Screen.Home -> HomeScreen(store, state, ::connect,
            onDisconnect = { FrtVpnService.disconnect(ctx) },
            onAdd = { screen = Screen.Add }, onLog = { screen = Screen.Log },
            onEdit = { screen = Screen.Edit(it) })
        Screen.Add -> AddScreen(store) { screen = Screen.Home }
        Screen.Log -> LogScreen { screen = Screen.Home }
        is Screen.Edit -> {
            val p = store.profiles.firstOrNull { it.id == s.id }
            if (p == null) screen = Screen.Home
            else EditScreen(store, p, state) { screen = Screen.Home }
        }
    }
}

// ---------------------------------------------------------------- главный экран

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HomeScreen(
    store: ProfileStore, state: VpnState, onConnect: (Profile) -> Unit, onDisconnect: () -> Unit,
    onAdd: () -> Unit, onLog: () -> Unit, onEdit: (String) -> Unit,
) {
    var toDelete by remember { mutableStateOf<Profile?>(null) }
    Scaffold(
        topBar = {
            TopAppBar(title = { Text("FreeRDPTun") }, actions = {
                TextButton(onClick = onLog) { Text("Журнал") }
                TextButton(onClick = onAdd) { Text("+ Добавить") }
            })
        },
    ) { pad ->
        Column(Modifier.padding(pad).padding(16.dp).verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            StatusCard(store, state, onConnect, onDisconnect)
            Text("Конфигурации", style = MaterialTheme.typography.titleMedium)
            if (store.profiles.isEmpty()) {
                Text("Нет конфигураций. Нажмите «+ Добавить» и вставьте JSON.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            store.profiles.forEach { p ->
                val active = state.isActive && state.activeId == p.id
                Card(Modifier.fillMaxWidth().clickable { onEdit(p.id) }) {
                    Column(Modifier.padding(12.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text(p.name, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                            if (active) Text("активна", color = Color(0xFF2E7D32))
                            if (store.defaultId == p.id) Text("  ★ по умолчанию", color = Color(0xFFB8860B))
                        }
                        Text(p.config.address, style = MaterialTheme.typography.bodySmall)
                        Text("${p.config.cidr} · MTU ${p.config.mtu}", style = MaterialTheme.typography.bodySmall)
                        Row {
                            TextButton(onClick = { store.setDefault(p.id) }, enabled = store.defaultId != p.id) {
                                Text("По умолчанию")
                            }
                            TextButton(onClick = {
                                if (active) VpnStateHolder.set { it.copy(error = "Сначала отключитесь, затем удаляйте активную конфигурацию.") }
                                else toDelete = p
                            }) { Text("Удалить", color = MaterialTheme.colorScheme.error) }
                        }
                    }
                }
            }
        }
    }
    toDelete?.let { p ->
        AlertDialog(
            onDismissRequest = { toDelete = null },
            title = { Text("Удалить конфигурацию?") },
            text = { Text("«${p.name}» будет удалена.") },
            confirmButton = { TextButton(onClick = { store.delete(p.id); toDelete = null }) { Text("Удалить") } },
            dismissButton = { TextButton(onClick = { toDelete = null }) { Text("Отмена") } },
        )
    }
}

@Composable
private fun StatusCard(store: ProfileStore, state: VpnState, onConnect: (Profile) -> Unit, onDisconnect: () -> Unit) {
    val (text, color) = when (state.status) {
        VpnStatus.IDLE -> "Отключено" to Color.Gray
        VpnStatus.CONNECTING -> "Подключение…" to Color(0xFFEF8F00)
        VpnStatus.CONNECTED -> "Подключено" to Color(0xFF2E7D32)
        VpnStatus.DISCONNECTING -> "Отключение…" to Color(0xFFEF8F00)
    }
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Surface(color = color, shape = MaterialTheme.shapes.small, modifier = Modifier.size(12.dp)) {}
                Spacer(Modifier.width(8.dp))
                Text(text, style = MaterialTheme.typography.titleLarge)
                if (state.status == VpnStatus.CONNECTING || state.status == VpnStatus.DISCONNECTING) {
                    Spacer(Modifier.width(8.dp)); CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                }
            }
            val shown = if (state.isActive) store.profiles.firstOrNull { it.id == state.activeId } else store.defaultProfile
            Text(
                if (shown != null) "${shown.name} — ${shown.config.address}" else "Нет конфигурации по умолчанию",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            state.error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp) }
            Button(
                onClick = { if (state.isActive) onDisconnect() else store.defaultProfile?.let(onConnect) },
                enabled = state.status != VpnStatus.DISCONNECTING && (state.isActive || store.defaultProfile != null),
                colors = if (state.isActive) ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)
                else ButtonDefaults.buttonColors(),
                modifier = Modifier.fillMaxWidth(),
            ) { Text(if (state.isActive) "Отключиться" else "Подключиться") }
        }
    }
}

// ---------------------------------------------------------------- добавление

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AddScreen(store: ProfileStore, close: () -> Unit) {
    val ctx = LocalContext.current
    var name by remember { mutableStateOf("") }
    var text by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    Scaffold(topBar = {
        TopAppBar(title = { Text("Новая конфигурация") },
            navigationIcon = { TextButton(onClick = close) { Text("Отмена") } },
            actions = { TextButton(onClick = {
                try {
                    val c = TunnelConfig.parse(text)
                    val problems = c.validate()
                    if (problems.isNotEmpty()) { error = problems.joinToString("\n"); return@TextButton }
                    store.add(Profile(name = name.trim().ifEmpty { c.host }, config = c))
                    close()
                } catch (e: TunnelConfig.ParseError) { error = e.message }
            }) { Text("Добавить") } })
    }) { pad ->
        Column(Modifier.padding(pad).padding(16.dp).verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(10.dp)) {
            OutlinedTextField(name, { name = it }, label = { Text("Название (необязательно)") },
                singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(text, { text = it; error = null }, label = { Text("JSON конфигурации") },
                minLines = 10, modifier = Modifier.fillMaxWidth(),
                textStyle = androidx.compose.ui.text.TextStyle(fontFamily = FontFamily.Monospace, fontSize = 12.sp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = {
                    val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    text = cm.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString() ?: ""
                    error = null
                }) { Text("Вставить из буфера") }
                OutlinedButton(onClick = {}, enabled = false) { Text("QR (позже)") }
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp) }
        }
    }
}

// ---------------------------------------------------------------- редактирование

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun EditScreen(store: ProfileStore, p: Profile, state: VpnState, close: () -> Unit) {
    val c = p.config
    var name by remember { mutableStateOf(p.name) }
    var address by remember { mutableStateOf(c.address) }
    var sha by remember { mutableStateOf(c.serverSha256) }
    var uuid by remember { mutableStateOf(c.uuid) }
    var tun by remember { mutableStateOf(c.tun) }
    var cidr by remember { mutableStateOf(c.cidr) }
    var ipv4 by remember { mutableStateOf(c.ipv4) }
    var mtu by remember { mutableStateOf(c.mtu.toString()) }
    var up by remember { mutableStateOf(c.totalUploadBps.toString()) }
    var down by remember { mutableStateOf(c.totalDownloadBps.toString()) }
    var maxConn by remember { mutableStateOf(c.maxConnections.toString()) }
    var hs by remember { mutableStateOf(c.handshakeTimeoutSeconds.toString()) }
    var idle by remember { mutableStateOf(c.idleTimeoutSeconds.toString()) }
    var queue by remember { mutableStateOf(c.queuePackets.toString()) }
    var bridge by remember { mutableStateOf(c.bridge) }
    var cert by remember { mutableStateOf(c.certificate) }
    var key by remember { mutableStateOf(c.privateKey) }
    var dns by remember { mutableStateOf(p.dnsServers) }
    var block6 by remember { mutableStateOf(p.blockIPv6) }
    var error by remember { mutableStateOf<String?>(null) }

    fun build(): Profile {
        fun i(s: String, f: String) = s.trim().toIntOrNull() ?: throw TunnelConfig.ParseError("$f: нужно целое число")
        fun l(s: String, f: String) = s.trim().toLongOrNull() ?: throw TunnelConfig.ParseError("$f: нужно целое число")
        val cfg = TunnelConfig(
            address = address.trim(), tun = tun.trim(), cidr = cidr.trim(), mtu = i(mtu, "mtu"),
            bridge = bridge.trim(), certificate = cert.trim(), privateKey = key.trim(),
            serverSha256 = sha.trim().lowercase(), uuid = uuid.trim().lowercase(), ipv4 = ipv4.trim(),
            totalUploadBps = l(up, "total_upload_bps"), totalDownloadBps = l(down, "total_download_bps"),
            maxConnections = i(maxConn, "max_connections"), handshakeTimeoutSeconds = i(hs, "handshake_timeout_seconds"),
            idleTimeoutSeconds = i(idle, "idle_timeout_seconds"), queuePackets = i(queue, "queue_packets"),
        )
        val problems = cfg.validate()
        if (problems.isNotEmpty()) throw TunnelConfig.ParseError(problems.joinToString("\n"))
        return p.copy(name = name.trim().ifEmpty { cfg.host }, config = cfg, dnsServers = dns.trim(), blockIPv6 = block6)
    }

    val preview = runCatching { build().config.jsonString() }.getOrNull()

    @Composable
    fun F(label: String, v: String, set: (String) -> Unit, num: Boolean = false, mono: Boolean = false) {
        OutlinedTextField(v, { set(it); error = null }, label = { Text(label) }, singleLine = true,
            modifier = Modifier.fillMaxWidth(),
            keyboardOptions = KeyboardOptions(keyboardType = if (num) KeyboardType.Number else KeyboardType.Uri),
            textStyle = androidx.compose.ui.text.TextStyle(fontFamily = if (mono) FontFamily.Monospace else FontFamily.Default,
                fontSize = if (mono) 12.sp else 16.sp))
    }

    @Composable
    fun H(t: String) = Text(t, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)

    Scaffold(topBar = {
        TopAppBar(title = { Text(name, maxLines = 1, overflow = TextOverflow.Ellipsis) },
            navigationIcon = { TextButton(onClick = close) { Text("Назад") } },
            actions = { TextButton(onClick = {
                try { store.update(build()); close() } catch (e: TunnelConfig.ParseError) { error = e.message }
            }) { Text("Сохранить") } })
    }) { pad ->
        Column(Modifier.padding(pad).padding(16.dp).verticalScroll(rememberScrollState()).imePadding(),
            verticalArrangement = Arrangement.spacedBy(8.dp)) {
            H("Название")
            OutlinedTextField(name, { name = it }, singleLine = true, modifier = Modifier.fillMaxWidth())
            H("Подключение")
            F("address (host:порт)", address, { address = it })
            F("server_sha256", sha, { sha = it }, mono = true)
            F("uuid", uuid, { uuid = it }, mono = true)
            Text("mode: client", color = MaterialTheme.colorScheme.onSurfaceVariant)
            H("Туннель")
            F("tun", tun, { tun = it })
            F("cidr", cidr, { cidr = it })
            F("ipv4", ipv4, { ipv4 = it })
            F("mtu", mtu, { mtu = it }, num = true)
            H("Лимиты и таймауты")
            F("total_upload_bps (0 = без лимита)", up, { up = it }, num = true)
            F("total_download_bps (0 = без лимита)", down, { down = it }, num = true)
            F("max_connections", maxConn, { maxConn = it }, num = true)
            F("handshake_timeout_seconds", hs, { hs = it }, num = true)
            F("idle_timeout_seconds", idle, { idle = it }, num = true)
            F("queue_packets", queue, { queue = it }, num = true)
            H("Прочие поля JSON")
            Text("Встроенный транспорт заменяет внешний bridge; клиенту эти поля не нужны.",
                fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            F("bridge", bridge, { bridge = it })
            F("certificate", cert, { cert = it })
            F("private_key", key, { key = it })
            Text("clients: null", color = MaterialTheme.colorScheme.onSurfaceVariant)
            H("Параметры приложения (вне JSON)")
            F("DNS-серверы", dns, { dns = it })
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text("Блокировать IPv6 (без утечек)", Modifier.weight(1f))
                Switch(block6, { block6 = it })
            }
            Text("Туннель только IPv4. DNS-запросы идут в туннель; сервер должен выполнять NAT и маршрутизацию.",
                fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            H("JSON")
            if (preview != null) {
                Text(preview, fontFamily = FontFamily.Monospace, fontSize = 11.sp)
                val ctx = LocalContext.current
                OutlinedButton(onClick = {
                    val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    cm.setPrimaryClip(android.content.ClipData.newPlainText("config", preview))
                }) { Text("Скопировать JSON") }
            } else Text("Исправьте поля, чтобы увидеть JSON.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (state.isActive && state.activeId == p.id) {
                Text("Изменения применятся при следующем подключении.", fontSize = 13.sp)
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, fontSize = 13.sp) }
        }
    }
}

// ---------------------------------------------------------------- журнал

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun LogScreen(close: () -> Unit) {
    val ctx = LocalContext.current
    var text by remember { mutableStateOf("") }
    LaunchedEffect(Unit) {
        while (true) {
            text = listOf(Native.logFile, java.io.File(Native.logFile.parentFile, "frt-freerdp.log")).joinToString("\n") { f ->
                runCatching {
                    val b = f.readBytes()
                    String(b, maxOf(0, b.size - 32 * 1024), minOf(b.size, 32 * 1024))
                }.getOrDefault("")
            }
            delay(1000)
        }
    }
    Scaffold(topBar = {
        TopAppBar(title = { Text("Журнал") },
            navigationIcon = { TextButton(onClick = close) { Text("Назад") } },
            actions = { TextButton(onClick = {
                val cm = ctx.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                cm.setPrimaryClip(android.content.ClipData.newPlainText("log", text))
            }) { Text("Копировать") } })
    }) { pad ->
        Column(Modifier.padding(pad).padding(12.dp).verticalScroll(rememberScrollState(), reverseScrolling = true)) {
            Text(text.ifEmpty { "Журнал пуст." }, fontFamily = FontFamily.Monospace, fontSize = 11.sp)
        }
    }
}
