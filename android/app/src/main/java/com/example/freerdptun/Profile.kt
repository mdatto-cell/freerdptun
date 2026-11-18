package com.example.freerdptun

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

data class Profile(
    val id: String = UUID.randomUUID().toString(),
    val name: String,
    val config: TunnelConfig,
    /** Параметры приложения (вне JSON). */
    val dnsServers: String = "1.1.1.1, 8.8.8.8",
    val blockIPv6: Boolean = true,
)

/** Профили в SharedPreferences (JSON). Хранятся без шифрования — приложение тестовое. */
class ProfileStore(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences("profiles", Context.MODE_PRIVATE)

    var profiles by mutableStateOf<List<Profile>>(emptyList())
        private set
    var defaultId by mutableStateOf<String?>(null)
        private set

    init {
        runCatching {
            val arr = JSONArray(prefs.getString("list", "[]"))
            profiles = (0 until arr.length()).map { decode(arr.getJSONObject(it)) }
        }
        val d = prefs.getString("default", null)
        defaultId = if (profiles.any { it.id == d }) d else profiles.firstOrNull()?.id
    }

    val defaultProfile: Profile? get() = profiles.firstOrNull { it.id == defaultId }

    fun add(p: Profile) {
        profiles = profiles + p
        if (defaultId == null) defaultId = p.id
        save()
    }

    fun update(p: Profile) {
        profiles = profiles.map { if (it.id == p.id) p else it }
        save()
    }

    fun delete(id: String) {
        profiles = profiles.filter { it.id != id }
        if (defaultId == id) defaultId = profiles.firstOrNull()?.id
        save()
    }

    fun setDefault(id: String) {
        if (profiles.any { it.id == id }) {
            defaultId = id
            save()
        }
    }

    private fun save() {
        val arr = JSONArray()
        profiles.forEach { arr.put(encode(it)) }
        prefs.edit().putString("list", arr.toString()).putString("default", defaultId).apply()
    }

    private fun encode(p: Profile) = JSONObject()
        .put("id", p.id).put("name", p.name).put("config", p.config.jsonString())
        .put("dns", p.dnsServers).put("blockIPv6", p.blockIPv6)

    private fun decode(o: JSONObject) = Profile(
        id = o.getString("id"), name = o.getString("name"),
        config = TunnelConfig.parse(o.getString("config")),
        dnsServers = o.optString("dns", "1.1.1.1, 8.8.8.8"), blockIPv6 = o.optBoolean("blockIPv6", true),
    )
}
