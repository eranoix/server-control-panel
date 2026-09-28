#include <jni.h>
#include <android/keycodes.h>
#include <android/log.h>

#include <cstdint>
#include <cstring>
#include <memory>
#include <mutex>

#include <ghostty/vt.h>

#define LOG_TAG "TerminalEngineJNI"
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)

namespace {

constexpr size_t kHeaderSize = 16;
constexpr size_t kCellStride = 16;

size_t rowFlagsBytes(uint16_t rows) {
    return (static_cast<size_t>(rows) + 3u) & ~size_t{3};
}

size_t bufferCapacityFor(uint16_t cols, uint16_t rows) {
    return kHeaderSize + rowFlagsBytes(rows) +
           static_cast<size_t>(cols) * static_cast<size_t>(rows) * kCellStride;
}

void putU16(uint8_t* p, uint16_t v) {
    p[0] = static_cast<uint8_t>(v & 0xff);
    p[1] = static_cast<uint8_t>((v >> 8) & 0xff);
}

void putI32(uint8_t* p, int32_t v) {
    p[0] = static_cast<uint8_t>(v & 0xff);
    p[1] = static_cast<uint8_t>((v >> 8) & 0xff);
    p[2] = static_cast<uint8_t>((v >> 16) & 0xff);
    p[3] = static_cast<uint8_t>((v >> 24) & 0xff);
}

struct EngineHandle {
    GhosttyTerminal terminal = nullptr;
    GhosttyRenderState renderState = nullptr;
    GhosttyRenderStateRowIterator rowIterator = nullptr;
    GhosttyRenderStateRowCells rowCells = nullptr;

    GhosttyMouseEncoder mouseEncoder = nullptr;
    GhosttyMouseEvent mouseEvent = nullptr;

    std::mutex mutex;

    std::mutex bufferMutex;

    uint8_t* bufferPtr = nullptr;
    size_t bufferCapacity = 0;
    uint16_t cols = 0;
    uint16_t rows = 0;

    int bufferAllocations = 0;

