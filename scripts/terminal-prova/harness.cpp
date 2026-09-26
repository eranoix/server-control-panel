// Harness: roda o MOTOR DO APP (libghostty-vt, o mesmo .a vendorizado) sobre um
// arquivo de bytes crus e imprime a grade resultante. Serve para comparar, sem
// aparelho nenhum, o que o motor do app produz com o que um emulador de
// referencia produz a partir dos MESMOS bytes.
//
// Reproduz o caminho de snapshot do ghostty_jni.cpp: begin_update/end_update,
// iterador de linhas, selecao de celula.
#include <ghostty/vt.h>

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

static void utf8(uint32_t cp, std::string& out) {
    if (cp == 0) { out += ' '; return; }
    if (cp < 0x80) { out += static_cast<char>(cp); return; }
    if (cp < 0x800) {
        out += static_cast<char>(0xC0 | (cp >> 6));
        out += static_cast<char>(0x80 | (cp & 0x3F));
        return;
    }
    if (cp < 0x10000) {
        out += static_cast<char>(0xE0 | (cp >> 12));
        out += static_cast<char>(0x80 | ((cp >> 6) & 0x3F));
        out += static_cast<char>(0x80 | (cp & 0x3F));
        return;
    }
    out += static_cast<char>(0xF0 | (cp >> 18));
    out += static_cast<char>(0x80 | ((cp >> 12) & 0x3F));
    out += static_cast<char>(0x80 | ((cp >> 6) & 0x3F));
    out += static_cast<char>(0x80 | (cp & 0x3F));
}

int main(int argc, char** argv) {
    const char* caminho = argc > 1 ? argv[1] : "/dev/stdin";
    uint16_t cols = argc > 2 ? static_cast<uint16_t>(atoi(argv[2])) : 67;
    uint16_t rows = argc > 3 ? static_cast<uint16_t>(atoi(argv[3])) : 53;
    size_t janela = argc > 4 ? static_cast<size_t>(atol(argv[4])) : 1500000;
    // Tamanho do pedaco de escrita: o app replaya o log EM PEDACOS.
    size_t pedaco = argc > 5 ? static_cast<size_t>(atol(argv[5])) : 0;

    FILE* f = fopen(caminho, "rb");
    if (!f) { fprintf(stderr, "nao abriu %s\n", caminho); return 1; }
    fseek(f, 0, SEEK_END);
    long total = ftell(f);
    long inicio = total > static_cast<long>(janela) ? total - static_cast<long>(janela) : 0;
    fseek(f, inicio, SEEK_SET);
    std::vector<uint8_t> dados(static_cast<size_t>(total - inicio));
    size_t lidos = fread(dados.data(), 1, dados.size(), f);
    dados.resize(lidos);
    fclose(f);

    GhosttyTerminal terminal = 0;
    GhosttyRenderState rs = 0;
    GhosttyRenderStateRowIterator it = 0;
    GhosttyRenderStateRowCells cells = 0;

    GhosttyTerminalOptions options{};
    options.cols = cols;
    options.rows = rows;
    options.max_scrollback = 10000 * 80;

    if (ghostty_terminal_new(nullptr, &terminal, options) != GHOSTTY_SUCCESS) {
        fprintf(stderr, "ghostty_terminal_new falhou\n"); return 1;
    }
    if (ghostty_render_state_new(nullptr, &rs) != GHOSTTY_SUCCESS) return 1;
    if (ghostty_render_state_row_iterator_new(nullptr, &it) != GHOSTTY_SUCCESS) return 1;
    if (ghostty_render_state_row_cells_new(nullptr, &cells) != GHOSTTY_SUCCESS) return 1;

    if (pedaco == 0) {
        ghostty_terminal_vt_write(terminal, dados.data(), dados.size());
    } else {
        for (size_t i = 0; i < dados.size(); i += pedaco) {
            size_t n = dados.size() - i < pedaco ? dados.size() - i : pedaco;
            ghostty_terminal_vt_write(terminal, dados.data() + i, n);
        }
    }

    ghostty_render_state_begin_update(rs, terminal);
    ghostty_render_state_end_update(rs);

    uint16_t c = 0, r = 0;
    ghostty_render_state_get(rs, GHOSTTY_RENDER_STATE_DATA_COLS, &c);
    ghostty_render_state_get(rs, GHOSTTY_RENDER_STATE_DATA_ROWS, &r);
    printf("=== MOTOR DO APP (libghostty-vt) — %ux%u, %zu bytes, pedaco=%zu ===\n",
           c, r, dados.size(), pedaco);

    GhosttyRenderStateRowIterator iter = it;
    ghostty_render_state_get(rs, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &iter);

    uint16_t y = 0;
    while (y < r && ghostty_render_state_row_iterator_next(iter)) {
        GhosttyRenderStateRowCells rc = cells;
        ghostty_render_state_row_get(iter, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &rc);
        std::string linha;
        for (uint16_t x = 0; x < c; x++) {
            ghostty_render_state_row_cells_select(rc, x);
            GhosttyCell raw = 0;
            ghostty_render_state_row_cells_get(rc, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw);
            uint32_t cp = 0;
            ghostty_cell_get(raw, GHOSTTY_CELL_DATA_CODEPOINT, &cp);
            utf8(cp, linha);
        }
        while (!linha.empty() && linha.back() == ' ') linha.pop_back();
        printf("%02u|%s\n", y, linha.c_str());
        y++;
    }
    return 0;
}
