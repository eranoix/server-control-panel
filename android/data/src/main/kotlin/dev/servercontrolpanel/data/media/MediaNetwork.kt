package dev.servercontrolpanel.data.media

import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import okhttp3.Call
import okhttp3.Interceptor
import okhttp3.Response

private object MediaAuthInterceptor : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val token = ApiClient.accessToken
        val request = if (token != null) {
            chain.request().newBuilder().header("Authorization", "Bearer $token").build()
        } else {
            chain.request()
        }
        return chain.proceed(request)
    }
}

fun mediaCallFactory(): Call.Factory =
    ApiClient.defaultClient.newBuilder().addInterceptor(MediaAuthInterceptor).build()
