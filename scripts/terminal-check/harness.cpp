// Harness: runs the APP'S ENGINE (libghostty-vt, the same vendored .a) over a file
// of raw bytes and prints the resulting grid, to compare it, with no device, with
// what a reference emulator produces from the SAME bytes.
//
// Mirrors the snapshot path of ghostty_jni.cpp: begin_update/end_update, row
// iterator, cell selection.
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
    const char* path = argc > 1 ? argv[1] : "/dev/stdin";
    uint16_t cols = argc > 2 ? static_cast<uint16_t>(atoi(argv[2])) : 67;
    uint16_t rows = argc > 3 ? static_cast<uint16_t>(atoi(argv[3])) : 53;
    size_t window = argc > 4 ? static_cast<size_t>(atol(argv[4])) : 1500000;
    // Write chunk size: the app replays the log IN CHUNKS.
    size_t chunk = argc > 5 ? static_cast<size_t>(atol(argv[5])) : 0;

    FILE* f = fopen(path, "rb");
    if (!f) { fprintf(stderr, "could not open %s\n", path); return 1; }
    fseek(f, 0, SEEK_END);
    long total = ftell(f);
    long start = total > static_cast<long>(window) ? total - static_cast<long>(window) : 0;
    fseek(f, start, SEEK_SET);
    std::vector<uint8_t> data(static_cast<size_t>(total - start));
    size_t nread = fread(data.data(), 1, data.size(), f);
    data.resize(nread);
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
        fprintf(stderr, "ghostty_terminal_new failed\n"); return 1;
    }
    if (ghostty_render_state_new(nullptr, &rs) != GHOSTTY_SUCCESS) return 1;
    if (ghostty_render_state_row_iterator_new(nullptr, &it) != GHOSTTY_SUCCESS) return 1;
    if (ghostty_render_state_row_cells_new(nullptr, &cells) != GHOSTTY_SUCCESS) return 1;

    if (chunk == 0) {
        ghostty_terminal_vt_write(terminal, data.data(), data.size());
    } else {
        for (size_t i = 0; i < data.size(); i += chunk) {
            size_t n = data.size() - i < chunk ? data.size() - i : chunk;
            ghostty_terminal_vt_write(terminal, data.data() + i, n);
        }
    }

    ghostty_render_state_begin_update(rs, terminal);
    ghostty_render_state_end_update(rs);

    uint16_t c = 0, r = 0;
    ghostty_render_state_get(rs, GHOSTTY_RENDER_STATE_DATA_COLS, &c);
    ghostty_render_state_get(rs, GHOSTTY_RENDER_STATE_DATA_ROWS, &r);
    printf("=== APP ENGINE (libghostty-vt) — %ux%u, %zu bytes, chunk=%zu ===\n",
           c, r, data.size(), chunk);

    GhosttyRenderStateRowIterator iter = it;
    ghostty_render_state_get(rs, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &iter);

    uint16_t y = 0;
    while (y < r && ghostty_render_state_row_iterator_next(iter)) {
        GhosttyRenderStateRowCells rc = cells;
        ghostty_render_state_row_get(iter, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &rc);
        std::string line;
        for (uint16_t x = 0; x < c; x++) {
            ghostty_render_state_row_cells_select(rc, x);
            GhosttyCell raw = 0;
            ghostty_render_state_row_cells_get(rc, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw);
            uint32_t cp = 0;
            ghostty_cell_get(raw, GHOSTTY_CELL_DATA_CODEPOINT, &cp);
            utf8(cp, line);
        }
        while (!line.empty() && line.back() == ' ') line.pop_back();
        printf("%02u|%s\n", y, line.c_str());
        y++;
    }
    return 0;
}
