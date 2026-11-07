package io.github.atqamz.handcompanion

import android.app.Activity
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import java.security.KeyStore
import java.security.UnrecoverableKeyException
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import org.json.JSONArray

@InvokeArg
class KeyBytes {
  var data: List<Int> = emptyList()
}

@TauriPlugin
class KeystorePlugin(activity: Activity) : Plugin(activity) {
  @Command
  @Synchronized
  fun probe(invoke: Invoke) {
    try {
      if (key(create = false) == null) {
        invoke.reject("the Android Keystore key that sealed the identity is gone", CODE_KEY_REPLACED)
      } else {
        invoke.resolve(response(ByteArray(0)))
      }
    } catch (error: Exception) {
      invoke.reject(error.message ?: "the Android Keystore could not read the key", CODE_KEYSTORE)
    }
  }

  @Command
  @Synchronized
  fun seal(invoke: Invoke) {
    try {
      val plaintext = invoke.parseArgs(KeyBytes::class.java).data.toByteArray()
      invoke.resolve(response(encrypt(plaintext)))
    } catch (error: Exception) {
      invoke.reject(error.message ?: "the Android Keystore could not encrypt the key", CODE_KEYSTORE)
    }
  }

  @Command
  @Synchronized
  fun open(invoke: Invoke) {
    val sealed = invoke.parseArgs(KeyBytes::class.java).data.toByteArray()
    if (sealed.size < IV_BYTES + TAG_BYTES + 1) {
      invoke.reject("the stored key is not IV, ciphertext and tag", CODE_CORRUPT)
      return
    }
    val key = try {
      key(create = false)
    } catch (error: KeyPermanentlyInvalidatedException) {
      invoke.reject("the Android Keystore key that sealed the identity is gone", CODE_KEY_REPLACED)
      return
    } catch (error: Exception) {
      invoke.reject(error.message ?: "the Android Keystore could not read the key", CODE_KEYSTORE)
      return
    }
    if (key == null) {
      invoke.reject("the Android Keystore key that sealed the identity is gone", CODE_KEY_REPLACED)
      return
    }
    try {
      val cipher = Cipher.getInstance(TRANSFORMATION)
      cipher.init(
        Cipher.DECRYPT_MODE,
        key,
        GCMParameterSpec(TAG_BITS, sealed.copyOfRange(0, IV_BYTES))
      )
      invoke.resolve(response(cipher.doFinal(sealed.copyOfRange(IV_BYTES, sealed.size))))
    } catch (error: KeyPermanentlyInvalidatedException) {
      invoke.reject("the Android Keystore key that sealed the identity is gone", CODE_KEY_REPLACED)
    } catch (error: AEADBadTagException) {
      invoke.reject("the stored key failed authentication", CODE_CORRUPT)
    } catch (error: Exception) {
      invoke.reject(error.message ?: "the Android Keystore could not open the key", CODE_KEYSTORE)
    }
  }

  private fun encrypt(plaintext: ByteArray): ByteArray {
    try {
      return encryptOnce(plaintext)
    } catch (error: KeyPermanentlyInvalidatedException) {
      deleteKey()
      return encryptOnce(plaintext)
    }
  }

  private fun encryptOnce(plaintext: ByteArray): ByteArray {
    val cipher = Cipher.getInstance(TRANSFORMATION)
    cipher.init(Cipher.ENCRYPT_MODE, key(create = true))
    require(cipher.iv.size == IV_BYTES) {
      "the Android Keystore made a ${cipher.iv.size}-byte IV, not $IV_BYTES"
    }
    return cipher.iv + cipher.doFinal(plaintext)
  }

  private fun key(create: Boolean): SecretKey? {
    val store = KeyStore.getInstance(KEYSTORE)
    store.load(null)
    val existing = try {
      store.getKey(ALIAS, null) as? SecretKey
    } catch (error: KeyPermanentlyInvalidatedException) {
      null
    } catch (error: UnrecoverableKeyException) {
      if (error.cause != null) throw error
      null
    }
    if (existing != null) {
      return existing
    }
    if (!create) {
      return null
    }
    if (store.containsAlias(ALIAS)) {
      store.deleteEntry(ALIAS)
    }
    return generate(store)
  }

  private fun generate(store: KeyStore): SecretKey {
    val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
    generator.init(
      KeyGenParameterSpec.Builder(
        ALIAS,
        KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT
      )
        .setKeySize(KEY_BITS)
        .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
        .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
        .setUserAuthenticationRequired(false)
        .build()
    )
    generator.generateKey()
    return store.getKey(ALIAS, null) as SecretKey
  }

  private fun deleteKey() {
    val store = KeyStore.getInstance(KEYSTORE)
    store.load(null)
    store.deleteEntry(ALIAS)
  }

  private fun response(bytes: ByteArray): JSObject {
    val values = JSONArray()
    bytes.forEach { values.put(it.toInt() and 0xff) }
    return JSObject().put("data", values)
  }

  private fun List<Int>.toByteArray(): ByteArray = ByteArray(size) { get(it).toByte() }

  private companion object {
    const val KEYSTORE = "AndroidKeyStore"
    const val ALIAS = "io.github.atqamz.handcompanion.iroh"
    const val TRANSFORMATION = "AES/GCM/NoPadding"
    const val KEY_BITS = 256
    const val TAG_BITS = 128
    const val TAG_BYTES = TAG_BITS / 8
    const val IV_BYTES = 12
    const val CODE_KEY_REPLACED = "keyReplaced"
    const val CODE_CORRUPT = "corrupt"
    const val CODE_KEYSTORE = "keystore"
  }
}
