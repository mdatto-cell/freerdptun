package com.example.freerdptun

import android.content.Context
import java.io.File

/** JNI-мост к Go-ядру (libfrtcore.so) и встроенному FreeRDP-транспорту (libfrtjni.so). */
object Native {
    private var ready = false
    lateinit var logFile: File
        private set

    init {
        System.loadLibrary("frtcore")
        System.loadLibrary("frtjni")
    }

    @Synchronized
    fun ensureInit(ctx: Context) {
        if (ready) return
        logFile = File(ctx.applicationContext.filesDir, "frt.log")
        init(logFile.absolutePath)
        ready = true
    }

    /** Перенаправляет stderr в файл журнала и регистрирует FreeRDP-транспорт. */
    private external fun init(logPath: String)

    /** null при успехе, иначе текст ошибки. */
    external fun start(cfg: String): String?
    external fun stop()

    /** 0 ожидание, 1 подключение, 2 подключено, 3 остановлено. */
    external fun status(): Int
    external fun lastError(): String

    external fun push(buf: ByteArray, len: Int)

    /** Длина пакета, 0 по таймауту, -1 если туннель завершён. */
    external fun pop(buf: ByteArray, timeoutMs: Int): Int
}
