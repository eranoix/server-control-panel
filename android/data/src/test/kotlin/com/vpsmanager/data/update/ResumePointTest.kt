package com.vpsmanager.data.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

@RunWith(RobolectricTestRunner::class)
class ResumePointTest {

    private val app get() = RuntimeEnvironment.getApplication()

    @Test
    fun `a rota volta uma vez e some`() {
        val store = ResumePoint(app)
        store.save("deploys")

        assertEquals("deploys", store.consume())
        // Second read: nothing. The route describes a RETURN, not a
        // preference — reappearing on some future opening would take the
        // person to a screen they never asked for.
        assertNull(store.consume())
    }

    @Test
    fun `rota vazia apaga em vez de gravar vazio`() {
        val store = ResumePoint(app)
        store.save("terminal")
        store.save("   ")
        assertNull(store.consume())
    }

    @Test
    fun `rota velha nao sequestra uma abertura futura`() {
        var clock = 1_000_000L
        val store = ResumePoint(app, now = { clock })
        store.save("whatsapp")

        clock += 11 * 60 * 1000L // onze minutos depois
        assertNull("passado o prazo, a rota nao vale mais", store.consume())
    }

    @Test
    fun `dentro do prazo a rota ainda vale`() {
        var clock = 1_000_000L
        val store = ResumePoint(app, now = { clock })
        store.save("arquivos")

        clock += 30_000L // trinta segundos: o tempo de uma instalacao
        assertEquals("arquivos", store.consume())
    }

    @Test
    fun `esquecer limpa sem precisar consumir`() {
        val store = ResumePoint(app)
        store.save("notificacoes")
        store.forget()
        assertNull(store.consume())
    }
}
