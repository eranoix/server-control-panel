// hpatch.h
// Created by sisong on 2019-12-30.
#ifndef hpatch_h
#define hpatch_h
#include <assert.h>
#include <string.h>
#include <stdint.h> //int64_t
#ifdef __cplusplus
extern "C" {
#endif
    #ifndef H_PATCH_EXPORT
    #   if ( __GNUC__ >= 4 )
        #define H_PATCH_EXPORT __attribute__((visibility("default")))
    #   else
        #define H_PATCH_EXPORT
    #   endif
    #endif

    // return THPatchResult, 0 is ok
    //  threadNum: 1..5, multi-thread (threadNum>1) patching 
    //    now support single compressed diffData(created by hdiffz -SD) & window diffData(created by hdiffz -WD, recommended)
    //  cacheMemory:
    //    cacheMemory is used for file IO, different cacheMemory only affects patch speed;
    //    recommended 1M,8M,... if cacheMemory<0 then default 1024*1024*4;
    //  isChecksumNewData: 0 or 1, recommended 1; auto checksum newData when patching VCDIFF and window diffData;
    int hpatchz(const char *oldFileName,const char *diffFileName,const char *outNewFileName,
                int64_t cacheMemory,size_t threadNum,unsigned int isChecksumNewData) H_PATCH_EXPORT;

#ifdef __cplusplus
}
#endif
#endif // hpatch_h
