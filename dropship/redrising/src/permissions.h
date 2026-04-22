#pragma once
#include <windows.h>
#include <string>

bool IsRunningAsLocalSystem();
void LaunchConsoleInSessionId();
bool ElevateToSystem(const std::wstring& exePath);
