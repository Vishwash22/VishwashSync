package com.lansync.app

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.DocumentsContract
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Duration
import java.time.Instant

private val Accent = Color(0xFF2F6FED)
private val Ok = Color(0xFF1F9D55)
private val Warn = Color(0xFFD97706)
private val Err = Color(0xFFD64545)

private val MODES = linkedMapOf(
    "sendreceive" to ("Send & receive" to "Changes go both ways"),
    "sendonly" to ("Send only" to "This phone's changes go out, nothing comes in"),
    "receiveonly" to ("Receive only" to "Changes come in; local edits stay here"),
)

class MainActivity : ComponentActivity() {
    private val storageGranted = mutableStateOf(false)
    private val legacyStoragePermission =
        registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { storageGranted.value = hasStorageAccess() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        SyncService.start(this)
        setContent {
            MaterialTheme(colorScheme = lightColorScheme(primary = Accent)) {
                App(storageGranted = storageGranted.value, onGrantStorage = ::requestStorage, onQuit = {
                    SyncService.stop(this)
                    finishAndRemoveTask()
                })
            }
        }
    }

    override fun onResume() {
        super.onResume()
        storageGranted.value = hasStorageAccess()
    }

    private fun hasStorageAccess() = if (Build.VERSION.SDK_INT >= 30) {
        Environment.isExternalStorageManager()
    } else {
        checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
    }

    private fun requestStorage() {
        if (Build.VERSION.SDK_INT >= 30) {
            startActivity(
                Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName"))
            )
        } else {
            legacyStoragePermission.launch(
                arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE, Manifest.permission.WRITE_EXTERNAL_STORAGE)
            )
        }
    }
}

/** UI state loaded from the core's REST API. */
private class UiState {
    var status by mutableStateOf<Api.Status?>(null)
    var folders by mutableStateOf<List<Api.Folder>>(emptyList())
    var devices by mutableStateOf(Api.Devices(emptyList(), emptyList()))
    var offers by mutableStateOf<List<Api.Offer>>(emptyList())
    var coreError by mutableStateOf<String?>(null)

    suspend fun refresh() {
        try {
            status = Api.status()
            folders = Api.folders()
            devices = Api.devices()
            coreError = null
        } catch (e: Exception) {
            coreError = "Starting LanSync…"
        }
    }

    suspend fun refreshOffers() {
        runCatching { offers = Api.offers() }
    }

    fun deviceName(id: String) = devices.known.firstOrNull { it.id == id }?.name?.ifBlank { null } ?: id.take(8)
}

