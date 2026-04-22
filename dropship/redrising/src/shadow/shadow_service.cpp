#include <windows.h>
#include <winsvc.h>
#include <string>
#include <atomic>

static SERVICE_STATUS        gSvcStatus;
static SERVICE_STATUS_HANDLE gSvcStatusHandle;
static HANDLE                gSvcStopEvent = INVALID_HANDLE_VALUE;

extern void RunClient();
extern std::atomic<bool>& GetRunningFlag();

static void ReportSvcStatus(DWORD dwCurrentState, DWORD dwWin32ExitCode, DWORD dwWaitHint) {
    gSvcStatus.dwCurrentState = dwCurrentState;
    gSvcStatus.dwWin32ExitCode = dwWin32ExitCode;
    gSvcStatus.dwWaitHint = dwWaitHint;
    gSvcStatus.dwControlsAccepted = (dwCurrentState == SERVICE_START_PENDING) ? 0 : SERVICE_ACCEPT_STOP | SERVICE_ACCEPT_SHUTDOWN;
    if (dwCurrentState == SERVICE_RUNNING || dwCurrentState == SERVICE_STOPPED)
        gSvcStatus.dwCheckPoint = 0;
    else
        gSvcStatus.dwCheckPoint++;
    SetServiceStatus(gSvcStatusHandle, &gSvcStatus);
}

static VOID WINAPI ServiceCtrlHandler(DWORD dwCtrl) {
    switch (dwCtrl) {
        case SERVICE_CONTROL_STOP:
        case SERVICE_CONTROL_SHUTDOWN:
            ReportSvcStatus(SERVICE_STOP_PENDING, NO_ERROR, 5000);
            SetEvent(gSvcStopEvent);
            GetRunningFlag() = false;
            break;
        default:
            break;
    }
}

static VOID WINAPI ServiceMain(DWORD dwArgc, LPWSTR *lpszArgv) {
    gSvcStatusHandle = RegisterServiceCtrlHandlerW(L"RedRising", ServiceCtrlHandler);
    if (!gSvcStatusHandle) return;

    gSvcStatus.dwServiceType = SERVICE_WIN32_OWN_PROCESS;
    gSvcStatus.dwServiceSpecificExitCode = 0;
    ReportSvcStatus(SERVICE_START_PENDING, NO_ERROR, 3000);

    gSvcStopEvent = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (!gSvcStopEvent) {
        ReportSvcStatus(SERVICE_STOPPED, NO_ERROR, 0);
        return;
    }

    ReportSvcStatus(SERVICE_RUNNING, NO_ERROR, 0);

    RunClient();

    WaitForSingleObject(gSvcStopEvent, INFINITE);
    CloseHandle(gSvcStopEvent);

    ReportSvcStatus(SERVICE_STOPPED, NO_ERROR, 0);
}

bool InstallSelfAsService() {
    wchar_t exePath[MAX_PATH];
    if (!GetModuleFileNameW(NULL, exePath, MAX_PATH))
        return false;

    SC_HANDLE scm = OpenSCManagerW(NULL, NULL, SC_MANAGER_CREATE_SERVICE);
    if (!scm) return false;

    // Delete existing service if present
    SC_HANDLE existing = OpenServiceW(scm, L"RedRising", SERVICE_ALL_ACCESS | DELETE);
    if (existing) {
        SERVICE_STATUS status;
        ControlService(existing, SERVICE_CONTROL_STOP, &status);
        DeleteService(existing);
        CloseServiceHandle(existing);
        Sleep(1000);
    }

    SC_HANDLE svc = CreateServiceW(
        scm,
        L"RedRising",
        L"RedRising Client",
        SERVICE_ALL_ACCESS,
        SERVICE_WIN32_OWN_PROCESS,
        SERVICE_AUTO_START,
        SERVICE_ERROR_NORMAL,
        exePath,
        NULL, NULL, NULL, NULL, NULL);

    if (!svc) {
        CloseServiceHandle(scm);
        return false;
    }

    StartServiceW(svc, 0, nullptr);

    CloseServiceHandle(svc);
    CloseServiceHandle(scm);
    return true;
}

bool UninstallService() {
    SC_HANDLE scm = OpenSCManagerW(NULL, NULL, SC_MANAGER_CONNECT);
    if (!scm) return false;

    SC_HANDLE svc = OpenServiceW(scm, L"RedRising", SERVICE_STOP | DELETE | SERVICE_QUERY_STATUS);
    if (!svc) {
        CloseServiceHandle(scm);
        return false;
    }

    SERVICE_STATUS status;
    ControlService(svc, SERVICE_CONTROL_STOP, &status);

    for (int i = 0; i < 100; i++) {
        if (QueryServiceStatus(svc, &status) && status.dwCurrentState == SERVICE_STOPPED)
            break;
        Sleep(100);
    }

    bool ok = DeleteService(svc);
    CloseServiceHandle(svc);
    CloseServiceHandle(scm);
    return ok;
}

void StartServiceDispatcher() {
    SERVICE_TABLE_ENTRYW svcTable[] = {
        { (LPWSTR)L"RedRising", (LPSERVICE_MAIN_FUNCTIONW)ServiceMain },
        { NULL, NULL }
    };
    StartServiceCtrlDispatcherW(svcTable);
}