    bool closed = false;
};

jclass g_illegalStateExceptionClass = nullptr;

EngineHandle* handleFrom(jlong ptr) {
    return reinterpret_cast<EngineHandle*>(static_cast<intptr_t>(ptr));
}

void throwClosed(JNIEnv* env) {
    if (g_illegalStateExceptionClass != nullptr) {
        env->ThrowNew(g_illegalStateExceptionClass, "TerminalEngine already closed");
    }
}

void freeBufferLocked(EngineHandle* h) {
    delete[] h->bufferPtr;
    h->bufferPtr = nullptr;
    h->bufferCapacity = 0;
}

jobject reallocateBuffer(JNIEnv* env, EngineHandle* h, uint16_t cols, uint16_t rows) {
    size_t needed = bufferCapacityFor(cols, rows);

    std::lock_guard<std::mutex> lock(h->bufferMutex);
    if (needed != h->bufferCapacity) {
        freeBufferLocked(h);
        h->bufferPtr = new uint8_t[needed];
        h->bufferCapacity = needed;
        h->bufferAllocations++;
    }
    std::memset(h->bufferPtr, 0, h->bufferCapacity);
    h->cols = cols;
    h->rows = rows;

    return env->NewDirectByteBuffer(h->bufferPtr, static_cast<jlong>(h->bufferCapacity));
}

GhosttyKey mapAndroidKeyCode(jint keyCode, jint unshiftedCodepoint) {
    switch (keyCode) {
        case AKEYCODE_DPAD_UP: return GHOSTTY_KEY_ARROW_UP;
        case AKEYCODE_DPAD_DOWN: return GHOSTTY_KEY_ARROW_DOWN;
        case AKEYCODE_DPAD_LEFT: return GHOSTTY_KEY_ARROW_LEFT;
        case AKEYCODE_DPAD_RIGHT: return GHOSTTY_KEY_ARROW_RIGHT;
        case AKEYCODE_MOVE_HOME: return GHOSTTY_KEY_HOME;
        case AKEYCODE_MOVE_END: return GHOSTTY_KEY_END;
        case AKEYCODE_PAGE_UP: return GHOSTTY_KEY_PAGE_UP;
        case AKEYCODE_PAGE_DOWN: return GHOSTTY_KEY_PAGE_DOWN;
        case AKEYCODE_INSERT: return GHOSTTY_KEY_INSERT;
        case AKEYCODE_FORWARD_DEL: return GHOSTTY_KEY_DELETE;
        case AKEYCODE_DEL: return GHOSTTY_KEY_BACKSPACE;
        case AKEYCODE_TAB: return GHOSTTY_KEY_TAB;
        case AKEYCODE_ENTER: return GHOSTTY_KEY_ENTER;
        case AKEYCODE_NUMPAD_ENTER: return GHOSTTY_KEY_NUMPAD_ENTER;
        case AKEYCODE_ESCAPE: return GHOSTTY_KEY_ESCAPE;
        case AKEYCODE_SPACE: return GHOSTTY_KEY_SPACE;
        case AKEYCODE_F1: return GHOSTTY_KEY_F1;
        case AKEYCODE_F2: return GHOSTTY_KEY_F2;
        case AKEYCODE_F3: return GHOSTTY_KEY_F3;
        case AKEYCODE_F4: return GHOSTTY_KEY_F4;
        case AKEYCODE_F5: return GHOSTTY_KEY_F5;
        case AKEYCODE_F6: return GHOSTTY_KEY_F6;
        case AKEYCODE_F7: return GHOSTTY_KEY_F7;
        case AKEYCODE_F8: return GHOSTTY_KEY_F8;
        case AKEYCODE_F9: return GHOSTTY_KEY_F9;
        case AKEYCODE_F10: return GHOSTTY_KEY_F10;
        case AKEYCODE_F11: return GHOSTTY_KEY_F11;
        case AKEYCODE_F12: return GHOSTTY_KEY_F12;
        default: break;
    }
    if (keyCode >= AKEYCODE_A && keyCode <= AKEYCODE_Z) {
        return static_cast<GhosttyKey>(GHOSTTY_KEY_A + (keyCode - AKEYCODE_A));
    }
    if (keyCode >= AKEYCODE_0 && keyCode <= AKEYCODE_9) {
        return static_cast<GhosttyKey>(GHOSTTY_KEY_DIGIT_0 + (keyCode - AKEYCODE_0));
    }
    (void)unshiftedCodepoint;
    return GHOSTTY_KEY_UNIDENTIFIED;
}

}

extern "C" {

JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM* vm, void* ) {
    JNIEnv* env = nullptr;
    if (vm->GetEnv(reinterpret_cast<void**>(&env), JNI_VERSION_1_6) != JNI_OK) {
        return JNI_ERR;
    }
    jclass local = env->FindClass("java/lang/IllegalStateException");
    if (local != nullptr) {
        g_illegalStateExceptionClass = static_cast<jclass>(env->NewGlobalRef(local));
        env->DeleteLocalRef(local);
    }
    return JNI_VERSION_1_6;
}

JNIEXPORT void JNICALL JNI_OnUnload(JavaVM* vm, void* ) {
    JNIEnv* env = nullptr;
    if (vm->GetEnv(reinterpret_cast<void**>(&env), JNI_VERSION_1_6) == JNI_OK &&
        g_illegalStateExceptionClass != nullptr) {
        env->DeleteGlobalRef(g_illegalStateExceptionClass);
        g_illegalStateExceptionClass = nullptr;
    }
}