private data class FolderDraft(
    val id: String = "", val label: String = "", val path: String = "",
    val mode: String = "sendreceive", val devices: Set<String> = emptySet(), val isNew: Boolean = true,
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun App(storageGranted: Boolean, onGrantStorage: () -> Unit, onQuit: () -> Unit) {
    val ui = remember { UiState() }
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var tab by remember { mutableIntStateOf(0) }
    var folderDraft by remember { mutableStateOf<FolderDraft?>(null) }
    var showAddDevice by remember { mutableStateOf<Api.Device?>(null) }
    var showRename by remember { mutableStateOf(false) }
    var firstRunName by remember { mutableStateOf(false) }
    var menuOpen by remember { mutableStateOf(false) }

    // Run an API action, show its error, refresh afterwards.
    fun act(block: suspend () -> Unit) {
        scope.launch {
            try {
                block()
            } catch (e: Exception) {
                snackbar.showSnackbar(e.message ?: "Something went wrong")
            }
            ui.refresh()
        }
    }

    // First start: ask the user to name this phone so it's easy to recognise on other devices.
    LaunchedEffect(ui.status?.nameSet) {
        if (ui.status?.nameSet == false && !firstRunName) {
            firstRunName = true
            showRename = true
        }
    }

    LaunchedEffect(Unit) {
        var tick = 0
        while (true) {
            ui.refresh()
            if (tick % 8 == 0 && ui.coreError == null) ui.refreshOffers()
            tick++
            delay(2000)
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text("LanSync", fontWeight = FontWeight.Bold)
                        ui.status?.let { Text(it.deviceName, fontSize = 13.sp, color = Color(0xFFB8C2D6)) }
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = Color(0xFF18202F), titleContentColor = Color.White, actionIconContentColor = Color.White,
                ),
                actions = {
                    IconButton(onClick = { act { Api.discover(); delay(800); ui.refreshOffers() } }) {
                        Icon(Icons.Filled.Refresh, contentDescription = "Look for devices")
                    }
                    Box {
                        IconButton(onClick = { menuOpen = true }) { Icon(Icons.Filled.MoreVert, contentDescription = "More") }
                        DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                            DropdownMenuItem(text = { Text("Rename this device") }, onClick = { menuOpen = false; showRename = true })
                            DropdownMenuItem(text = { Text("Quit LanSync") }, onClick = { menuOpen = false; onQuit() })
                        }
                    }
                },
            )
        },
        bottomBar = {
            NavigationBar {
                NavigationBarItem(selected = tab == 0, onClick = { tab = 0 }, icon = { Icon(Icons.Filled.Home, null) }, label = { Text("Folders") })
                NavigationBarItem(selected = tab == 1, onClick = { tab = 1 }, icon = { Icon(Icons.Filled.Person, null) }, label = { Text("Devices") })
            }
        },
        floatingActionButton = {
            FloatingActionButton(onClick = {
                if (tab == 0) folderDraft = FolderDraft() else showAddDevice = Api.Device("", "", "", "", false)
            }, containerColor = Accent, contentColor = Color.White) {
                Icon(Icons.Filled.Add, contentDescription = if (tab == 0) "Add folder" else "Add device")
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize().background(Color(0xFFF4F6F9))) {
            val err = ui.coreError
            if (err != null && ui.status == null) {
                Text(err, Modifier.align(Alignment.Center), color = Color.Gray)
            } else if (tab == 0) {
                FoldersScreen(ui, storageGranted, onGrantStorage,
                    onSync = { f -> act { Api.syncFolder(f.id) } },
                    onEdit = { f -> folderDraft = FolderDraft(f.id, f.label, f.path, f.mode, f.devices.toSet(), isNew = false) },
                    onRemove = { f -> act { Api.removeFolder(f.id) } },
                    onAccept = { o -> folderDraft = FolderDraft(o.folderID, o.label, defaultPath(o.label), "sendreceive", setOf(o.deviceID)) },
                )
            } else {
                DevicesScreen(ui,
                    onRename = { showRename = true },
                    onAddDiscovered = { d -> act { Api.addDevice(d.id, d.name, ""); ui.refreshOffers() } },
                    onRemove = { d -> act { Api.removeDevice(d.id) } },
                )
            }
        }
    }

    folderDraft?.let { draft ->
        FolderDialog(draft, ui.devices.known, onDismiss = { folderDraft = null }, onSave = { d ->
            scope.launch {
                try {
                    Api.saveFolder(d.id, d.label, d.path, d.mode, d.devices.toList())
                    folderDraft = null
                    ui.refresh()
                    ui.refreshOffers()
                } catch (e: Exception) {
                    snackbar.showSnackbar(e.message ?: "Could not save folder")
                }
            }
        })
    }
    showAddDevice?.let { d ->
        DeviceDialog(d, onDismiss = { showAddDevice = null }, onSave = { id, name, addr ->
            scope.launch {
                try {
                    Api.addDevice(id, name, addr)
                    showAddDevice = null
                    ui.refresh()
                    ui.refreshOffers()
                } catch (e: Exception) {
                    snackbar.showSnackbar(e.message ?: "Could not add device")
                }
            }
        })
    }
    if (showRename) {
        val firstRun = ui.status?.nameSet == false
        var name by remember { mutableStateOf(ui.status?.deviceName ?: "") }
        AlertDialog(
            onDismissRequest = { showRename = false },
            title = { Text(if (firstRun) "Name this device" else "Rename this device") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("This name is shown on your other devices, so pick one you'll recognise, like “Vishwash's phone”.", fontSize = 14.sp, color = Color.DarkGray)
                    OutlinedTextField(name, { if (it.length <= 64) name = it }, singleLine = true, label = { Text("Device name") }, modifier = Modifier.fillMaxWidth())
                }
            },
            confirmButton = {
                TextButton(onClick = { showRename = false; act { Api.setDeviceName(name) } }, enabled = name.isNotBlank()) { Text("Save") }
            },
            dismissButton = { TextButton(onClick = { showRename = false }) { Text(if (firstRun) "Later" else "Cancel") } },
        )
    }
}

