# libhpatchz.so -- lado PATCHER do HDiffPatch (MIT), so o que o aparelho
# precisa para APLICAR um patch. O gerador (hdiffz) nao entra aqui: ele roda
# no servidor, e compilar o diff no celular seria carregar o suffix-array e
# os compressores inteiros para nada.
#
# Este arquivo e uma copia adaptada de
#   vendor/HDiffPatch/builds/android_ndk_jni_mk/Android.mk
# (commit fixado em ../../../toolchain.properties). Nao foi traduzido para
# CMake de proposito, ao contrario de :terminal-engine: os -D deste arquivo
# escolhem QUAIS descompressores e checksums existem dentro do .so, e uma
# retraducao a mao seria um lugar a mais para divergir em silencio do
# upstream -- um -D perdido nao quebra o build, produz um .so que rejeita
# patches em runtime, no aparelho do usuario. AGP suporta ndkBuild tao
# nativamente quanto CMake (externalNativeBuild.ndkBuild em build.gradle.kts).
#
# Diferencas em relacao ao upstream, todas deliberadas:
#   - HDP_PATH aponta para a arvore vendorizada em ../../../vendor;
#   - os interruptores viraram valores fixos comentados (abaixo), em vez de
#     variaveis passaveis pela linha de comando -- este .so tem UMA
#     configuracao, e ela e a que vendor-files.txt cobre;
#   - os ramos das features desligadas foram REMOVIDOS, nao so zerados: as
#     fontes delas nao estao vendorizadas, entao um ramo vivo apontaria para
#     arquivo inexistente. Religar qualquer uma exige revendorizar (ver
#     vendor-hdiffpatch.sh --relist).
#   - o ramo armeabi-v7a saiu: este projeto empacota arm64-v8a e x86_64.

LOCAL_PATH := $(call my-dir)
include $(CLEAR_VARS)

LOCAL_MODULE := hpatchz

HDP_PATH := $(LOCAL_PATH)/../../../vendor/HDiffPatch

# ---------------------------------------------------------------------------
# Interruptores (nomes iguais aos do upstream, para a comparacao continuar
# possivel). LIGADO/DESLIGADO e por que:
#
#   MT     = 1  multi-thread de I/O e descompressao. Fica ligado nao porque o
#               patch padrao use (ApkPatcher passa threadNum=1), mas porque
#               diffs de janela (-WD, o modo recomendado pelo upstream para
#               arquivos grandes) so aproveitam paralelismo com este codigo
#               presente. Desligar economizaria ~10 KB e fecharia a porta.
#   ZLIB   = 1  descompressor deflate via -lz (libz.so do proprio Android:
#               nenhuma fonte de zlib e vendorizada). Tambem habilita o
#               checksum crc32.
#   LZMA   = 1  hdiffz -c-lzma / -c-lzma2.
#   ZSTD   = 1  hdiffz -c-zstd. E o compressor PADRAO do hdiffz: sem isto o
#               .so rejeitaria o patch mais provavel de todos.
#   MD5    = 1  } plugins de checksum. Nao os usamos -- a verificacao que vale
#   XXH    = 1  } neste modulo e o SHA-256 do arquivo produzido, em Kotlin.
#               Ficam ligados por interoperabilidade: o gerador roda em outro
#               processo, em outro repositorio, e se algum dia passar
#               -C-md5 ou -C-xxh128 o patch falharia no aparelho com
#               HPATCH_CHECKSUMSET_ERROR. Custo medido dos dois: 10.264 bytes
#               no .so arm64 (102.648 com, 92.384 sem). Barato demais para
#               valer o acoplamento.
#
#   BROTLI = 0  compressor que este projeto nunca vai gerar.
#   VCD    = 0  formato VCDIFF (xdelta3/open-vcdiff). Fora de escopo: o patch
#               vem do nosso hdiffz.
#   BSD    = 0  formato bsdiff4/endsley. Idem. (Upstream exige BZIP2 junto.)
#   BZIP2  = 0  so existiria para o BSD acima.
#   DIR    = 0  patch de DIRETORIO. O alvo aqui e um arquivo unico (o APK);
#               ligar isto arrastaria dir_patch/* e a nocao de manifesto de
#               diretorio para dentro de um modulo que so troca um arquivo.
# ---------------------------------------------------------------------------

DEF_FLAGS := -Os -flto -DANDROID_NDK -DNDEBUG -D_LARGEFILE_SOURCE \
             -D_IS_NEED_DEFAULT_CompressPlugin=0 \
             -D_IS_NEED_CACHE_OLD_BY_COVERS=0 \
             -D_IS_NEED_CACHE_OLD_ALL=1
LINK_FLAGS := -llog

# _IS_NEED_CACHE_OLD_ALL=1 (herdado do upstream) e a razao de o cacheMemory
# escolhido em ApkPatcher.kt ser load-bearing: com ele, se cacheMemory >=
# tamanho do APK antigo, o patcher carrega o arquivo velho INTEIRO na
# memoria. Passar "mais cache para ir mais rapido" viraria, sem aviso, um
# pico de +33 MB de heap nativo no celular. Ver DEFAULT_CACHE_MEMORY_BYTES.

# Features desligadas -- os -D precisam existir mesmo sem os fontes, porque
# hpatchz.c ramifica em cima deles.
DEF_FLAGS += -D_IS_NEED_BSDIFF=0 -D_IS_NEED_VCDIFF=0 -D_IS_NEED_DIR_DIFF_PATCH=0