JNIEXPORT jlong JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeCreate(
    JNIEnv* env, jclass, jint cols, jint rows, jint scrollback) {
    auto* h = new EngineHandle();

    GhosttyTerminalOptions options{};
    options.cols = static_cast<uint16_t>(cols);
    options.rows = static_cast<uint16_t>(rows);
    options.max_scrollback = static_cast<size_t>(scrollback);

    if (ghostty_terminal_new(nullptr, &h->terminal, options) != GHOSTTY_SUCCESS) {
        LOGE("ghostty_terminal_new failed");
        delete h;
        return 0;
    }
    if (ghostty_render_state_new(nullptr, &h->renderState) != GHOSTTY_SUCCESS) {
        LOGE("ghostty_render_state_new failed");
        ghostty_terminal_free(h->terminal);
        delete h;
        return 0;
    }
    if (ghostty_render_state_row_iterator_new(nullptr, &h->rowIterator) != GHOSTTY_SUCCESS ||
        ghostty_render_state_row_cells_new(nullptr, &h->rowCells) != GHOSTTY_SUCCESS) {
        LOGE("render state iterator allocation failed");
        if (h->rowIterator) ghostty_render_state_row_iterator_free(h->rowIterator);
        ghostty_render_state_free(h->renderState);
        ghostty_terminal_free(h->terminal);
        delete h;
        return 0;
    }
    if (ghostty_mouse_encoder_new(nullptr, &h->mouseEncoder) != GHOSTTY_SUCCESS ||
        ghostty_mouse_event_new(nullptr, &h->mouseEvent) != GHOSTTY_SUCCESS) {
        LOGE("mouse encoder allocation failed");
        if (h->mouseEncoder) ghostty_mouse_encoder_free(h->mouseEncoder);
        ghostty_render_state_row_cells_free(h->rowCells);
        ghostty_render_state_row_iterator_free(h->rowIterator);
        ghostty_render_state_free(h->renderState);
        ghostty_terminal_free(h->terminal);
        delete h;
        return 0;
    }

    jobject buf = reallocateBuffer(env, h, static_cast<uint16_t>(cols), static_cast<uint16_t>(rows));
    env->DeleteLocalRef(buf);

    return static_cast<jlong>(reinterpret_cast<intptr_t>(h));
}

JNIEXPORT jobject JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeBuffer(
    JNIEnv* env, jclass, jlong handle) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return nullptr;
    }
    std::lock_guard<std::mutex> bufferLock(h->bufferMutex);
    return env->NewDirectByteBuffer(h->bufferPtr, static_cast<jlong>(h->bufferCapacity));
}

JNIEXPORT void JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeWrite(
    JNIEnv* env, jclass, jlong handle, jbyteArray data) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return;
    }
    jsize length = env->GetArrayLength(data);
    void* raw = env->GetPrimitiveArrayCritical(data, nullptr);
    if (raw == nullptr) return;
    {
        std::lock_guard<std::mutex> lock(h->mutex);
        ghostty_terminal_vt_write(h->terminal, static_cast<const uint8_t*>(raw), static_cast<size_t>(length));
    }
    env->ReleasePrimitiveArrayCritical(data, raw, JNI_ABORT);
}

