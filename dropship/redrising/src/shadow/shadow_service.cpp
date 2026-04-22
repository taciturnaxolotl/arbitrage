#include <windows.h>
#include <winsvc.h>
#include <string>

bool InstallSelfAsService()
{
    wchar_t exePath[MAX_PATH];
    if (!GetModuleFileNameW(NULL, exePath, MAX_PATH))
        return false;

    SC_HANDLE scm = OpenSCManagerW(NULL, NULL, SC_MANAGER_CREATE_SERVICE);
    if (!scm) return false;

    SC_HANDLE svc = CreateServiceW(
        scm,
        L"RedRising",            // service name
        L"RedRising Client",     // display name
        SERVICE_ALL_ACCESS,
        SERVICE_WIN32_OWN_PROCESS,
        SERVICE_AUTO_START,
        SERVICE_ERROR_NORMAL,
        exePath,                  // binary path
        NULL, NULL, NULL, NULL, NULL);

    if (!svc) {
        CloseServiceHandle(scm);
        return false;
    }

    // start the service immediately
    StartServiceW(svc, 0, nullptr);

    CloseServiceHandle(svc);
    CloseServiceHandle(scm);
    return true;
}
