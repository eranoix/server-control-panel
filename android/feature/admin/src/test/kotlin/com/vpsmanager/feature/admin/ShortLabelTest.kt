package com.vpsmanager.feature.admin

import org.junit.Assert.assertEquals
import org.junit.Test

class ShortLabelTest {

    @Test
    fun `enumeracao entre parenteses sai — era ela que ocupava duas linhas`() {
        // The case from the owner's screenshot: "Métricas (CPU,\nmemória, dis…"
        // ate the whole block and the cut fell on the detail, not on the name.
        assertEquals("Métricas", shortLabel("Métricas (CPU, memória, disco)"))
    }

    @Test
    fun `sigla FICA — ela e o nome pelo qual a coisa e conhecida`() {
        assertEquals("Firewall (UFW)", shortLabel("Firewall (UFW)"))
        assertEquals("AdGuard (DNS)", shortLabel("AdGuard (DNS)"))
        assertEquals("Modelos (IA)", shortLabel("Modelos (IA)"))
    }

    @Test
    fun `rotulo sem parenteses passa intacto`() {
        assertEquals("Containers", shortLabel("Containers"))
        assertEquals("Fila de jobs", shortLabel("Fila de jobs"))
    }

    @Test
    fun `parentese no MEIO nao deixa buraco nem espaco duplo`() {
        assertEquals("Serviços do sistema", shortLabel("Serviços (systemd e afins) do sistema"))
    }

    @Test
    fun `se o parentese ERA o nome, o original volta`() {
        // Cutting would leave the block with no name — worse than a cut name.
        assertEquals("(sem rótulo definido)", shortLabel("(sem rótulo definido)"))
    }

    @Test
    fun `parentese sem fechamento nao quebra`() {
        assertEquals("Métricas (CPU", shortLabel("Métricas (CPU"))
    }

    @Test
    fun `espacos em volta somem`() {
        assertEquals("Processos", shortLabel("  Processos  "))
    }
}