JNIEXPORT void JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeSnapshot(
    JNIEnv* env, jclass, jlong handle) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return;
    }

    {
        std::lock_guard<std::mutex> lock(h->mutex);
        ghostty_render_state_begin_update(h->renderState, h->terminal);
    }
    ghostty_render_state_end_update(h->renderState);
    std::lock_guard<std::mutex> bufferLock(h->bufferMutex);

    uint16_t cols = 0;
    uint16_t rows = 0;
    ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_COLS, &cols);
    ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_ROWS, &rows);

    uint16_t useCols = cols <= h->cols ? cols : h->cols;
    uint16_t useRows = rows <= h->rows ? rows : h->rows;

    bool cursorVisible = false;
    bool cursorHasValue = false;
    uint16_t cursorX = 0;
    uint16_t cursorY = 0;
    bool cursorWideTail = false;
    ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR_VISIBLE, &cursorVisible);
    ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_HAS_VALUE, &cursorHasValue);
    if (cursorHasValue) {
        ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_X, &cursorX);
        ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_Y, &cursorY);
        ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_WIDE_TAIL, &cursorWideTail);
    }

    uint8_t* buf = h->bufferPtr;
    putU16(buf + 0, useCols);
    putU16(buf + 2, useRows);
    putU16(buf + 4, cursorX);
    putU16(buf + 6, cursorY);
    buf[8] = cursorVisible ? 1 : 0;
    buf[9] = cursorHasValue ? 1 : 0;
    buf[10] = cursorWideTail ? 1 : 0;
    buf[11] = 0;
    buf[12] = 0;
    buf[13] = 0;
    buf[14] = 0;
    buf[15] = 0;

    uint8_t* rowFlags = buf + kHeaderSize;
    uint8_t* cells = rowFlags + rowFlagsBytes(h->rows);

    GhosttyRenderStateRowIterator rowIter = h->rowIterator;
    ghostty_render_state_get(h->renderState, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &rowIter);

    uint16_t y = 0;
    while (y < useRows && ghostty_render_state_row_iterator_next(rowIter)) {
        bool wrap = false;
        bool wrapContinuation = false;
        GhosttyRow rawRow = 0;
        ghostty_render_state_row_get(rowIter, GHOSTTY_RENDER_STATE_ROW_DATA_RAW, &rawRow);
        ghostty_row_get(rawRow, GHOSTTY_ROW_DATA_WRAP, &wrap);
        ghostty_row_get(rawRow, GHOSTTY_ROW_DATA_WRAP_CONTINUATION, &wrapContinuation);
        rowFlags[y] = static_cast<uint8_t>((wrap ? 1 : 0) | (wrapContinuation ? 2 : 0));

        GhosttyRenderStateRowCells rowCells = h->rowCells;
        ghostty_render_state_row_get(rowIter, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &rowCells);

        uint8_t* rowBase = cells + static_cast<size_t>(y) * h->cols * kCellStride;
        for (uint16_t x = 0; x < useCols; x++) {
            ghostty_render_state_row_cells_select(rowCells, x);
            uint8_t* cell = rowBase + static_cast<size_t>(x) * kCellStride;

            GhosttyCell rawCell = 0;
            ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &rawCell);

            uint32_t codepoint = 0;
            GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
            ghostty_cell_get(rawCell, GHOSTTY_CELL_DATA_CODEPOINT, &codepoint);
            ghostty_cell_get(rawCell, GHOSTTY_CELL_DATA_WIDE, &wide);

            GhosttyStyle style{};
            style.size = sizeof(GhosttyStyle);
            bool hasStyling = false;
            ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_HAS_STYLING, &hasStyling);
            if (hasStyling) {
                ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
            } else {
                ghostty_style_default(&style);
            }

            GhosttyColorRgb fg{};
            GhosttyColorRgb bg{};
            bool fgValid = ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &fg) == GHOSTTY_SUCCESS;
            bool bgValid = ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg) == GHOSTTY_SUCCESS;

            putI32(cell + 0, static_cast<int32_t>(codepoint));
            cell[4] = fgValid ? 1 : 0;
            cell[5] = fg.r;
            cell[6] = fg.g;
            cell[7] = fg.b;
            cell[8] = bgValid ? 1 : 0;
            cell[9] = bg.r;
            cell[10] = bg.g;
            cell[11] = bg.b;

            uint8_t attrs = 0;
            attrs |= style.bold ? 1u << 0 : 0;
            attrs |= style.italic ? 1u << 1 : 0;
            attrs |= style.faint ? 1u << 2 : 0;
            attrs |= style.blink ? 1u << 3 : 0;
            attrs |= style.inverse ? 1u << 4 : 0;
            attrs |= style.invisible ? 1u << 5 : 0;
            attrs |= style.strikethrough ? 1u << 6 : 0;
            attrs |= style.overline ? 1u << 7 : 0;
            cell[12] = attrs;
            cell[13] = static_cast<uint8_t>(style.underline);
            cell[14] = static_cast<uint8_t>(wide);
            cell[15] = 0;
        }
        y++;
    }
}

