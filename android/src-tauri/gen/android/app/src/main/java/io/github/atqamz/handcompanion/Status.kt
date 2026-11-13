package io.github.atqamz.handcompanion

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable data class Gauge(val text: String, val level: String, val pct: Int)

@Serializable data class AskOption(val label: String, val keys: List<String>)

@Serializable data class Ask(val question: String, val options: List<AskOption>)

@Serializable data class Keys(val revision: Long, val digest: String, val pressed: Long)

@Serializable
data class Supervisor(
  val ref: String,
  val harness: String,
  val model: String? = null,
  val effort: String? = null,
  val status: String,
  val live: Boolean,
  val pending: Int,
  val resumable: Boolean,
  val blocked: Boolean = false,
  val keys: Keys? = null,
  val ask: Ask? = null,
  val held: Boolean = false,
  val switching: String? = null,
  val reason: String? = null,
  @SerialName("deny_at") val denyAt: String? = null
)

@Serializable
data class Need(
  val kind: String,
  val ref: String? = null,
  val title: String? = null,
  val word: String,
  val tone: String,
  val task: String? = null,
  val decision: String? = null,
  val report: String? = null,
  val attempt: String? = null,
  val headline: String? = null,
  val status: String? = null,
  @SerialName("created_at") val createdAt: String? = null,
  val excerpt: String? = null,
  val cut: Boolean = false,
  @SerialName("deny_at") val denyAt: String? = null,
  val options: List<AskOption>? = null
)

@Serializable
data class Needs(
  val waiting: Int,
  val more: Int,
  val worst: String? = null,
  @SerialName("worst_word") val worstWord: String? = null,
  @SerialName("worst_count") val worstCount: Int,
  val cleared: String? = null,
  val summary: String? = null,
  val items: List<Need>
)

@Serializable
data class TaskCounts(val active: Int, val inbox: Int, val done: Int, val abandoned: Int)

@Serializable
data class Status(
  val supervisor: Supervisor? = null,
  @SerialName("agent_state") val agentState: String,
  @SerialName("composer_hint") val composerHint: String,
  val pill: String,
  val line: String,
  val gauge: Gauge,
  val compactions: Int,
  val needs: Needs,
  val tasks: TaskCounts,
  val projects: Int
)