private fun defaultPath(label: String) =
    Environment.getExternalStorageDirectory().absolutePath + "/LanSync/" + label.replace("/", "_")

@Composable
private fun FoldersScreen(
    ui: UiState, storageGranted: Boolean, onGrantStorage: () -> Unit,
    onSync: (Api.Folder) -> Unit, onEdit: (Api.Folder) -> Unit, onRemove: (Api.Folder) -> Unit, onAccept: (Api.Offer) -> Unit,
) {
    var confirmRemove by remember { mutableStateOf<Api.Folder?>(null) }
    LazyColumn(contentPadding = PaddingValues(12.dp, 12.dp, 12.dp, 88.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        if (!storageGranted) {
            item {
                Card(modifier = Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = Color(0xFFFFF1F1))) {
                    Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Filled.Warning, null, tint = Err)
                        Spacer(Modifier.width(10.dp))
                        Column(Modifier.weight(1f)) {
                            Text("Storage access needed", fontWeight = FontWeight.SemiBold)
                            Text("Allow \"All files access\" so LanSync can sync folders on this phone.", fontSize = 13.sp, color = Color.DarkGray)
                        }
                        TextButton(onClick = onGrantStorage) { Text("Allow") }
                    }
                }
            }
        }
        val groups = folderGroups(ui)
        if (groups.isEmpty()) {
            item { Text("No folders yet. Tap + to add one, or accept a folder another device offers.", Modifier.padding(8.dp), color = Color.Gray) }
        }
        // One section per device pair, e.g. "Vishwash's laptop ⇄ This phone".
        groups.forEach { g ->
            item(key = "group-" + g.key) { GroupHeader(g, ui, onSyncAll = { g.folders.forEach(onSync) }) }
            items(g.offers, key = { "offer-" + it.deviceID + it.folderID }) { o ->
                Card(modifier = Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = Color(0xFFFFF8E6))) {
                    Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text("${o.deviceName} wants to share “${o.label}”", fontWeight = FontWeight.SemiBold)
                            Text("Accept it to choose where to keep it on this phone.", fontSize = 12.sp, color = Color.Gray)
                        }
                        Button(onClick = { onAccept(o) }) { Text("Accept") }
                    }
                }
            }
            items(g.folders, key = { it.id }) { f ->
                FolderCard(f, g.devices.joinToString(" + ") { ui.deviceName(it) }.ifEmpty { null }, onSync, onEdit, onRemove = { confirmRemove = f })
            }
        }
    }
    confirmRemove?.let { f ->
        AlertDialog(
            onDismissRequest = { confirmRemove = null },
            title = { Text("Stop syncing “${f.label}”?") },
            text = { Text("Files on this phone are kept.") },
            confirmButton = { TextButton(onClick = { confirmRemove = null; onRemove(f) }) { Text("Remove", color = Err) } },
            dismissButton = { TextButton(onClick = { confirmRemove = null }) { Text("Cancel") } },
        )
    }
}

private class FolderGroup(val key: String, val devices: List<String>) {
    val folders = mutableListOf<Api.Folder>()
    val offers = mutableListOf<Api.Offer>()
}

/** Groups folders (and pending offers) by the set of devices they're shared with. */
private fun folderGroups(ui: UiState): List<FolderGroup> {
    val groups = linkedMapOf<String, FolderGroup>()
    fun group(ids: List<String>): FolderGroup {
        val devices = ids.sorted()
        val key = devices.joinToString(",")
        return groups.getOrPut(key) { FolderGroup(key, devices) }
    }
    ui.folders.forEach { group(it.devices).folders.add(it) }
    ui.offers.forEach { group(listOf(it.deviceID)).offers.add(it) }
    return groups.values.sortedWith(
        compareBy<FolderGroup> { it.devices.isEmpty() }.thenBy { g -> g.devices.joinToString { ui.deviceName(it) } }
    )
}