JNIEXPORT jobject JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeResize(
    JNIEnv* env, jclass, jlong handle, jint cols, jint rows) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return nullptr;
    }
    {
        std::lock_guard<std::mutex> lock(h->mutex);
        ghostty_terminal_resize(h->terminal, static_cast<uint16_t>(cols), static_cast<uint16_t>(rows), 0, 0);
    }
    return reallocateBuffer(env, h, static_cast<uint16_t>(cols), static_cast<uint16_t>(rows));
}

JNIEXPORT jbyteArray JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeEncodeKey(
    JNIEnv* env, jclass, jlong handle, jint action, jint androidKeyCode, jint mods,
    jint unshiftedCodepoint, jbyteArray utf8OrNull, jboolean cursorApplicationMode,
    jboolean altEscPrefix) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return nullptr;
    }

    GhosttyKeyEncoder encoder = nullptr;
    GhosttyKeyEvent event = nullptr;
    if (ghostty_key_encoder_new(nullptr, &encoder) != GHOSTTY_SUCCESS) return nullptr;
    if (ghostty_key_event_new(nullptr, &event) != GHOSTTY_SUCCESS) {
        ghostty_key_encoder_free(encoder);
        return nullptr;
    }

    bool cursorApp = cursorApplicationMode != JNI_FALSE;
    bool altEsc = altEscPrefix != JNI_FALSE;
    ghostty_key_encoder_setopt(encoder, GHOSTTY_KEY_ENCODER_OPT_CURSOR_KEY_APPLICATION, &cursorApp);
    ghostty_key_encoder_setopt(encoder, GHOSTTY_KEY_ENCODER_OPT_ALT_ESC_PREFIX, &altEsc);

    ghostty_key_event_set_action(event, static_cast<GhosttyKeyAction>(action));
    ghostty_key_event_set_key(event, mapAndroidKeyCode(androidKeyCode, unshiftedCodepoint));
    ghostty_key_event_set_mods(event, static_cast<GhosttyMods>(mods));
    ghostty_key_event_set_unshifted_codepoint(event, static_cast<uint32_t>(unshiftedCodepoint));

    jbyte utf8Stack[8];
    jsize utf8Len = 0;
    if (utf8OrNull != nullptr) {
        utf8Len = env->GetArrayLength(utf8OrNull);
        if (utf8Len > 0 && utf8Len <= static_cast<jsize>(sizeof(utf8Stack))) {
            env->GetByteArrayRegion(utf8OrNull, 0, utf8Len, utf8Stack);
            ghostty_key_event_set_utf8(event, reinterpret_cast<const char*>(utf8Stack), static_cast<size_t>(utf8Len));
        }
    }

    char outBuf[128];
    size_t written = 0;
    GhosttyResult result = ghostty_key_encoder_encode(encoder, event, outBuf, sizeof(outBuf), &written);

    ghostty_key_event_free(event);
    ghostty_key_encoder_free(encoder);

    if (result != GHOSTTY_SUCCESS || written == 0) {
        return nullptr;
    }

    jbyteArray out = env->NewByteArray(static_cast<jsize>(written));
    if (out != nullptr) {
        env->SetByteArrayRegion(out, 0, static_cast<jsize>(written), reinterpret_cast<const jbyte*>(outBuf));
    }
    return out;
}

JNIEXPORT jint JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeModes(
    JNIEnv* env, jclass, jlong handle) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return 0;
    }
    std::lock_guard<std::mutex> lock(h->mutex);
    jint bits = 0;
    bool mouseTracking = false;
    if (ghostty_terminal_get(h->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &mouseTracking) == GHOSTTY_SUCCESS &&
        mouseTracking) {
        bits |= 1;
    }
    bool bracketedPaste = false;
    if (ghostty_terminal_mode_get(h->terminal, GHOSTTY_MODE_BRACKETED_PASTE, &bracketedPaste) == GHOSTTY_SUCCESS &&
        bracketedPaste) {
        bits |= 2;
    }
    GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
    if (ghostty_terminal_get(h->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen) == GHOSTTY_SUCCESS &&
        screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE) {
        bits |= 4;
    }
    bool altScroll = false;
    if (ghostty_terminal_mode_get(h->terminal, GHOSTTY_MODE_ALT_SCROLL, &altScroll) == GHOSTTY_SUCCESS &&
        altScroll) {
        bits |= 8;
    }
    bool cursorKeys = false;
    if (ghostty_terminal_mode_get(h->terminal, GHOSTTY_MODE_DECCKM, &cursorKeys) == GHOSTTY_SUCCESS &&
        cursorKeys) {
        bits |= 16;
    }
    return bits;
}

