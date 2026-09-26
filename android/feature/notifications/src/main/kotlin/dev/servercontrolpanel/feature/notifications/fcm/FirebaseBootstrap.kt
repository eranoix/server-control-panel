package dev.servercontrolpanel.feature.notifications.fcm

import android.content.Context
import android.util.Log
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import org.json.JSONObject

private const val TAG_BOOT = "VpsFirebaseBoot"

/** Asset file name the Firebase console config is placed under. */
internal const val GOOGLE_SERVICES_FILE = "google-services.json"

/**
 * Initialises Firebase at runtime from `app/src/main/assets/google-services.json`, and does
 * nothing when the file is absent.
 *
 * Runtime parsing is used instead of the google-services Gradle plugin because the plugin
 * fails the build when the file is missing; this way push is an optional feature.
 * None of the four fields is secret: the client `api_key` is public by design, and sending
 * is authorised by the server's service account.
 */
object FirebaseBootstrap {

    /** Returns `true` if Firebase started. Safe to call more than once. */
    fun install(context: Context): Boolean {
        val raw = readAsset(context) ?: run {
            Log.i(
                TAG_BOOT,
                "native push disabled: put the $GOOGLE_SERVICES_FILE from the Firebase " +
                    "console in app/src/main/assets/ (the project needs the package " +
                    "tech.northwind.servercontrolpanel)",
            )
            return false
        }
        val options = optionsFrom(raw, context.packageName) ?: run {
            // A present but unusable file means someone thinks push is configured, so log an error.
            Log.e(TAG_BOOT, "$GOOGLE_SERVICES_FILE present but has no fields for package ${context.packageName}")
            return false
        }
        return runCatching {
            if (FirebaseApp.getApps(context).isEmpty()) {
                FirebaseApp.initializeApp(context, options)
            }
            // Fetch the token now so the device registers on first run, not only when FCM rotates it.
            FirebaseMessaging.getInstance().token.addOnSuccessListener { token ->
                VpsFirebaseMessagingService.registerTokenDetached(context, token)
            }
            true
        }.getOrElse { e ->
            Log.e(TAG_BOOT, "Firebase failed to start: ${e.message}")
            false
        }
    }

    private fun readAsset(context: Context): String? = runCatching {
        context.assets.open(GOOGLE_SERVICES_FILE).bufferedReader().use { it.readText() }
    }.getOrNull()

    /**
     * Extracts options from the console JSON for the client whose `package_name` matches.
     * The file may list several apps; picking the wrong one means pushes never arrive.
     */
    internal fun optionsFrom(json: String, packageName: String): FirebaseOptions? = runCatching {
        val root = JSONObject(json)
        val project = root.getJSONObject("project_info")
        val clients = root.getJSONArray("client")
        for (i in 0 until clients.length()) {
            val client = clients.getJSONObject(i)
            val info = client.getJSONObject("client_info")
            if (info.getJSONObject("android_client_info").getString("package_name") != packageName) {
                continue
            }
            val apiKey = client.getJSONArray("api_key").getJSONObject(0).getString("current_key")
            return@runCatching FirebaseOptions.Builder()
                .setProjectId(project.getString("project_id"))
                .setApplicationId(info.getString("mobilesdk_app_id"))
                .setApiKey(apiKey)
                .setGcmSenderId(project.getString("project_number"))
                .build()
        }
        null
    }.getOrNull()
}
