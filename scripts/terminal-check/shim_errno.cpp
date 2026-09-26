#include <cerrno>
extern "C" int* __errno(void) { return &errno; }