JNIEXPORT void JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeScrollViewport(
    JNIEnv* env, jclass, jlong handle, jint tag, jlong value) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return;
    }
    std::lock_guard<std::mutex> lock(h->mutex);
    GhosttyTerminalScrollViewport behavior{};
    behavior.tag = static_cast<GhosttyTerminalScrollViewportTag>(tag);
    switch (behavior.tag) {
        case GHOSTTY_SCROLL_VIEWPORT_DELTA:
            behavior.value.delta = static_cast<intptr_t>(value);
            break;
        case GHOSTTY_SCROLL_VIEWPORT_ROW:
            behavior.value.row = value < 0 ? 0u : static_cast<size_t>(value);
            break;
        default:
            break;
    }
    ghostty_terminal_scroll_viewport(h->terminal, behavior);
}

JNIEXPORT jlongArray JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeScrollState(
    JNIEnv* env, jclass, jlong handle) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return nullptr;
    }

    jlong values[4] = {0, 0, 0, 1};
    {
        std::lock_guard<std::mutex> lock(h->mutex);
        GhosttyTerminalScrollbar scrollbar{};
        if (ghostty_terminal_get(h->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, &scrollbar) == GHOSTTY_SUCCESS) {
            values[0] = static_cast<jlong>(scrollbar.total);
            values[1] = static_cast<jlong>(scrollbar.offset);
            values[2] = static_cast<jlong>(scrollbar.len);
        }
        bool viewportActive = true;
        if (ghostty_terminal_get(h->terminal, GHOSTTY_TERMINAL_DATA_VIEWPORT_ACTIVE, &viewportActive) == GHOSTTY_SUCCESS) {
            values[3] = viewportActive ? 1 : 0;
        }
    }

    jlongArray out = env->NewLongArray(4);
    if (out != nullptr) {
        env->SetLongArrayRegion(out, 0, 4, values);
    }
    return out;
}

JNIEXPORT jbyteArray JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeEncodeMouse(
    JNIEnv* env, jclass, jlong handle, jint action, jint button, jint mods,
    jfloat xPx, jfloat yPx, jint cellWidthPx, jint cellHeightPx,
    jint screenWidthPx, jint screenHeightPx, jboolean anyButtonPressed) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return nullptr;
    }
    if (cellWidthPx <= 0 || cellHeightPx <= 0) return nullptr;

    std::lock_guard<std::mutex> lock(h->mutex);

    ghostty_mouse_encoder_setopt_from_terminal(h->mouseEncoder, h->terminal);

    bool trackLastCell = true;
    ghostty_mouse_encoder_setopt(h->mouseEncoder, GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL, &trackLastCell);

    GhosttyMouseEncoderSize size{};
    size.size = sizeof(GhosttyMouseEncoderSize);
    size.screen_width = static_cast<uint32_t>(screenWidthPx);
    size.screen_height = static_cast<uint32_t>(screenHeightPx);
    size.cell_width = static_cast<uint32_t>(cellWidthPx);
    size.cell_height = static_cast<uint32_t>(cellHeightPx);
    ghostty_mouse_encoder_setopt(h->mouseEncoder, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &size);

    bool pressed = anyButtonPressed != JNI_FALSE;
    ghostty_mouse_encoder_setopt(h->mouseEncoder, GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &pressed);

    ghostty_mouse_event_set_action(h->mouseEvent, static_cast<GhosttyMouseAction>(action));
    if (button <= 0) {
        ghostty_mouse_event_clear_button(h->mouseEvent);
    } else {
        ghostty_mouse_event_set_button(h->mouseEvent, static_cast<GhosttyMouseButton>(button));
    }
    ghostty_mouse_event_set_mods(h->mouseEvent, static_cast<GhosttyMods>(mods));

    GhosttyMousePosition position{};
    position.x = xPx;
    position.y = yPx;
    ghostty_mouse_event_set_position(h->mouseEvent, position);

    char outBuf[64];
    size_t written = 0;
    GhosttyResult result =
        ghostty_mouse_encoder_encode(h->mouseEncoder, h->mouseEvent, outBuf, sizeof(outBuf), &written);
    if (result != GHOSTTY_SUCCESS || written == 0) {
        return nullptr;
    }

    jbyteArray out = env->NewByteArray(static_cast<jsize>(written));
    if (out != nullptr) {
        env->SetByteArrayRegion(out, 0, static_cast<jsize>(written), reinterpret_cast<const jbyte*>(outBuf));
    }
    return out;
}

