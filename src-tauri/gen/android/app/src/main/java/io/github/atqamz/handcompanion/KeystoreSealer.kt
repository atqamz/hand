package io.github.atqamz.handcompanion

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import java.security.KeyStore
import java.security.UnrecoverableKeyException
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class SealError(val code: String, message: String) : Exception(message)

object KeystoreSealer {
  @Synchronized
  fun probe() {
    try {
      if (key(create = false) == null) {
        throw SealError(CODE_KEY_REPLACED, "the Android Keystore key that sealed the identity is gone")
      }
    } catch (error: SealError) {
      throw error
    } catch (error: Exception) {
      throw SealError(CODE_KEYSTORE, error.message ?: "the Android Keystore could not read the key")
    }
  }

  @Synchronized
  fun seal(plaintext: ByteArray): ByteArray {
    try {
      return encrypt(plaintext)
    } catch (error: Exception) {
      throw SealError(CODE_KEYSTORE, error.message ?: "the Android Keystore could not encrypt the key")
    }
  }

  @Synchronized
  fun open(sealed: ByteArray): ByteArray {
    if (sealed.size < IV_BYTES + TAG_BYTES + 1) {
      throw SealError(CODE_CORRUPT, "the stored key is not IV, ciphertext and tag")
    }
    val key = try {
      key(create = false)
    } catch (error: KeyPermanentlyInvalidatedException) {
      throw SealError(CODE_KEY_REPLACED, "the Android Keystore key that sealed the identity is gone")
    } catch (error: Exception) {
      throw SealError(CODE_KEYSTORE, error.message ?: "the Android Keystore could not read the key")
    }
    if (key == null) {
      throw SealError(CODE_KEY_REPLACED, "the Android Keystore key that sealed the identity is gone")
    }
    try {
      val cipher = Cipher.getInstance(TRANSFORMATION)
      cipher.init(
        Cipher.DECRYPT_MODE,
        key,
        GCMParameterSpec(TAG_BITS, sealed.copyOfRange(0, IV_BYTES))
      )
      return cipher.doFinal(sealed.copyOfRange(IV_BYTES, sealed.size))
    } catch (error: KeyPermanentlyInvalidatedException) {
      throw SealError(CODE_KEY_REPLACED, "the Android Keystore key that sealed the identity is gone")
    } catch (error: AEADBadTagException) {
      throw SealError(CODE_CORRUPT, "the stored key failed authentication")
    } catch (error: Exception) {
      throw SealError(CODE_KEYSTORE, error.message ?: "the Android Keystore could not open the key")
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

  private const val KEYSTORE = "AndroidKeyStore"
  private const val ALIAS = "io.github.atqamz.handcompanion.iroh"
  private const val TRANSFORMATION = "AES/GCM/NoPadding"
  private const val KEY_BITS = 256
  private const val TAG_BITS = 128
  private const val TAG_BYTES = TAG_BITS / 8
  private const val IV_BYTES = 12
  private const val CODE_KEY_REPLACED = "keyReplaced"
  private const val CODE_CORRUPT = "corrupt"
  private const val CODE_KEYSTORE = "keystore"
}
