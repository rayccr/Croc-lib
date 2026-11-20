// Windows x64 C ABI loading example. Build from a VS x64 developer shell.
// cl /nologo /std:c++20 /EHsc version.cpp
#include <windows.h>
#include <cstdint>
#include <iostream>

int main()
{
    HMODULE hEngine = LoadLibraryW(L"croc_engine.dll");
    if (hEngine == nullptr) { return 1; }
    using FInitialize = int32_t (*)();
    using FVersion = char* (*)();
    FInitialize pfnInitialize = reinterpret_cast<FInitialize>(GetProcAddress(hEngine, "Croc_Initialize"));
    FInitialize pfnShutdown = reinterpret_cast<FInitialize>(GetProcAddress(hEngine, "Croc_Shutdown"));
    FVersion pfnVersion = reinterpret_cast<FVersion>(GetProcAddress(hEngine, "Croc_GetEngineVersion"));
    if (pfnInitialize == nullptr || pfnShutdown == nullptr || pfnVersion == nullptr) { return 2; }
    if (pfnInitialize() != 0) { return 3; }
    std::cout << pfnVersion() << '\n';
    return pfnShutdown() == 0 ? 0 : 4;
}
