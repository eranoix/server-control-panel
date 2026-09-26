package com.vpsmanager.data.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment

@RunWith(RobolectricTestRunner::class)
class OndeEuEstavaTest {

    private val app get() = RuntimeEnvironment.getApplication()

    @Test
    fun `a rota volta uma vez e some`() {
        val store = OndeEuEstava(app)
        store.lembrar("deploys")

        assertEquals("deploys", store.consumir())
        // Second read: nothing. The route describes a RETURN, not a
        // preference — reappearing on some future opening would take the
        // person to a screen they never asked for.
        assertNull(store.consumir())
    }

    @Test
    fun `rota vazia apaga em vez de gravar vazio`() {
        val store = OndeEuEstava(app)
        store.lembrar("terminal")
        store.lembrar("   ")
        assertNull(store.consumir())
    }

    @Test
    fun `rota velha nao sequestra uma abertura futura`() {
        var relogio = 1_000_000L
        val store = OndeEuEstava(app, agora = { relogio })
        store.lembrar("whatsapp")

        relogio += 11 * 60 * 1000L // onze minutos depois
        assertNull("passado o prazo, a rota nao vale mais", store.consumir())
    }

    @Test
    fun `dentro do prazo a rota ainda vale`() {
        var relogio = 1_000_000L
        val store = OndeEuEstava(app, agora = { relogio })
        store.lembrar("arquivos")

        relogio += 30_000L // trinta segundos: o tempo de uma instalacao
        assertEquals("arquivos", store.consumir())
    }

    @Test
    fun `esquecer limpa sem precisar consumir`() {
        val store = OndeEuEstava(app)
        store.lembrar("notificacoes")
        store.esquecer()
        assertNull(store.consumir())
    }
}
