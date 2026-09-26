cmake_minimum_required(VERSION 3.22)
project(terminal_engine_jni CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

if(NOT ANDROID_ABI)
    message(FATAL_ERROR "ANDROID_ABI nao definido — este CMakeLists.txt so e valido sob o toolchain do NDK (externalNativeBuild).")
endif()

set(VENDOR_DIR ${CMAKE_CURRENT_SOURCE_DIR}/../../../vendor)
set(VENDOR_LIB ${VENDOR_DIR}/${ANDROID_ABI}/libghostty-vt.a)

if(NOT EXISTS ${VENDOR_LIB})
    message(FATAL_ERROR "libghostty-vt.a ausente para ${ANDROID_ABI}: ${VENDOR_LIB}")
endif()

add_library(ghostty_vt STATIC IMPORTED)
set_target_properties(ghostty_vt PROPERTIES IMPORTED_LOCATION ${VENDOR_LIB})

# ghostty_jni.cpp e a UNICA translation unit deste modulo autorizada a
# referenciar tipos/enums/funcoes ghostty_* (offsets de campo do C ABI
# vendorizado nao podem vazar para fora deste arquivo).
add_library(terminal_engine_jni SHARED
    ghostty_jni.cpp
)

target_include_directories(terminal_engine_jni PRIVATE
    ${VENDOR_DIR}/include
)

find_library(log-lib log)

target_link_libraries(terminal_engine_jni
    PRIVATE
    ghostty_vt
    ${log-lib}
)