@Composable
private fun GroupHeader(g: FolderGroup, ui: UiState, onSyncAll: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(start = 4.dp, top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        if (g.devices.isEmpty()) {
            Text("Not shared with any device yet", fontWeight = FontWeight.SemiBold, color = Color.Gray, modifier = Modifier.weight(1f))
            return@Row
        }
        val online = g.devices.all { id -> ui.devices.known.any { it.id == id && it.online } }
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(9.dp).clip(CircleShape).background(if (online) Ok else Color(0xFFC3C8D2)))
                Spacer(Modifier.width(8.dp))
                Text(g.devices.joinToString(" + ") { ui.deviceName(it) }, fontWeight = FontWeight.Bold, fontSize = 16.sp, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
            Text("⇄  This phone · ${ui.status?.deviceName ?: ""}", fontSize = 13.sp, color = Accent, modifier = Modifier.padding(start = 17.dp))
        }
        if (g.folders.isNotEmpty()) OutlinedButton(onClick = onSyncAll) { Text("Sync all") }
    }
}

private fun direction(f: Api.Folder, remote: String) = when (f.mode) {
    "sendonly" -> "This phone → $remote"
    "receiveonly" -> "$remote → This phone"
    else -> "This phone ⇄ $remote"
}

@Composable
private fun FolderCard(f: Api.Folder, remote: String?, onSync: (Api.Folder) -> Unit, onEdit: (Api.Folder) -> Unit, onRemove: () -> Unit) {
    val s = f.status
    val busy = s.state == "scanning" || s.state == "syncing"
    val (stateText, stateColor) = when (s.state) {
        "idle" -> "Up to date" to Ok
        "scanning" -> "Scanning…" to Warn
        "syncing" -> "Syncing ${s.done}/${s.total}" to Warn
        else -> "Error" to Err
    }
    Card(modifier = Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = Color.White), elevation = CardDefaults.cardElevation(1.dp)) {
        Column(Modifier.padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(f.label, fontWeight = FontWeight.Bold, fontSize = 17.sp, modifier = Modifier.weight(1f, fill = false), maxLines = 1, overflow = TextOverflow.Ellipsis)
                Spacer(Modifier.width(8.dp))
                Text(
                    MODES[f.mode]?.first ?: f.mode, fontSize = 11.sp, fontWeight = FontWeight.SemiBold, color = Accent,
                    modifier = Modifier.clip(RoundedCornerShape(50)).background(Color(0xFFE8EEFC)).padding(horizontal = 8.dp, vertical = 2.dp),
                )
                Spacer(Modifier.width(8.dp))
                Text(stateText, color = stateColor, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            }
            if (remote != null) Text(direction(f, remote), fontSize = 13.sp, color = Accent, fontWeight = FontWeight.Medium, modifier = Modifier.padding(top = 2.dp))
            Text(f.path, fontSize = 12.sp, color = Color.Gray, modifier = Modifier.padding(top = 2.dp))
            Text("${s.files} files · ${size(s.bytes)} · last sync ${ago(s.lastSync)}", fontSize = 12.sp, color = Color.Gray)
            if (s.lastError.isNotBlank()) Text(s.lastError, fontSize = 12.sp, color = Err)
            if (s.state == "syncing" && s.total > 0) {
                LinearProgressIndicator(progress = { s.done.toFloat() / s.total }, modifier = Modifier.fillMaxWidth().padding(top = 8.dp), color = Warn)
            }
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { onSync(f) }, enabled = !busy) { Text("Sync now") }
                OutlinedButton(onClick = { onEdit(f) }) { Text("Edit") }
                TextButton(onClick = onRemove) { Text("Remove", color = Err) }
            }
        }
    }
}

