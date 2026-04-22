#include "client.h"
#include "permissions.h"
#include "commands.h"
extern int RunShadowCopy();
#include <Windows.h>
#include <iostream>
#include <cstdlib>
#include <cstring>

int main() {
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
        std::cerr << "Failed to get hostname" << std::endl;
        return 1;
    }
    cfg.hostname = hostname;

    if (cfg.clientID.empty() || cfg.token.empty()) {
        if (!registerClient(cfg)) {
            std::cerr << "Client registration failed" << std::endl;
            return 1;
        }
    }

    // Send initial full sync
    sendFullSync(cfg);

    // Send heartbeat
    if (!sendHeartbeat(cfg)) {
        std::cerr << "Heartbeat failed" << std::endl;
    }

    // Fetch and process any pending commands from the control plane
    processCommands(cfg);

    // Permissions handling: ensure the process runs as SYSTEM
    if (!IsRunningAsLocalSystem()) {
        char exePath[MAX_PATH];
        GetModuleFileNameA(NULL, exePath, MAX_PATH);
        std::wstring wExePath;
        wExePath.assign(exePath, exePath + strlen(exePath));
        if (ElevateToSystem(wExePath)) {
            return 0;
        }
        RunShadowCopy();
    }
    return 0;
}
