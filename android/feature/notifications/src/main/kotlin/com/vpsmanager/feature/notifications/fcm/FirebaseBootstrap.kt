package com.vpsmanager.feature.notifications.fcm

import android.content.Context
import android.util.Log
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import org.json.JSONObject

private const val TAG_BOOT = "VpsFirebaseBoot"

/** Where the owner drops the file the Firebase console hands over. */
internal const val ARQUIVO_GOOGLE_SERVICES = "google-services.json"

/**
 * Brings Firebase up from the `google-services.json` placed in
 * `app/src/main/assets/` — and does nothing at all when it is not there.
 *
 * ## Why read the file instead of applying the plugin
 *
 * The usual Android route is the `com.google.gms.google-services` plugin,
 * which reads the same file at BUILD time and generates resources. That would
 * be expensive here: the plugin becomes part of everyone's Gradle graph — CI,
 * the emulator, the machine of someone who only wants to compile — and a build
 * that is green today would start depending on downloading and resolving one
 * more plugin. Worse: without the file present, the plugin FAILS the build
 * rather than degrading.
 *
 * Read at runtime, the app keeps compiling and running exactly as it does
 * today for as long as the Firebase project does not exist, and lights up by
 * itself the minute the file appears — without touching any build.
 *
 * ## What happens without the file
 *
 * Nothing, and on purpose: no default `FirebaseApp` is born, and
 * `FirebaseMessaging`/[VpsFirebaseMessagingService] are simply never called by
 * the system. It is the SAME clean degradation as on the server side, where a
 * missing `fcm_service_account` switches sending off and logs the exact
 * instruction instead of bringing the process down. Native push is one extra
 * feature; its absence cannot stop the app.
 *
 * ## The four fields
 *
 * They are the ones `FirebaseOptions` requires for Cloud Messaging, and all of
 * them are in the console's JSON — none is a secret (the client `api_key` is
 * public by design; what authorises SENDING is the service account, which
 * lives only in the server's vault).
 */
object FirebaseBootstrap {

    /**
     * Returns `true` if Firebase came up. Called once at app boot; it is safe
     * to call again (the SDK returns the app that already exists).
     */
    fun instalar(context: Context): Boolean {
        val bruto = lerAsset(context) ?: run {
            Log.i(
                TAG_BOOT,
                "push nativo desligado: ponha o $ARQUIVO_GOOGLE_SERVICES do console do " +
                    "Firebase em app/src/main/assets/ (o projeto precisa do pacote " +
                    "tech.northwind.vpsm.app)",
            )
            return false
        }
        val opcoes = opcoesDe(bruto, context.packageName) ?: run {
            // A file that is present and useless is worse than an absent
            // one: somebody believes they configured it. Which is why this
            // branch shouts instead of whispering.
            Log.e(TAG_BOOT, "$ARQUIVO_GOOGLE_SERVICES presente mas sem os campos do pacote ${context.packageName}")
            return false
        }
        return runCatching {
            if (FirebaseApp.getApps(context).isEmpty()) {
                FirebaseApp.initializeApp(context, opcoes)
            }
            // Asking for the token NOW is what fires `onNewToken` on the
            // first run; without this the device would only register itself on
            // the day FCM decided to rotate the key on its own.
            FirebaseMessaging.getInstance().token.addOnSuccessListener { token ->
                VpsFirebaseMessagingService.registrarTokenAvulso(context, token)
            }
            true
        }.getOrElse { e ->
            Log.e(TAG_BOOT, "Firebase não subiu: ${e.message}")
            false
        }
    }

    private fun lerAsset(context: Context): String? = runCatching {
        context.assets.open(ARQUIVO_GOOGLE_SERVICES).bufferedReader().use { it.readText() }
    }.getOrNull()

    /**
     * Extracts the options from the console's JSON, picking the client whose
     * `package_name` matches the app's.
     *
     * Matching the package is not fussiness: the console file may describe
     * SEVERAL apps from the same project, and taking the first one would
     * register the device under the wrong identity — the push would go out and
     * never arrive.
     */
    internal fun opcoesDe(json: String, packageName: String): FirebaseOptions? = runCatching {
        val raiz = JSONObject(json)
        val projeto = raiz.getJSONObject("project_info")
        val clientes = raiz.getJSONArray("client")
        for (i in 0 until clientes.length()) {
            val cliente = clientes.getJSONObject(i)
            val info = cliente.getJSONObject("client_info")
            if (info.getJSONObject("android_client_info").getString("package_name") != packageName) {
                continue
            }
            val apiKey = cliente.getJSONArray("api_key").getJSONObject(0).getString("current_key")
            return@runCatching FirebaseOptions.Builder()
                .setProjectId(projeto.getString("project_id"))
                .setApplicationId(info.getString("mobilesdk_app_id"))
                .setApiKey(apiKey)
                .setGcmSenderId(projeto.getString("project_number"))
                .build()
        }
        null
    }.getOrNull()
}
