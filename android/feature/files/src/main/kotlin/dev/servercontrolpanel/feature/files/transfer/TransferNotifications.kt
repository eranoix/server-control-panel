package dev.servercontrolpanel.feature.files.transfer

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import androidx.core.app.NotificationCompat
import androidx.work.ForegroundInfo
import androidx.work.WorkManager
import java.util.UUID

internal object TransferNotifications {
    const val CHANNEL_ID = "file_transfers"
    private const val CHANNEL_NAME = "File transfers"

    private fun ensureChannel(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, CHANNEL_NAME, NotificationManager.IMPORTANCE_LOW),
        )
    }

    fun foregroundInfo(context: Context, workId: UUID, title: String, percent: Int): ForegroundInfo {
        ensureChannel(context)
        val cancelIntent = WorkManager.getInstance(context).createCancelPendingIntent(workId)
        val builder = NotificationCompat.Builder(context, CHANNEL_ID)
            .setContentTitle(title)
            .setSmallIcon(android.R.drawable.stat_sys_download)
            .setOngoing(true)
            .addAction(android.R.drawable.ic_menu_close_clear_cancel, "Cancel", cancelIntent)
        if (percent in 0..100) {
            builder.setProgress(100, percent, false)
            builder.setContentText("$percent%")
        } else {
            builder.setProgress(0, 0, true)
            builder.setContentText("In progress…")
        }
        return ForegroundInfo(workId.hashCode(), builder.build(), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
    }
}
