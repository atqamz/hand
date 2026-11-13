package io.github.atqamz.handcompanion

import java.net.URLEncoder
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

const val API_VERSION = 1

sealed class BoardError(message: String? = null) : Exception(message) {
  data object Busy : BoardError()

  data object NoLaptop : BoardError()

  data object Unreachable : BoardError()

  data object Unpaired : BoardError()

  data object Lost : BoardError()

  data object BoardNotRunning : BoardError()

  data object TimedOut : BoardError()

  data object TooLarge : BoardError()

  data object Malformed : BoardError()

  class UnsupportedVersion(val version: Int) : BoardError("API version $version")

  class AppliedUnread(cause: Throwable) : BoardError("the Board answered 200 but the answer was not read") {
    init {
      initCause(cause)
    }
  }

  class BadRequest(message: String?) : BoardError(message)

  class BadCursor(message: String?) : BoardError(message)

  class Unauthorized(message: String?) : BoardError(message)

  class Forbidden(message: String?) : BoardError(message)

  class CsrfRequired(message: String?) : BoardError(message)

  class NotFound(message: String?) : BoardError(message)

  class MethodNotAllowed(message: String?) : BoardError(message)

  class Conflict(message: String?) : BoardError(message)

  class Internal(message: String?) : BoardError(message)

  class NoSpace(message: String?) : BoardError(message)

  class Other(val status: Int, val code: String?, message: String?) : BoardError(message)
}

class Raw(val status: Int, val body: String)

fun interface Transport {
  fun request(fleet: String, post: Boolean, path: String, csrf: String?, body: String?): Raw
}

@Serializable class FleetRef(val id: String, val name: String)

@Serializable
class Envelope<T>(
  val version: Int,
  val fleet: FleetRef,
  val cursor: String? = null,
  @SerialName("generated_at") val generatedAt: String? = null,
  val data: T
)

sealed interface Write<out T> {
  class Updated<T>(val envelope: Envelope<T>) : Write<T>

  data object Accepted : Write<Nothing>
}

sealed interface Poll<out T> {
  class Changed<T>(val envelope: Envelope<T>) : Poll<T>

  data object Unchanged : Poll<Nothing>
}

@Serializable private class ErrorBody(val error: ErrorDetail)

@Serializable private class ErrorDetail(val code: String, val message: String? = null)

@Serializable private class SessionData(val csrf: String)

private val json = Json { ignoreUnknownKeys = true }

fun parseWire(text: String?): Raw =
  try {
    val wire = json.parseToJsonElement(text.orEmpty()).jsonObject
    wire["failure"]?.jsonPrimitive?.content?.let { throw failure(it) }
    Raw(wire.getValue("status").jsonPrimitive.int, wire.getValue("body").jsonPrimitive.content)
  } catch (error: BoardError) {
    throw error
  } catch (_: Exception) {
    throw BoardError.Malformed
  }

private fun failure(name: String): BoardError =
  when (name) {
    "busy" -> BoardError.Busy
    "no_laptop" -> BoardError.NoLaptop
    "unreachable" -> BoardError.Unreachable
    "unpaired" -> BoardError.Unpaired
    "lost" -> BoardError.Lost
    "board_not_running" -> BoardError.BoardNotRunning
    "timed_out" -> BoardError.TimedOut
    "too_large" -> BoardError.TooLarge
    else -> BoardError.Malformed
  }

private fun refusal(raw: Raw): BoardError {
  val detail =
    try {
      json.decodeFromString<ErrorBody>(raw.body).error
    } catch (_: Exception) {
      return BoardError.Other(raw.status, null, null)
    }
  val message = detail.message
  return when (detail.code) {
    "bad_request" -> BoardError.BadRequest(message)
    "bad_cursor" -> BoardError.BadCursor(message)
    "unauthorized" -> BoardError.Unauthorized(message)
    "forbidden" -> BoardError.Forbidden(message)
    "csrf_required" -> BoardError.CsrfRequired(message)
    "not_found" -> BoardError.NotFound(message)
    "method_not_allowed" -> BoardError.MethodNotAllowed(message)
    "conflict" -> BoardError.Conflict(message)
    "internal" -> BoardError.Internal(message)
    "no_space" -> BoardError.NoSpace(message)
    else -> BoardError.Other(raw.status, detail.code, message)
  }
}

class BoardClient(
  private val fleet: String,
  private val transport: Transport,
  private val io: CoroutineDispatcher = Dispatchers.IO
) {
  @Volatile private var statusCursor: String? = null
  @Volatile private var csrf: String? = null

  suspend fun status(): Poll<Status> {
    val since = statusCursor?.let { "?since=" + URLEncoder.encode(it, "UTF-8") }.orEmpty()
    val raw =
      try {
        send(false, "/api/v1/status$since", null)
      } catch (error: BoardError.BadCursor) {
        statusCursor = null
        throw error
      }
    if (raw.status == 304) return Poll.Unchanged
    val envelope = decode(raw, Status.serializer())
    statusCursor = envelope.cursor
    return Poll.Changed(envelope)
  }

  suspend fun <T> read(path: String, data: KSerializer<T>): Envelope<T> =
    decode(send(false, "/api/v1/${path.trimStart('/')}", null), data)

  suspend fun <T> write(path: String, body: String?, data: KSerializer<T>): Write<T> {
    val raw =
      try {
        sendWrite(path, body)
      } catch (_: BoardError.CsrfRequired) {
        csrf = null
        sendWrite(path, body)
      }
    return try {
      val root = parse(raw)
      if (root["data"].let { it is JsonObject && it["accepted"] == JsonPrimitive(true) }) {
        Write.Accepted
      } else {
        Write.Updated(json.decodeFromJsonElement(Envelope.serializer(data), root))
      }
    } catch (error: Exception) {
      throw BoardError.AppliedUnread(error)
    }
  }

  private suspend fun sendWrite(path: String, body: String?): Raw =
    send(true, "/api/v1/${path.trimStart('/')}", body.orEmpty(), token())

  private suspend fun token(): String =
    csrf
      ?: decode(send(false, "/api/v1/session", null), SessionData.serializer()).data.csrf.also {
        csrf = it
      }

  private suspend fun send(
    post: Boolean,
    path: String,
    body: String?,
    token: String? = null
  ): Raw {
    val raw = withContext(io) { transport.request(fleet, post, path, token, body) }
    if (raw.status != 200 && raw.status != 304) throw refusal(raw)
    return raw
  }

  private fun parse(raw: Raw): JsonObject {
    val root: JsonObject =
      try {
        json.parseToJsonElement(raw.body).jsonObject
      } catch (_: Exception) {
        throw BoardError.Malformed
      }
    val version =
      try {
        root["version"]?.jsonPrimitive?.int
      } catch (_: Exception) {
        null
      } ?: throw BoardError.Malformed
    if (version != API_VERSION) throw BoardError.UnsupportedVersion(version)
    val id =
      try {
        root["fleet"]?.jsonObject?.get("id")?.jsonPrimitive?.content
      } catch (_: Exception) {
        null
      }
    if (id != fleet) throw BoardError.Malformed
    return root
  }

  private fun <T> decode(raw: Raw, data: KSerializer<T>): Envelope<T> {
    val root = parse(raw)
    return try {
      json.decodeFromJsonElement(Envelope.serializer(data), root)
    } catch (_: Exception) {
      throw BoardError.Malformed
    }
  }
}

object CoreTransport : Transport {
  override fun request(
    fleet: String,
    post: Boolean,
    path: String,
    csrf: String?,
    body: String?
  ): Raw = parseWire(Core.request(fleet, post, path, csrf, body?.toByteArray()))
}