@Composable
private fun DevicesScreen(ui: UiState, onRename: () -> Unit, onAddDiscovered: (Api.Device) -> Unit, onRemove: (Api.Device) -> Unit) {
    val context = LocalContext.current
    var confirmRemove by remember { mutableStateOf<Api.Device?>(null) }
    LazyColumn(contentPadding = PaddingValues(12.dp, 12.dp, 12.dp, 88.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        ui.status?.let { me ->
            item {
                Card(modifier = Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = Color(0xFF18202F))) {
                    Column(Modifier.padding(14.dp)) {
                        Text("This device", color = Color(0xFFB8C2D6), fontSize = 12.sp)
                        Text(me.deviceName, color = Color.White, fontWeight = FontWeight.Bold, fontSize = 17.sp)
                        Text(me.deviceID, color = Color(0xFFCFD6E4), fontFamily = FontFamily.Monospace, fontSize = 12.sp, modifier = Modifier.padding(top = 4.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(top = 6.dp)) {
                            TextButton(onClick = onRename) { Text("Rename", color = Color.White) }
                            TextButton(onClick = { copy(context, me.deviceID) }) { Text("Copy ID", color = Color.White) }
                            TextButton(onClick = {
                                context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, me.deviceID), "Share device ID"))
                            }) { Text("Share ID", color = Color.White) }
                        }
                    }
                }
            }
        }
        item { SectionTitle("My devices") }
        if (ui.devices.known.isEmpty()) item { Text("No devices added yet.", Modifier.padding(horizontal = 4.dp), color = Color.Gray) }
        items(ui.devices.known, key = { "k" + it.id }) { d ->
            DeviceRow(d, action = { TextButton(onClick = { confirmRemove = d }) { Text("Remove", color = Err) } })
        }
        item { SectionTitle("Discovered on this network") }
        if (ui.devices.discovered.isEmpty()) {
            item { Text("No other LanSync devices found yet. Make sure they're on the same Wi-Fi.", Modifier.padding(horizontal = 4.dp), color = Color.Gray) }
        }
        items(ui.devices.discovered, key = { "d" + it.id }) { d ->
            DeviceRow(d, action = { Button(onClick = { onAddDiscovered(d) }) { Text("Add") } })
        }
    }
    confirmRemove?.let { d ->
        AlertDialog(
            onDismissRequest = { confirmRemove = null },
            title = { Text("Remove ${d.name.ifBlank { "device" }}?") },
            text = { Text("It is also removed from all folders.") },
            confirmButton = { TextButton(onClick = { confirmRemove = null; onRemove(d) }) { Text("Remove", color = Err) } },
            dismissButton = { TextButton(onClick = { confirmRemove = null }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun SectionTitle(text: String) =
    Text(text, fontWeight = FontWeight.SemiBold, color = Color.Gray, fontSize = 13.sp, modifier = Modifier.padding(start = 4.dp, top = 8.dp))

@Composable
private fun DeviceRow(d: Api.Device, action: @Composable () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = Color.White), elevation = CardDefaults.cardElevation(1.dp)) {
        Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(10.dp).clip(CircleShape).background(if (d.online) Ok else Color(0xFFC3C8D2)))
            Spacer(Modifier.width(10.dp))
            Column(Modifier.weight(1f)) {
                Text(d.name.ifBlank { "Unnamed" } + if (d.online) "  · online" else "  · offline", fontWeight = FontWeight.SemiBold)
                Text(d.id, fontFamily = FontFamily.Monospace, fontSize = 11.sp, color = Color.Gray, maxLines = 1, overflow = TextOverflow.Ellipsis)
                val addr = d.seenAddr.ifBlank { d.addr }
                if (addr.isNotBlank()) Text(addr, fontSize = 12.sp, color = Color.Gray)
            }
            Spacer(Modifier.width(8.dp))
            action()
        }
    }
}