Src_Files := $(HDP_PATH)/builds/android_ndk_jni_mk/hpatch_jni.c \
             $(HDP_PATH)/builds/android_ndk_jni_mk/hpatch.c \
             $(HDP_PATH)/file_for_patch.c \
             $(HDP_PATH)/libHDiffPatch/HPatch/patch.c

# MT = 1
DEF_FLAGS += -D_IS_USED_MULTITHREAD=1
Src_Files += $(HDP_PATH)/libHDiffPatch/HPatch/hpatch_mt/_hcache_old_mt.c \
             $(HDP_PATH)/libHDiffPatch/HPatch/hpatch_mt/_hcache_window_old_mt.c \
             $(HDP_PATH)/libHDiffPatch/HPatch/hpatch_mt/_hinput_mt.c \
             $(HDP_PATH)/libHDiffPatch/HPatch/hpatch_mt/_houtput_mt.c \
             $(HDP_PATH)/libHDiffPatch/HPatch/hpatch_mt/_hpatch_mt.c \
             $(HDP_PATH)/libHDiffPatch/HPatch/hpatch_mt/hpatch_mt.c \
             $(HDP_PATH)/libParallel/parallel_import_c.c

# Checksums: fadler64 sempre (e o default do hdiffz), crc32 vem de graca com
# o zlib do sistema, md5 e xxh3/xxh128 pelos motivos comentados acima.
DEF_FLAGS += -D_IS_NEED_DEFAULT_ChecksumPlugin=0 -D_ChecksumPlugin_fadler64
Src_Files += $(HDP_PATH)/libHDiffPatch/HDiff/private_diff/limit_mem_diff/adler_roll.c

DEF_FLAGS += -D_ChecksumPlugin_crc32

MD5_PATH  := $(HDP_PATH)/../libmd5
DEF_FLAGS += -D_ChecksumPlugin_md5 -I$(MD5_PATH)
Src_Files += $(MD5_PATH)/md5.c

XXH_PATH  := $(HDP_PATH)/../xxHash
DEF_FLAGS += -D_ChecksumPlugin_xxh3 -D_ChecksumPlugin_xxh128 -I$(XXH_PATH)

# ZLIB = 1 -- descompressor deflate contra a libz do sistema; nenhuma fonte
# de zlib entra no vendor.
DEF_FLAGS += -D_CompressPlugin_zlib
LINK_FLAGS += -lz

# LZMA = 1
LZMA_PATH := $(HDP_PATH)/../lzma/C
DEF_FLAGS += -D_CompressPlugin_lzma -D_CompressPlugin_lzma2 -DZ7_ST -I$(LZMA_PATH)
Src_Files += $(LZMA_PATH)/LzmaDec.c \
             $(LZMA_PATH)/Lzma2Dec.c
ifeq ($(TARGET_ARCH_ABI),arm64-v8a)
  # Decodificador LZMA em assembly arm64 -- e o caminho que roda no aparelho
  # de verdade (x86_64 so existe para o emulador).
  DEF_FLAGS += -DZ7_LZMA_DEC_OPT
  Src_Files += $(LZMA_PATH)/../Asm/arm64/LzmaDecOpt.S
endif

# ZSTD = 1 -- so o lado de DEScompressao (lib/decompress + lib/common).
ZSTD_PATH := $(HDP_PATH)/../zstd/lib
DEF_FLAGS += -D_CompressPlugin_zstd -I$(ZSTD_PATH) -I$(ZSTD_PATH)/common -I$(ZSTD_PATH)/decompress \
             -DZSTD_HAVE_WEAK_SYMBOLS=0 -DZSTD_TRACE=0 -DZSTD_DISABLE_ASM=1 -DZSTDLIB_HIDDEN= \
             -DZSTDLIB_VISIBLE= -DZDICTLIB_VISIBLE= -DZSTDERRORLIB_VISIBLE= \
             -DDYNAMIC_BMI2=0 -DZSTD_LEGACY_SUPPORT=0 -DZSTD_LIB_DEPRECATED=0 -DHUF_FORCE_DECOMPRESS_X1=1 \
             -DZSTD_FORCE_DECOMPRESS_SEQUENCES_SHORT=1 -DZSTD_NO_INLINE=1 -DZSTD_STRIP_ERROR_STRINGS=1 \
             -DZSTDERRORLIB_VISIBILITY=
Src_Files += $(ZSTD_PATH)/common/debug.c \
             $(ZSTD_PATH)/common/entropy_common.c \
             $(ZSTD_PATH)/common/error_private.c \
             $(ZSTD_PATH)/common/fse_decompress.c \
             $(ZSTD_PATH)/common/xxhash.c \
             $(ZSTD_PATH)/common/zstd_common.c \
             $(ZSTD_PATH)/decompress/huf_decompress.c \
             $(ZSTD_PATH)/decompress/zstd_ddict.c \
             $(ZSTD_PATH)/decompress/zstd_decompress.c \
             $(ZSTD_PATH)/decompress/zstd_decompress_block.c

LOCAL_SRC_FILES := $(Src_Files)
LOCAL_LDLIBS    := $(LINK_FLAGS)
LOCAL_CFLAGS    := $(DEF_FLAGS)
include $(BUILD_SHARED_LIBRARY)