JNIEXPORT jbyteArray JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeEncodePaste(
    JNIEnv* env, jclass, jlong handle, jbyteArray utf8) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return nullptr;
    }
    if (utf8 == nullptr) return nullptr;

    jsize length = env->GetArrayLength(utf8);
    std::unique_ptr<char[]> data(new char[static_cast<size_t>(length) + 1]);
    if (length > 0) {
        env->GetByteArrayRegion(utf8, 0, length, reinterpret_cast<jbyte*>(data.get()));
    }

    bool bracketed = false;
    {
        std::lock_guard<std::mutex> lock(h->mutex);
        ghostty_terminal_mode_get(h->terminal, GHOSTTY_MODE_BRACKETED_PASTE, &bracketed);
    }

    size_t capacity = static_cast<size_t>(length) + 32;
    std::unique_ptr<char[]> out(new char[capacity]);
    size_t written = 0;
    GhosttyResult result =
        ghostty_paste_encode(data.get(), static_cast<size_t>(length), bracketed, out.get(), capacity, &written);
    if (result == GHOSTTY_OUT_OF_SPACE) {
        capacity = written;
        out.reset(new char[capacity]);
        result = ghostty_paste_encode(data.get(), static_cast<size_t>(length), bracketed, out.get(), capacity, &written);
    }
    if (result != GHOSTTY_SUCCESS) return nullptr;

    jbyteArray array = env->NewByteArray(static_cast<jsize>(written));
    if (array != nullptr && written > 0) {
        env->SetByteArrayRegion(array, 0, static_cast<jsize>(written), reinterpret_cast<const jbyte*>(out.get()));
    }
    return array;
}

JNIEXPORT jint JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeDebugBufferAllocationCount(
    JNIEnv* env, jclass, jlong handle) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) {
        throwClosed(env);
        return 0;
    }
    std::lock_guard<std::mutex> bufferLock(h->bufferMutex);
    return h->bufferAllocations;
}

JNIEXPORT void JNICALL
Java_dev_servercontrolpanel_terminalengine_TerminalEngine_nativeClose(
    JNIEnv* env, jclass, jlong handle) {
    EngineHandle* h = handleFrom(handle);
    if (h == nullptr || h->closed) return;

    {
        std::lock_guard<std::mutex> lock(h->mutex);
        h->closed = true;
        if (h->mouseEvent) ghostty_mouse_event_free(h->mouseEvent);
        if (h->mouseEncoder) ghostty_mouse_encoder_free(h->mouseEncoder);
        if (h->rowCells) ghostty_render_state_row_cells_free(h->rowCells);
        if (h->rowIterator) ghostty_render_state_row_iterator_free(h->rowIterator);
        if (h->renderState) ghostty_render_state_free(h->renderState);
        if (h->terminal) ghostty_terminal_free(h->terminal);
        std::lock_guard<std::mutex> bufferLock(h->bufferMutex);
        freeBufferLocked(h);
    }
    delete h;
}

}