@Composable
private fun FolderDialog(initial: FolderDraft, known: List<Api.Device>, onDismiss: () -> Unit, onSave: (FolderDraft) -> Unit) {
    var d by remember { mutableStateOf(initial) }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        uri?.let { treeUriToPath(it) }?.let { path ->
            d = d.copy(path = path, label = d.label.ifBlank { path.substringAfterLast('/') })
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (initial.isNew) "Add folder" else "Edit “${initial.label}”") },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(d.label, { d = d.copy(label = it) }, label = { Text("Label") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(d.path, { d = d.copy(path = it) }, label = { Text("Folder path") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedButton(onClick = { picker.launch(null) }) { Text("Choose folder…") }
                if (initial.isNew && initial.id.isEmpty()) {
                    OutlinedTextField(d.id, { d = d.copy(id = it) }, label = { Text("Folder ID (optional)") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                }
                Text("Sync mode", fontWeight = FontWeight.SemiBold, fontSize = 13.sp, modifier = Modifier.padding(top = 4.dp))
                MODES.forEach { (key, value) ->
                    Row(Modifier.fillMaxWidth().clickable { d = d.copy(mode = key) }, verticalAlignment = Alignment.CenterVertically) {
                        RadioButton(selected = d.mode == key, onClick = { d = d.copy(mode = key) })
                        Column {
                            Text(value.first)
                            Text(value.second, fontSize = 12.sp, color = Color.Gray)
                        }
                    }
                }
                Text("Share with", fontWeight = FontWeight.SemiBold, fontSize = 13.sp, modifier = Modifier.padding(top = 4.dp))
                if (known.isEmpty()) Text("Add a device first to share this folder.", fontSize = 13.sp, color = Color.Gray)
                known.forEach { dev ->
                    val checked = dev.id in d.devices
                    Row(Modifier.fillMaxWidth().clickable { d = d.copy(devices = if (checked) d.devices - dev.id else d.devices + dev.id) }, verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(checked = checked, onCheckedChange = { d = d.copy(devices = if (it) d.devices + dev.id else d.devices - dev.id) })
                        Text(dev.name.ifBlank { dev.id })
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = { onSave(d) }, enabled = d.path.isNotBlank()) { Text("Save") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
private fun DeviceDialog(initial: Api.Device, onDismiss: () -> Unit, onSave: (String, String, String) -> Unit) {
    var id by remember { mutableStateOf(initial.id) }
    var name by remember { mutableStateOf(initial.name) }
    var addr by remember { mutableStateOf(initial.addr) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Add device") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(id, { id = it }, label = { Text("Device ID") }, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                OutlinedTextField(addr, { addr = it }, label = { Text("Address (optional)") }, placeholder = { Text("192.168.1.20:22000") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                Text("Only needed if the device isn't discovered automatically.", fontSize = 12.sp, color = Color.Gray)
            }
        },
        confirmButton = { TextButton(onClick = { onSave(id, name, addr) }, enabled = id.isNotBlank()) { Text("Add") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** Converts a folder picked with the system picker ("primary:DCIM/Camera") into a file path the Go core can use. */
private fun treeUriToPath(uri: Uri): String? {
    val docId = runCatching { DocumentsContract.getTreeDocumentId(uri) }.getOrNull() ?: return null
    val volume = docId.substringBefore(':')
    val rel = docId.substringAfter(':', "")
    val root = if (volume.equals("primary", ignoreCase = true)) Environment.getExternalStorageDirectory().absolutePath else "/storage/$volume"
    return if (rel.isEmpty()) root else "$root/$rel"
}

private fun copy(context: Context, text: String) {
    val cm = context.getSystemService(ClipboardManager::class.java)
    cm.setPrimaryClip(ClipData.newPlainText("LanSync device ID", text))
}

private fun size(bytes: Long): String {
    val units = listOf("B", "KB", "MB", "GB", "TB")
    var v = bytes.toDouble()
    var i = 0
    while (v >= 1024 && i < units.size - 1) { v /= 1024; i++ }
    return if (i == 0) "$bytes B" else "%.1f %s".format(v, units[i])
}

private fun ago(iso: String): String {
    val t = runCatching { Instant.parse(iso) }.getOrNull() ?: return "never"
    if (t.epochSecond <= 0) return "never"
    val s = Duration.between(t, Instant.now()).seconds
    return when {
        s < 60 -> "just now"
        s < 3600 -> "${s / 60} min ago"
        s < 86400 -> "${s / 3600} h ago"
        else -> "${s / 86400} d ago"
    }
}
