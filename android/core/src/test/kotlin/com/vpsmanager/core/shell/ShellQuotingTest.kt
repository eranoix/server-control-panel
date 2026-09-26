package com.vpsmanager.core.shell

import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * The test that matters here is [caminhoComEspacoChegaInteiroNoShell] and its
 * neighbours: they do not compare the STRING the app produces, they run a real
 * `/bin/sh` and check how many arguments the shell handed to the program. It is
 * the only way to prove the property that matters — "the path arrives whole, as
 * ONE argument" — because the only authority on that is a POSIX shell, not our
 * expectation of what the escaping ought to look like.
 */
class ShellQuotingTest {

    @Test
    fun `caminho simples nao ganha aspas`() {
        assertEquals("/opt/panel/data/mobile-inbox/foto.jpg", comAspasParaShell("/opt/panel/data/mobile-inbox/foto.jpg"))
    }

    @Test
    fun `caminho com espaco ganha aspas simples`() {
        assertEquals("'/tmp/Captura de tela.png'", comAspasParaShell("/tmp/Captura de tela.png"))
    }

    @Test
    fun `aspa simples no nome e fechada escapada e reaberta`() {
        assertEquals("""'/tmp/joao'\''s foto.jpg'""", comAspasParaShell("/tmp/joao's foto.jpg"))
    }

    @Test
    fun `texto vazio vira argumento vazio explicito`() {
        assertEquals("''", comAspasParaShell(""))
    }

    @Test
    fun `metacaracteres perigosos sao neutralizados`() {
        // Cada um destes, cru, faria o shell EXECUTAR algo em vez de tratar o
        // texto como nome de arquivo.
        listOf("/tmp/a;rm -rf b", "/tmp/\$(id)", "/tmp/`id`", "/tmp/a|b", "/tmp/a&b", "/tmp/a\nb", "/tmp/a*b").forEach { bruto ->
            val citado = comAspasParaShell(bruto)
            assertTrue("deveria ter aspas: $bruto -> $citado", citado.startsWith("'") && citado.endsWith("'"))
        }
    }

    @Test
    fun `insercao de varios caminhos separa por espaco e termina com espaco`() {
        val texto = textoDeInsercaoParaShell(listOf("/tmp/a.png", "/tmp/b c.png"))
        assertEquals("/tmp/a.png '/tmp/b c.png' ", texto)
    }

    @Test
    fun `insercao nunca termina em quebra de linha`() {
        // A newline here would EXECUTE the operator's command line.
        val texto = textoDeInsercaoParaShell(listOf("/tmp/a.png", "/tmp/b.png"))
        assertTrue(!texto.contains('\n'))
        assertTrue(texto.endsWith(" "))
    }

    @Test
    fun `lista vazia nao insere nada`() {
        assertEquals("", textoDeInsercaoParaShell(emptyList()))
    }

    @Test
    fun `caminhoComEspacoChegaInteiroNoShell`() {
        assertEquals(listOf("/tmp/Captura de tela.png"), argumentosVistosPeloShell("/tmp/Captura de tela.png"))
    }

    @Test
    fun `nome com aspa simples chega inteiro no shell`() {
        assertEquals(listOf("/tmp/joao's foto.jpg"), argumentosVistosPeloShell("/tmp/joao's foto.jpg"))
    }

    @Test
    fun `nome com substituicao de comando nao executa nada no shell`() {
        // Se o escape falhasse, o shell rodaria `id` e o argumento voltaria
        // como "uid=0(root)..." em vez do texto literal.
        assertEquals(listOf("/tmp/\$(id).png"), argumentosVistosPeloShell("/tmp/\$(id).png"))
    }

    @Test
    fun `dois caminhos inseridos juntos chegam como dois argumentos`() {
        val vistos = argumentosVistosPeloShell("/tmp/um dois.png", "/tmp/tres;quatro.png")
        assertEquals(listOf("/tmp/um dois.png", "/tmp/tres;quatro.png"), vistos)
    }

    /**
     * Runs `sh -c 'printf "%s\n" <insertion>'` and returns what the shell
     * understood as the arguments. `printf %s\n` is the most faithful echo
     * possible: one argument per line, interpreting nothing of the content.
     */
    private fun argumentosVistosPeloShell(vararg caminhos: String): List<String> {
        assumeTrue(File("/bin/sh").exists())
        val insercao = textoDeInsercaoParaShell(caminhos.toList())
        val processo = ProcessBuilder("/bin/sh", "-c", "printf '%s\\n' $insercao")
            .redirectErrorStream(true)
            .start()
        val saida = processo.inputStream.bufferedReader().readText()
        processo.waitFor()
        return saida.split("\n").filter { it.isNotEmpty() }
    }
}
