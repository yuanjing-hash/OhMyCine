package com.ohmycine.player.credentials

import android.app.Activity
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

@InvokeArg
class StoreMasterKeyArgs {
    lateinit var key: String
}

@TauriPlugin
class CredentialPlugin(private val activity: Activity) : Plugin(activity) {
    private val preferences by lazy {
        activity.getSharedPreferences(PREFERENCES_NAME, Activity.MODE_PRIVATE)
    }

    @Command
    fun getMasterKey(invoke: Invoke) = resolve(invoke) {
        mapOf("key" to readMasterKey())
    }

    @Command
    fun createMasterKey(invoke: Invoke) = resolve(invoke) {
        val existing = readMasterKey()
        if (existing != null)
            return@resolve mapOf("key" to existing)

        val key = ByteArray(MASTER_KEY_BYTES).also(SecureRandom()::nextBytes)
        storeMasterKeyBytes(key)
        mapOf("key" to Base64.encodeToString(key, Base64.NO_WRAP))
    }

    @Command
    fun storeMasterKey(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(StoreMasterKeyArgs::class.java)
        val key = Base64.decode(args.key, Base64.DEFAULT)
        require(key.size == MASTER_KEY_BYTES) { "Credential master key is invalid." }
        storeMasterKeyBytes(key)
        mapOf("stored" to true)
    }

    private fun readMasterKey(): String? {
        val encoded = preferences.getString(PREFERENCES_KEY, null) ?: return null
        val payload = Base64.decode(encoded, Base64.DEFAULT)
        require(payload.size > IV_BYTES) { "Stored credential master key is invalid." }
        val iv = payload.copyOfRange(0, IV_BYTES)
        val ciphertext = payload.copyOfRange(IV_BYTES, payload.size)
        val cipher = Cipher.getInstance(CIPHER_TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, keystoreKey(), GCMParameterSpec(GCM_TAG_BITS, iv))
        cipher.updateAAD(AAD)
        val plaintext = cipher.doFinal(ciphertext)
        require(plaintext.size == MASTER_KEY_BYTES) { "Stored credential master key is invalid." }
        return Base64.encodeToString(plaintext, Base64.NO_WRAP)
    }

    private fun storeMasterKeyBytes(key: ByteArray) {
        val cipher = Cipher.getInstance(CIPHER_TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, keystoreKey())
        cipher.updateAAD(AAD)
        val ciphertext = cipher.doFinal(key)
        val payload = cipher.iv + ciphertext
        check(preferences.edit().putString(PREFERENCES_KEY, Base64.encodeToString(payload, Base64.NO_WRAP)).commit()) {
            "Credential master key could not be stored."
        }
    }

    private fun keystoreKey(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }

        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    private fun resolve(invoke: Invoke, block: () -> Map<String, Any?>) {
        try {
            invoke.resolveObject(block())
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android secure credential storage failed.")
        }
    }

    companion object {
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val CIPHER_TRANSFORMATION = "AES/GCM/NoPadding"
        private const val KEY_ALIAS = "com.ohmycine.player.credential-master-key.v1"
        private const val PREFERENCES_NAME = "ohmycine_secure_credentials"
        private const val PREFERENCES_KEY = "credential_master_key_v1"
        private const val MASTER_KEY_BYTES = 32
        private const val IV_BYTES = 12
        private const val GCM_TAG_BITS = 128
        private val AAD = "com.ohmycine.player:credential-master-key:v1".toByteArray(Charsets.UTF_8)
    }
}
