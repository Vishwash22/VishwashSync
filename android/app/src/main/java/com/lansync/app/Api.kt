package com.lansync.app

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** Client for the Go core's local REST API (the same API the desktop web UI uses). */
object Api {
    private const val BASE = "http://127.0.0.1:8384"

    data class Status(val deviceID: String, val deviceName: String)
    data class FolderStatus(
        val state: String, val lastSync: String, val lastError: String,
        val files: Int, val bytes: Long, val done: Int, val total: Int,
    )
    data class Folder(
        val id: String, val label: String, val path: String, val mode: String,
        val devices: List<String>, val status: FolderStatus,
    )
    data class Device(val id: String, val name: String, val addr: String, val seenAddr: String, val online: Boolean)
    data class Devices(val known: List<Device>, val discovered: List<Device>)
    data class Offer(val deviceID: String, val deviceName: String, val folderID: String, val label: String)

    class ApiException(message: String) : Exception(message)

    private suspend fun call(method: String, path: String, body: JSONObject? = null): String =
        withContext(Dispatchers.IO) {
            val conn = URL(BASE + path).openConnection() as HttpURLConnection
            conn.requestMethod = method
            conn.connectTimeout = 3000
            conn.readTimeout = 20000
            if (body != null) {
                conn.doOutput = true
                conn.setRequestProperty("Content-Type", "application/json")
                conn.outputStream.use { it.write(body.toString().toByteArray()) }
            }
            val code = conn.responseCode
            val stream = if (code >= 400) conn.errorStream else conn.inputStream
            val text = stream?.bufferedReader()?.use { it.readText() } ?: ""
            if (code >= 400) {
                throw ApiException(runCatching { JSONObject(text).getString("error") }.getOrDefault("HTTP $code"))
            }
            text
        }

    private fun JSONArray.objects() = (0 until length()).map { getJSONObject(it) }
    private fun JSONArray?.strings() = if (this == null) emptyList() else (0 until length()).map { getString(it) }
    private fun enc(s: String) = URLEncoder.encode(s, "UTF-8").replace("+", "%20")

    private fun device(o: JSONObject) = Device(
        o.getString("id"), o.optString("name"), o.optString("addr"), o.optString("seenAddr"), o.optBoolean("online"),
    )

    suspend fun status(): Status = JSONObject(call("GET", "/api/status")).let {
        Status(it.getString("deviceID"), it.getString("deviceName"))
    }

    suspend fun setDeviceName(name: String) {
        call("PUT", "/api/status", JSONObject().put("deviceName", name))
    }

    suspend fun folders(): List<Folder> = JSONArray(call("GET", "/api/folders")).objects().map { o ->
        val s = o.getJSONObject("status")
        Folder(
            o.getString("id"), o.getString("label"), o.getString("path"), o.getString("mode"),
            o.optJSONArray("devices").strings(),
            FolderStatus(
                s.getString("state"), s.optString("lastSync"), s.optString("lastError"),
                s.optInt("files"), s.optLong("bytes"), s.optInt("done"), s.optInt("total"),
            ),
        )
    }

    suspend fun saveFolder(id: String, label: String, path: String, mode: String, devices: List<String>) {
        call(
            "POST", "/api/folders",
            JSONObject().put("id", id).put("label", label).put("path", path).put("mode", mode)
                .put("devices", JSONArray(devices)),
        )
    }

    suspend fun removeFolder(id: String) { call("DELETE", "/api/folders/${enc(id)}") }

    suspend fun syncFolder(id: String) { call("POST", "/api/folders/${enc(id)}/sync") }

    suspend fun devices(): Devices = JSONObject(call("GET", "/api/devices")).let { o ->
        Devices(o.getJSONArray("known").objects().map(::device), o.getJSONArray("discovered").objects().map(::device))
    }

    suspend fun addDevice(id: String, name: String, addr: String) {
        call("POST", "/api/devices", JSONObject().put("id", id).put("name", name).put("addr", addr))
    }

    suspend fun removeDevice(id: String) { call("DELETE", "/api/devices/${enc(id)}") }

    suspend fun discover() { call("POST", "/api/discover") }

    suspend fun offers(): List<Offer> = JSONArray(call("GET", "/api/offers")).objects().map {
        Offer(it.getString("deviceID"), it.getString("deviceName"), it.getString("folderID"), it.getString("label"))
    }
}
